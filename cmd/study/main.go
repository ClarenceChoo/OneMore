// study runs a repeatable mixed HTTP workload against an isolated benchmark database.
package main

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/clarencechoo/onemore/internal/core"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type queryCounter struct{ total atomic.Int64 }

func (q *queryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	q.total.Add(1)
	return ctx
}
func (*queryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

type sample struct {
	op     string
	ms     float64
	status int
}
type metrics struct {
	Requests   int            `json:"requests"`
	Errors     int            `json:"errors"`
	Throughput float64        `json:"requestsPerSecond"`
	P50        float64        `json:"p50Ms"`
	P95        float64        `json:"p95Ms"`
	P99        float64        `json:"p99Ms"`
	Statuses   map[int]int    `json:"statusCounts"`
	ByOp       map[string]any `json:"byOperation,omitempty"`
}
type scenario struct {
	DetailMode       string  `json:"detailMode"`
	Cache            bool    `json:"cache"`
	DBQueries        int64   `json:"dbQueries"`
	DBQueriesPerCall float64 `json:"dbQueriesPerRequest"`
	Metrics          metrics `json:"metrics"`
}

func summarize(samples []sample, seconds float64) metrics {
	latencies := make([]float64, 0, len(samples))
	m := metrics{Requests: len(samples), Throughput: float64(len(samples)) / seconds, Statuses: map[int]int{}}
	for _, s := range samples {
		m.Statuses[s.status]++
		if s.status != 200 {
			m.Errors++
		} else {
			latencies = append(latencies, s.ms)
		}
	}
	sort.Float64s(latencies)
	percentile := func(p float64) float64 {
		if len(latencies) == 0 {
			return 0
		}
		return latencies[int(float64(len(latencies)-1)*p)]
	}
	m.P50, m.P95, m.P99 = percentile(.50), percentile(.95), percentile(.99)
	return m
}

func seed(ctx context.Context, db *pgxpool.Pool, plans int) error {
	schema, err := os.ReadFile("db/schema.sql")
	if err != nil {
		return err
	}
	if _, err = db.Exec(ctx, string(schema)); err != nil {
		return err
	}
	if _, err = db.Exec(ctx, `TRUNCATE idempotency_records,outbox_events,notifications,offers,memberships,invitations,sessions,users RESTART IDENTITY CASCADE`); err != nil {
		return err
	}
	_, err = db.Exec(ctx, `INSERT INTO users(id,subject,name) SELECT md5('study-user-'||g)::uuid,'study:'||g,'Member '||g FROM generate_series(1,501) g`)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `INSERT INTO invitations(id,host_id,title,category,area,location,starts_at,join_deadline,spaces)
SELECT md5('study-plan-'||g)::uuid,md5('study-user-1')::uuid,'Activity '||g,
  (ARRAY['Badminton','Board games','Study','Food'])[1+(g%4)],
  (ARRAY['Central','North','East','West'])[1+(g%4)],'Community space',
  clock_timestamp()+((60+(g%1200))*interval '1 minute'),
  clock_timestamp()+((30+(g%1200))*interval '1 minute'),3 FROM generate_series(1,$1) g`, plans)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `INSERT INTO memberships(invitation_id,user_id,status,waitlist_order)
SELECT md5('study-plan-'||p)::uuid,md5('study-user-'||(2+((p*7+slot)%499)))::uuid,
  CASE WHEN slot<=2 THEN 'confirmed' ELSE 'waitlisted' END,nextval('waitlist_order_seq')
FROM generate_series(1,$1) p CROSS JOIN generate_series(1,4) slot`, plans)
	if err != nil {
		return err
	}
	raw := "study-session"
	hash := sha256.Sum256([]byte(raw))
	_, err = db.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,md5('study-user-501')::uuid,clock_timestamp()+interval '1 day')`, hex.EncodeToString(hash[:]))
	return err
}

func run(ctx context.Context, db *pgxpool.Pool, redisClient *redis.Client, counter *queryCounter, plans, workers int, duration time.Duration, cache, sequential bool) (scenario, error) {
	if _, err := db.Exec(ctx, `DELETE FROM memberships WHERE user_id=md5('study-user-501')::uuid`); err != nil {
		return scenario{}, err
	}
	store := &core.Store{DB: db, FeedDetailsSequential: sequential, DisableFeedCache: !cache}
	if cache {
		store.Redis = redisClient
	}
	server := httptest.NewServer((&core.API{Store: store, Auth: &core.Auth{Store: store}}).Router())
	defer server.Close()
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: workers, MaxConnsPerHost: workers}}
	defer client.CloseIdleConnections()
	request := func(op, target string) sample {
		method := http.MethodGet
		if op == "join" || op == "leave" {
			method = http.MethodPost
		}
		req, _ := http.NewRequest(method, server.URL+target, nil)
		req.AddCookie(&http.Cookie{Name: "onemore_session", Value: "study-session"})
		started := time.Now()
		response, err := client.Do(req)
		s := sample{op: op, ms: float64(time.Since(started).Microseconds()) / 1000}
		if err != nil {
			return s
		}
		s.status = response.StatusCode
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		s.ms = float64(time.Since(started).Microseconds()) / 1000
		return s
	}
	// Warm both feed variants before measuring cache-enabled runs.
	if cache {
		request("browse", "/api/plans")
		request("filter", "/api/plans?category=Study&area=East")
	}
	before := counter.total.Load()
	started := time.Now()
	stop := started.Add(duration)
	out := make(chan sample, workers*4)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			planID := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("study-plan-%d", 1+worker%plans))))
			planID = planID[:8] + "-" + planID[8:12] + "-" + planID[12:16] + "-" + planID[16:20] + "-" + planID[20:32]
			for i := 0; time.Now().Before(stop); i++ {
				switch i % 20 {
				case 14, 15, 16, 17:
					out <- request("filter", "/api/plans?category=Study&area=East")
				case 18:
					out <- request("join", "/api/plans/"+planID+"/join")
				case 19:
					out <- request("leave", "/api/plans/"+planID+"/leave")
				default:
					out <- request("browse", "/api/plans")
				}
			}
		}(worker)
	}
	go func() { wg.Wait(); close(out) }()
	all := []sample{}
	byOp := map[string][]sample{}
	for s := range out {
		all = append(all, s)
		byOp[s.op] = append(byOp[s.op], s)
	}
	seconds := time.Since(started).Seconds()
	queries := counter.total.Load() - before
	result := scenario{DetailMode: "batch", Cache: cache, DBQueries: queries, Metrics: summarize(all, seconds)}
	if sequential {
		result.DetailMode = "sequential"
	}
	if len(all) > 0 {
		result.DBQueriesPerCall = float64(queries) / float64(len(all))
	}
	result.Metrics.ByOp = map[string]any{}
	for op, samples := range byOp {
		result.Metrics.ByOp[op] = summarize(samples, seconds)
	}
	return result, nil
}

func main() {
	plans := flag.Int("plans", 3000, "number of plans to seed")
	workers := flag.Int("workers", 20, "concurrent HTTP clients")
	duration := flag.Duration("duration", 10*time.Second, "duration per scenario")
	flag.Parse()
	if *plans < *workers || *plans < 100 || *workers < 1 || *duration < time.Second {
		panic("use at least 100 plans, at least as many plans as workers, and a duration of at least one second")
	}
	ctx := context.Background()
	dbURL := os.Getenv("STUDY_DATABASE_URL")
	parsed, err := url.Parse(dbURL)
	if err != nil || !strings.HasSuffix(strings.TrimPrefix(parsed.Path, "/"), "_bench") {
		panic("STUDY_DATABASE_URL must point to an isolated database ending in _bench")
	}
	redisURL := os.Getenv("STUDY_REDIS_URL")
	options, err := redis.ParseURL(redisURL)
	if err != nil || options.DB == 0 {
		panic("STUDY_REDIS_URL must select a non-default Redis database")
	}
	redisClient := redis.NewClient(options)
	defer redisClient.Close()
	if err = redisClient.Ping(ctx).Err(); err != nil {
		panic(err)
	}
	q := &queryCounter{}
	config, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		panic(err)
	}
	config.ConnConfig.Tracer = q
	config.MaxConns = int32(*workers + 5)
	db, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		panic(err)
	}
	defer db.Close()
	var name string
	if err = db.QueryRow(ctx, `SELECT current_database()`).Scan(&name); err != nil || !strings.HasSuffix(name, "_bench") {
		panic("benchmark database safety check failed")
	}
	if err = seed(ctx, db, *plans); err != nil {
		panic(err)
	}
	report := struct {
		Plans       int        `json:"plans"`
		Memberships int        `json:"memberships"`
		Workers     int        `json:"workers"`
		Duration    string     `json:"durationPerScenario"`
		Scenarios   []scenario `json:"scenarios"`
	}{Plans: *plans, Memberships: *plans * 4, Workers: *workers, Duration: duration.String()}
	for _, sequential := range []bool{true, false} {
		for _, cache := range []bool{false, true} {
			result, err := run(ctx, db, redisClient, q, *plans, *workers, *duration, cache, sequential)
			if err != nil {
				panic(err)
			}
			report.Scenarios = append(report.Scenarios, result)
		}
	}
	_ = json.NewEncoder(os.Stdout).Encode(report)
}
