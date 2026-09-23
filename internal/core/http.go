package core

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type API struct {
	Store *Store
	Auth  *Auth
}

func jsonResponse(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeErr(w http.ResponseWriter, err error) {
	var e *AppError
	if !errors.As(err, &e) {
		log.Printf("request error: %v", err)
		e = &AppError{"internal", "Something went wrong. Try again.", 500}
	}
	jsonResponse(w, e.Status, map[string]string{"code": e.Code, "message": e.Message})
}
func decode(r *http.Request, v any) error {
	if r.Header.Get("Content-Type") != "application/json" {
		return Bad("invalid_content_type", "Send JSON to this endpoint.")
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 64*1024))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return Bad("invalid_json", "Check the form and try again.")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return Bad("invalid_json", "Send one JSON object.")
	}
	return nil
}
func (a *API) handler(fn func(http.ResponseWriter, *http.Request, User) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, err := a.Auth.User(r)
		if err != nil {
			writeErr(w, err)
			return
		}
		if err = fn(w, r, u); err != nil {
			writeErr(w, err)
		}
	}
}
func (a *API) Router() http.Handler {
	mux := http.NewServeMux()
	metrics := &opsMetrics{startedAt: time.Now()}
	mux.HandleFunc("GET /internal/ops", metrics.servePage)
	mux.HandleFunc("GET /internal/ops.json", func(w http.ResponseWriter, r *http.Request) {
		snapshot, err := metrics.snapshot(r.Context(), a.Store)
		if err != nil {
			writeErr(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		jsonResponse(w, http.StatusOK, snapshot)
	})
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		if err := a.Store.DB.Ping(r.Context()); err != nil {
			writeErr(w, err)
			return
		}
		jsonResponse(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /auth/config", a.Auth.Config)
	mux.HandleFunc("GET /auth/login", a.Auth.Login)
	mux.HandleFunc("GET /auth/callback", a.Auth.Callback)
	mux.HandleFunc("POST /auth/register", a.Auth.Register)
	mux.HandleFunc("POST /auth/local/login", a.Auth.LocalLogin)
	mux.HandleFunc("POST /auth/logout", a.Auth.Logout)
	mux.HandleFunc("GET /api/me", a.handler(func(w http.ResponseWriter, r *http.Request, u User) error { jsonResponse(w, 200, u); return nil }))
	mux.HandleFunc("GET /api/plans", a.handler(a.list))
	mux.HandleFunc("GET /api/recommendations", a.handler(a.recommendations))
	mux.HandleFunc("GET /api/preferences", a.handler(a.preferences))
	mux.HandleFunc("POST /api/preferences", a.handler(a.savePreferences))
	mux.HandleFunc("POST /api/discovery/events", a.handler(a.discoveryEvents))
	mux.HandleFunc("POST /api/plans", a.handler(a.create))
	mux.HandleFunc("GET /api/plans/{id}", a.handler(a.detail))
	mux.HandleFunc("GET /api/my-plans", a.handler(a.mine))
	mux.HandleFunc("POST /api/plans/{id}/join", a.handler(a.action("join")))
	mux.HandleFunc("POST /api/plans/{id}/leave", a.handler(a.action("leave")))
	mux.HandleFunc("POST /api/plans/{id}/cancel", a.handler(a.action("cancel")))
	mux.HandleFunc("POST /api/plans/{id}/offer/accept", a.handler(a.action("accept")))
	mux.HandleFunc("POST /api/plans/{id}/offer/decline", a.handler(a.action("decline")))
	mux.HandleFunc("GET /api/notifications", a.handler(a.notifications))
	mux.HandleFunc("POST /api/notifications/{id}/read", a.handler(a.readNotification))
	mux.HandleFunc("GET /api/events", a.handler(a.events))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		tracked := &statusWriter{ResponseWriter: w}
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/events" {
			defer func() {
				status := tracked.status
				if status == 0 {
					status = http.StatusOK
				}
				metrics.observe(time.Since(started), status)
			}()
		}
		w = tracked
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		if r.Method == http.MethodPost {
			if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
				writeErr(w, Forbidden("Cross-site requests are not allowed."))
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || !strings.EqualFold(u.Host, r.Host) {
					writeErr(w, Forbidden("Cross-site requests are not allowed."))
					return
				}
			}
		}
		mux.ServeHTTP(w, r)
	})
}
func planID(r *http.Request) (string, error) {
	id := r.PathValue("id")
	if !uuidPattern.MatchString(id) {
		return "", Bad("invalid_id", "The plan link is invalid.")
	}
	return id, nil
}
func (a *API) list(w http.ResponseWriter, r *http.Request, u User) error {
	q := r.URL.Query()
	f := FeedFilter{Category: q.Get("category"), Area: q.Get("area"), OpenOnly: q.Get("open") == "true", Cursor: q.Get("cursor")}
	if v := q.Get("before"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return Bad("invalid_time", "Choose a valid time filter.")
		}
		f.Before = t
	}
	page, err := a.Store.ListPlans(r.Context(), u.ID, f)
	if err != nil {
		return err
	}
	jsonResponse(w, 200, page)
	return nil
}
func (a *API) recommendations(w http.ResponseWriter, r *http.Request, u User) error {
	page, err := a.Store.Recommendations(r.Context(), u.ID)
	if err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, page)
	return nil
}
func (a *API) preferences(w http.ResponseWriter, r *http.Request, u User) error {
	p, err := a.Store.GetPreferences(r.Context(), u.ID)
	if err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, p)
	return nil
}
func (a *API) savePreferences(w http.ResponseWriter, r *http.Request, u User) error {
	var p DiscoveryPreferences
	if err := decode(r, &p); err != nil {
		return err
	}
	saved, err := a.Store.SavePreferences(r.Context(), u.ID, p)
	if err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, saved)
	return nil
}
func (a *API) discoveryEvents(w http.ResponseWriter, r *http.Request, u User) error {
	var in struct {
		Events []DiscoveryEventInput `json:"events"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if err := a.Store.RecordDiscoveryEvents(r.Context(), u.ID, in.Events); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
func (a *API) create(w http.ResponseWriter, r *http.Request, u User) error {
	var in CreatePlan
	if err := decode(r, &in); err != nil {
		return err
	}
	p, err := a.Store.Create(r.Context(), u.ID, r.Header.Get("Idempotency-Key"), in)
	if err != nil {
		return err
	}
	jsonResponse(w, 201, p)
	return nil
}
func (a *API) detail(w http.ResponseWriter, r *http.Request, u User) error {
	id, err := planID(r)
	if err != nil {
		return err
	}
	p, err := a.Store.GetPlan(r.Context(), u.ID, id)
	if err != nil {
		return err
	}
	jsonResponse(w, 200, p)
	return nil
}
func (a *API) mine(w http.ResponseWriter, r *http.Request, u User) error {
	plans, err := a.Store.MyPlans(r.Context(), u.ID)
	if err != nil {
		return err
	}
	jsonResponse(w, 200, plans)
	return nil
}
func (a *API) action(kind string) func(http.ResponseWriter, *http.Request, User) error {
	return func(w http.ResponseWriter, r *http.Request, u User) error {
		id, err := planID(r)
		if err != nil {
			return err
		}
		var p Plan
		switch kind {
		case "join":
			p, err = a.Store.Join(r.Context(), u.ID, id)
		case "leave":
			p, err = a.Store.Leave(r.Context(), u.ID, id)
		case "cancel":
			p, err = a.Store.Cancel(r.Context(), u.ID, id)
		case "accept":
			p, err = a.Store.OfferAction(r.Context(), u.ID, id, true)
		case "decline":
			p, err = a.Store.OfferAction(r.Context(), u.ID, id, false)
		}
		if err != nil {
			return err
		}
		jsonResponse(w, 200, p)
		return nil
	}
}
func (a *API) notifications(w http.ResponseWriter, r *http.Request, u User) error {
	items, err := a.Store.Notifications(r.Context(), u.ID)
	if err != nil {
		return err
	}
	jsonResponse(w, 200, items)
	return nil
}
func (a *API) readNotification(w http.ResponseWriter, r *http.Request, u User) error {
	id, err := planID(r)
	if err != nil {
		return err
	}
	return a.Store.ReadNotification(r.Context(), u.ID, id, w)
}
func (a *API) events(w http.ResponseWriter, r *http.Request, u User) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return &AppError{"unsupported", "Live updates are unavailable.", 500}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	_, _ = io.WriteString(w, "event: refresh\ndata: {}\n\n")
	flusher.Flush()
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	var channel <-chan *redis.Message
	if a.Store.Redis != nil {
		sub := a.Store.Redis.Subscribe(r.Context(), "onemore:refresh")
		defer sub.Close()
		channel = sub.Channel()
	}
	for {
		select {
		case <-r.Context().Done():
			return nil
		case <-tick.C:
			_, _ = io.WriteString(w, "event: refresh\ndata: {}\n\n")
			flusher.Flush()
		case <-channel:
			_, _ = io.WriteString(w, "event: refresh\ndata: {}\n\n")
			flusher.Flush()
		}
	}
}
