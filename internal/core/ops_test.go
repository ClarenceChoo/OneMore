package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
