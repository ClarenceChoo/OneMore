package core

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"regexp"
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
	mux.HandleFunc("POST /auth/dev", a.Auth.DevLogin)
	mux.HandleFunc("POST /auth/logout", a.Auth.Logout)
	mux.HandleFunc("GET /api/me", a.handler(func(w http.ResponseWriter, r *http.Request, u User) error { jsonResponse(w, 200, u); return nil }))
	mux.HandleFunc("GET /api/plans", a.handler(a.list))
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
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
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
