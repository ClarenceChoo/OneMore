package core

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func authRequest(t *testing.T, h http.Handler, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	return r
}

func TestLocalProfilesHaveIndependentSessions(t *testing.T) {
	s := testStore(t)
	h := (&API{Store: s, Auth: &Auth{Store: s, Local: true}}).Router()
	first := authRequest(t, h, "/auth/register", map[string]string{"name": "First Person", "handle": "first_person", "password": "a-long-password-1"}, nil)
	if first.Code != http.StatusCreated {
		t.Fatalf("first registration: %d %s", first.Code, first.Body.String())
	}
	second := authRequest(t, h, "/auth/register", map[string]string{"name": "Second Person", "handle": "second_person", "password": "a-long-password-2"}, nil)
	if second.Code != http.StatusCreated {
		t.Fatalf("second registration: %d %s", second.Code, second.Body.String())
	}
	firstCookie, secondCookie := first.Result().Cookies()[0], second.Result().Cookies()[0]
	if firstCookie.Value == secondCookie.Value {
		t.Fatal("different profiles shared a session")
	}
	for _, tc := range []struct {
		cookie *http.Cookie
		name   string
	}{{firstCookie, "First Person"}, {secondCookie, "Second Person"}} {
		req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
		req.AddCookie(tc.cookie)
		r := httptest.NewRecorder()
		h.ServeHTTP(r, req)
		if r.Code != http.StatusOK || !bytes.Contains(r.Body.Bytes(), []byte(tc.name)) {
			t.Fatalf("session for %s: %d %s", tc.name, r.Code, r.Body.String())
		}
	}
	duplicate := authRequest(t, h, "/auth/register", map[string]string{"name": "Duplicate", "handle": "FIRST_PERSON", "password": "a-long-password-3"}, nil)
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate handle: %d %s", duplicate.Code, duplicate.Body.String())
	}
	wrong := authRequest(t, h, "/auth/local/login", map[string]string{"handle": "first_person", "password": "wrong-password"}, nil)
	if wrong.Code != http.StatusUnauthorized || len(wrong.Result().Cookies()) != 0 {
		t.Fatalf("wrong password: %d %s", wrong.Code, wrong.Body.String())
	}
	login := authRequest(t, h, "/auth/local/login", map[string]string{"handle": "FIRST_PERSON", "password": "a-long-password-1"}, nil)
	if login.Code != http.StatusOK || len(login.Result().Cookies()) != 1 {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	logoutReq := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	logoutReq.AddCookie(firstCookie)
	logout := httptest.NewRecorder()
	h.ServeHTTP(logout, logoutReq)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout: %d", logout.Code)
	}
	meReq := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	meReq.AddCookie(firstCookie)
	me := httptest.NewRecorder()
	h.ServeHTTP(me, meReq)
	if me.Code != http.StatusUnauthorized {
		t.Fatalf("logged-out session remained active: %d", me.Code)
	}
}

func TestLocalAuthDisabled(t *testing.T) {
	s := testStore(t)
	h := (&API{Store: s, Auth: &Auth{Store: s}}).Router()
	r := authRequest(t, h, "/auth/register", map[string]string{"name": "Person", "handle": "person", "password": "a-long-password"}, nil)
	if r.Code != http.StatusNotFound {
		t.Fatalf("registration should be disabled: %d", r.Code)
	}
}
