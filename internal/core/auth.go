package core

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/argon2"
	"golang.org/x/oauth2"
)

type Auth struct {
	Store  *Store
	Local  bool
	Secure bool
	OIDC   *oidc.IDTokenVerifier
	OAuth  *oauth2.Config
}

func NewAuth(ctx context.Context, s *Store) (*Auth, error) {
	a := &Auth{Store: s, Local: os.Getenv("LOCAL_AUTH") == "true", Secure: os.Getenv("COOKIE_SECURE") == "true"}
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

var handlePattern = regexp.MustCompile(`^[a-z0-9_]{3,24}$`)

func passwordHash(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, 2, 19*1024, 1, 32)
	return fmt.Sprintf("$argon2id$v=19$m=19456,t=2,p=1$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash)), nil
}

func matchesPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" || parts[3] != "m=19456,t=2,p=1" {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) != 16 {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) != 32 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, 2, 19*1024, 1, 32)
	return subtle.ConstantTimeCompare(got, want) == 1
}

func (a *Auth) Register(w http.ResponseWriter, r *http.Request) {
	if !a.Local {
		writeErr(w, NotFound())
		return
	}
	var in struct {
		Name     string `json:"name"`
		Handle   string `json:"handle"`
		Password string `json:"password"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Handle = strings.ToLower(strings.TrimSpace(in.Handle))
	if len([]rune(in.Name)) < 2 || len([]rune(in.Name)) > 50 || !handlePattern.MatchString(in.Handle) || len(in.Password) < 10 || len(in.Password) > 128 {
		writeErr(w, Bad("invalid_profile", "Use a name of 2–50 characters, a handle of 3–24 letters, numbers, or underscores, and a password of 10–128 characters."))
		return
	}
	hash, err := passwordHash(in.Password)
	if err != nil {
		writeErr(w, err)
		return
	}
	tx, err := a.Store.DB.Begin(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var id string
	err = tx.QueryRow(r.Context(), `INSERT INTO users(id,subject,name) VALUES($1,$2,$3) ON CONFLICT(subject) DO NOTHING RETURNING id`, NewID(), "local:"+in.Handle, in.Name).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, &AppError{"handle_taken", "That handle is already in use.", http.StatusConflict})
		return
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO local_credentials(user_id,handle,password_hash) VALUES($1,$2,$3)`, id, in.Handle, hash)
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	if err = a.session(r.Context(), w, id); err != nil {
		writeErr(w, err)
		return
	}
	jsonResponse(w, http.StatusCreated, map[string]string{"id": id, "name": in.Name})
}

func (a *Auth) LocalLogin(w http.ResponseWriter, r *http.Request) {
	if !a.Local {
		writeErr(w, NotFound())
		return
	}
	var in struct {
		Handle   string `json:"handle"`
		Password string `json:"password"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	in.Handle = strings.ToLower(strings.TrimSpace(in.Handle))
	if !handlePattern.MatchString(in.Handle) || len(in.Password) == 0 || len(in.Password) > 128 {
		writeErr(w, Bad("invalid_credentials", "Check your handle and password."))
		return
	}
	var id, hash string
	err := a.Store.DB.QueryRow(r.Context(), `SELECT user_id,password_hash FROM local_credentials WHERE handle=$1`, in.Handle).Scan(&id, &hash)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !matchesPassword(in.Password, hash)) {
		writeErr(w, &AppError{"invalid_credentials", "Check your handle and password.", http.StatusUnauthorized})
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	if err = a.session(r.Context(), w, id); err != nil {
		writeErr(w, err)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]string{"id": id})
}
func (a *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("onemore_session"); err == nil {
		_, _ = a.Store.DB.Exec(r.Context(), `DELETE FROM sessions WHERE token_hash=$1`, hashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: "onemore_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.Secure, SameSite: http.SameSiteLaxMode})
	w.WriteHeader(http.StatusNoContent)
}
func (a *Auth) Config(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, 200, map[string]any{"localAuth": a.Local, "oidc": a.OAuth != nil})
}
