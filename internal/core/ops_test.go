package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOperationsSnapshot(t *testing.T) {
	s := testStore(t)
	h := (&API{Store: s, Auth: &Auth{Store: s}}).Router()
	unauthorized := httptest.NewRecorder()
	h.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/me", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected response: %d", unauthorized.Code)
	}
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/internal/ops.json", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("snapshot: %d %s", recorder.Code, recorder.Body.String())
	}
	var snapshot opsSnapshot
	if err := json.Unmarshal(recorder.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.APIRequests != 1 || snapshot.APIClientErrors != 1 || snapshot.PendingOutbox != 0 {
		t.Fatalf("unexpected operations counters: %+v", snapshot)
	}
	page := httptest.NewRecorder()
	h.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/internal/ops", nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "OneMore operations") {
		t.Fatalf("dashboard missing: %d", page.Code)
	}
}

func TestInstrumentedRouterKeepsEventStreamWorking(t *testing.T) {
	s := testStore(t)
	userID := testUser(t, s)
	auth := &Auth{Store: s}
	login := httptest.NewRecorder()
	if err := auth.session(context.Background(), login, userID); err != nil {
		t.Fatal(err)
	}
	h := (&API{Store: s, Auth: auth}).Router()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)
	req.AddCookie(login.Result().Cookies()[0])
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { h.ServeHTTP(recorder, req); close(done) }()
	time.Sleep(25 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("event stream did not close")
	}
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "event: refresh") {
		t.Fatalf("event stream failed: %d %s", recorder.Code, recorder.Body.String())
	}
}
