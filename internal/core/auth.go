package core

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2"
)

type Auth struct {
	Store  *Store
	Dev    bool
	Secure bool
	OIDC   *oidc.IDTokenVerifier
	OAuth  *oauth2.Config
}

func NewAuth(ctx context.Context, s *Store) (*Auth, error) {
	a := &Auth{Store: s, Dev: os.Getenv("DEV_AUTH") == "true", Secure: os.Getenv("COOKIE_SECURE") == "true"}
	issuer := os.Getenv("OIDC_ISSUER")
	if issuer == "" {
		return a, nil
	}
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, err
	}
	clientID := os.Getenv("OIDC_CLIENT_ID")
	a.OIDC = provider.Verifier(&oidc.Config{ClientID: clientID})
	a.OAuth = &oauth2.Config{ClientID: clientID, ClientSecret: os.Getenv("OIDC_CLIENT_SECRET"), Endpoint: provider.Endpoint(), RedirectURL: os.Getenv("OIDC_REDIRECT_URL"), Scopes: []string{oidc.ScopeOpenID, "profile", "email"}}
	return a, nil
}
func token() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func hashToken(t string) string { sum := sha256.Sum256([]byte(t)); return hex.EncodeToString(sum[:]) }
func (a *Auth) session(ctx context.Context, w http.ResponseWriter, userID string) error {
	t := token()
	expires := time.Now().Add(7 * 24 * time.Hour)
	_, err := a.Store.DB.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,$3)`, hashToken(t), userID, expires)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: "onemore_session", Value: t, Path: "/", HttpOnly: true, Secure: a.Secure, SameSite: http.SameSiteLaxMode, Expires: expires})
	return nil
}
func (a *Auth) User(r *http.Request) (User, error) {
	var u User
	c, err := r.Cookie("onemore_session")
	if err != nil {
		return u, &AppError{"unauthorized", "Sign in to continue.", 401}
	}
	err = a.Store.DB.QueryRow(r.Context(), `SELECT u.id,u.name,u.email FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.expires_at>clock_timestamp()`, hashToken(c.Value)).Scan(&u.ID, &u.Name, &u.Email)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, &AppError{"unauthorized", "Sign in to continue.", 401}
	}
	return u, err
}
func (a *Auth) Login(w http.ResponseWriter, r *http.Request) {
	if a.OAuth == nil {
		writeErr(w, &AppError{"not_configured", "Sign-in provider is not configured.", 503})
		return
	}
	state, nonce := token(), token()
	flow := state + "." + nonce
	http.SetCookie(w, &http.Cookie{Name: "onemore_flow", Value: flow, Path: "/auth", HttpOnly: true, Secure: a.Secure, SameSite: http.SameSiteLaxMode, MaxAge: 600})
	http.Redirect(w, r, a.OAuth.AuthCodeURL(state, oidc.Nonce(nonce)), http.StatusFound)
}
func (a *Auth) Callback(w http.ResponseWriter, r *http.Request) {
	if a.OAuth == nil {
		writeErr(w, &AppError{"not_configured", "Sign-in provider is not configured.", 503})
		return
	}
	c, err := r.Cookie("onemore_flow")
	if err != nil {
		writeErr(w, Bad("invalid_state", "Sign-in session expired. Try again."))
		return
	}
	parts := strings.SplitN(c.Value, ".", 2)
	if len(parts) != 2 || parts[0] != r.URL.Query().Get("state") {
		writeErr(w, Bad("invalid_state", "Sign-in session expired. Try again."))
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "onemore_flow", Value: "", Path: "/auth", MaxAge: -1, HttpOnly: true, Secure: a.Secure})
	oauthToken, err := a.OAuth.Exchange(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		writeErr(w, Bad("sign_in_failed", "Sign-in failed. Try again."))
		return
	}
	raw, ok := oauthToken.Extra("id_token").(string)
	if !ok {
		writeErr(w, Bad("sign_in_failed", "Sign-in response was incomplete."))
		return
	}
	idToken, err := a.OIDC.Verify(r.Context(), raw)
	if err != nil {
		writeErr(w, Bad("sign_in_failed", "Sign-in response was invalid."))
		return
	}
	var claims struct {
		Subject string `json:"sub"`
		Name    string `json:"name"`
		Email   string `json:"email"`
		Nonce   string `json:"nonce"`
	}
	if err = idToken.Claims(&claims); err != nil || claims.Nonce != parts[1] || claims.Subject == "" {
		writeErr(w, Bad("sign_in_failed", "Sign-in response was invalid."))
		return
	}
	if claims.Name == "" {
		claims.Name = "Member"
	}
	var userID string
	subject := os.Getenv("OIDC_ISSUER") + "|" + claims.Subject
	err = a.Store.DB.QueryRow(r.Context(), `INSERT INTO users(id,subject,name,email) VALUES($1,$2,$3,$4) ON CONFLICT(subject) DO UPDATE SET name=EXCLUDED.name,email=EXCLUDED.email RETURNING id`, NewID(), subject, claims.Name, claims.Email).Scan(&userID)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err = a.session(r.Context(), w, userID); err != nil {
		writeErr(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusFound)
}
func (a *Auth) DevLogin(w http.ResponseWriter, r *http.Request) {
	if !a.Dev {
		writeErr(w, NotFound())
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	names := map[string]string{"Alex": "11111111-1111-4111-8111-111111111111", "Sam": "22222222-2222-4222-8222-222222222222", "Jordan": "33333333-3333-4333-8333-333333333333"}
	id, ok := names[in.Name]
	if !ok {
		writeErr(w, Bad("invalid_user", "Choose a demo profile."))
		return
	}
	_, err := a.Store.DB.Exec(r.Context(), `INSERT INTO users(id,subject,name) VALUES($1,$2,$3) ON CONFLICT(subject) DO NOTHING`, id, "demo:"+in.Name, in.Name)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err = a.session(r.Context(), w, id); err != nil {
		writeErr(w, err)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]string{"name": in.Name})
}
func (a *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("onemore_session"); err == nil {
		_, _ = a.Store.DB.Exec(r.Context(), `DELETE FROM sessions WHERE token_hash=$1`, hashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: "onemore_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.Secure, SameSite: http.SameSiteLaxMode})
	w.WriteHeader(http.StatusNoContent)
}
func (a *Auth) Config(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, 200, map[string]any{"devAuth": a.Dev, "oidc": a.OAuth != nil})
}
