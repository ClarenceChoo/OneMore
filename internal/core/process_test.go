package core

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type childProcess struct {
	cmd      *exec.Cmd
	output   bytes.Buffer
	finished chan struct{}
	err      error
}

func processEnv(values map[string]string) []string {
	env := []string{}
	for _, item := range os.Environ() {
		key := strings.SplitN(item, "=", 2)[0]
		if _, replaced := values[key]; !replaced {
			env = append(env, item)
		}
	}
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	return env
}

func startChild(t *testing.T, binary string, args []string, env map[string]string) *childProcess {
	t.Helper()
	p := &childProcess{finished: make(chan struct{})}
	p.cmd = exec.Command(binary, args...)
	p.cmd.Dir = "../.."
	p.cmd.Env = processEnv(env)
	p.cmd.Stdout, p.cmd.Stderr = &p.output, &p.output
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { p.err = p.cmd.Wait(); close(p.finished) }()
	t.Cleanup(p.stop)
	return p
}

func (p *childProcess) stop() {
	select {
	case <-p.finished:
		return
	default:
		_ = p.cmd.Process.Kill()
		<-p.finished
	}
}

func buildProcessBinary(t *testing.T, tags string) string {
	t.Helper()
	name := "onemore-normal"
	if tags != "" {
		name = "onemore-chaos"
	}
	path := filepath.Join(t.TempDir(), name)
	args := []string{"build", "-o", path}
	if tags != "" {
		args = append(args, "-tags", tags)
	}
	args = append(args, "./cmd/onemore")
	cmd := exec.Command("go", args...)
	cmd.Dir = "../.."
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build child process: %v\n%s", err, output)
	}
	return path
}

func freePort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().String()
	_ = listener.Close()
	return port
}

func waitFor(t *testing.T, label string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", label)
}

func TestTwoAPIsAndWorkerRecoveryAcrossProcesses(t *testing.T) {
	dbURL, redisURL := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_REDIS_URL")
	if dbURL == "" || redisURL == "" {
		t.Skip("set TEST_DATABASE_URL and TEST_REDIS_URL for process recovery test")
	}
	s := testStore(t)
	ctx := context.Background()
	normal, chaos := buildProcessBinary(t, ""), buildProcessBinary(t, "chaos")
	baseEnv := map[string]string{"DATABASE_URL": dbURL, "REDIS_URL": redisURL, "LOCAL_AUTH": "true", "OIDC_ISSUER": "", "COOKIE_SECURE": "false"}
	addresses := []string{freePort(t), freePort(t)}
	for _, address := range addresses {
		env := map[string]string{}
		for key, value := range baseEnv {
			env[key] = value
		}
		env["LISTEN_ADDR"] = address
		p := startChild(t, normal, nil, env)
		waitFor(t, "API on "+address, func() bool {
			response, err := http.Get("http://" + address + "/health")
			if err != nil {
				select {
				case <-p.finished:
					t.Fatalf("API exited: %v\n%s", p.err, p.output.String())
				default:
				}
				return false
			}
			_ = response.Body.Close()
			return response.StatusCode == 200
		})
	}
	host, first, second := testUser(t, s), testUser(t, s), testUser(t, s)
	plan := testPlan(t, s, host, 1)
	cookies := make([]*http.Cookie, 2)
	for i, userID := range []string{first, second} {
		recorder := httptest.NewRecorder()
		if err := (&Auth{Store: s}).session(ctx, recorder, userID); err != nil {
			t.Fatal(err)
		}
		cookies[i] = recorder.Result().Cookies()[0]
	}
	workers := []*childProcess{startChild(t, normal, []string{"-mode=worker"}, baseEnv), startChild(t, normal, []string{"-mode=worker"}, baseEnv)}
	client := &http.Client{Timeout: 5 * time.Second}
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for i, address := range addresses {
		wg.Add(1)
		go func(i int, address string) {
			defer wg.Done()
			req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("http://%s/api/plans/%s/join", address, plan.ID), nil)
			req.AddCookie(cookies[i])
			response, err := client.Do(req)
			if err != nil {
				statuses <- 0
				return
			}
			_ = response.Body.Close()
			statuses <- response.StatusCode
		}(i, address)
	}
	wg.Wait()
	close(statuses)
	for status := range statuses {
		if status != 200 {
			t.Fatalf("concurrent claim returned %d", status)
		}
	}
	got, err := s.GetPlan(ctx, host, plan.ID)
	if err != nil || got.Confirmed != 1 || got.Waiting != 1 || got.Available != 0 {
		t.Fatalf("capacity after cross-process claims: %+v %v", got, err)
	}
	for _, worker := range workers {
		worker.stop()
	}
	if err := s.DeliverOutbox(ctx); err != nil {
		t.Fatal(err)
	}
	eventID := NewID()
	_, err = s.DB.Exec(ctx, `INSERT INTO outbox_events(id,kind,invitation_id,user_id,message) VALUES($1,'recovery_probe',$2,$3,'Recovery completed.')`, eventID, plan.ID, second)
	if err != nil {
		t.Fatal(err)
	}
	badEnv := map[string]string{}
	for key, value := range baseEnv {
		badEnv[key] = value
	}
	badEnv["REDIS_URL"] = "redis://127.0.0.1:1"
	bad := startChild(t, normal, []string{"-mode=worker"}, badEnv)
	time.Sleep(500 * time.Millisecond)
	bad.stop()
	if !strings.Contains(bad.output.String(), "failed to dial") && !strings.Contains(bad.output.String(), "outbox delivery") {
		t.Fatalf("worker did not attempt delivery during Redis outage: %s", bad.output.String())
	}
	var pending int
	if err = s.DB.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE id=$1 AND delivered_at IS NULL`, eventID).Scan(&pending); err != nil || pending != 1 {
		t.Fatalf("Redis outage lost event: pending=%d err=%v", pending, err)
	}
	crasher := startChild(t, chaos, []string{"-mode=worker"}, baseEnv)
	waitFor(t, "crash after Redis publish", func() bool {
		select {
		case <-crasher.finished:
			return true
		default:
			return false
		}
	})
	if crasher.cmd.ProcessState.ExitCode() != 77 {
		t.Fatalf("unexpected chaos exit: %v\n%s", crasher.err, crasher.output.String())
	}
	if err = s.DB.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE id=$1 AND delivered_at IS NULL`, eventID).Scan(&pending); err != nil || pending != 1 {
		t.Fatalf("crash committed delivery unexpectedly: pending=%d err=%v", pending, err)
	}
	startChild(t, normal, []string{"-mode=worker"}, baseEnv)
	startChild(t, normal, []string{"-mode=worker"}, baseEnv)
	waitFor(t, "recovered delivery", func() bool {
		var count int
		_ = s.DB.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE id=$1 AND delivered_at IS NOT NULL`, eventID).Scan(&count)
		return count == 1
	})
	var notifications int
	if err = s.DB.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE event_id=$1`, eventID).Scan(&notifications); err != nil || notifications != 1 {
		t.Fatalf("notification was not deduplicated: count=%d err=%v", notifications, err)
	}
	redisOptions, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(redisOptions)
	defer rdb.Close()
	entries, err := rdb.XRange(ctx, "onemore:events", "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	duplicates := 0
	for _, entry := range entries {
		if entry.Values["id"] == eventID {
			duplicates++
		}
	}
	if duplicates != 2 {
		t.Fatalf("expected two stream deliveries after crash, got %d", duplicates)
	}
}
