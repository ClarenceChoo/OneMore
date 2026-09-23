package core

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to an isolated database ending in _test")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	var name string
	if err = db.QueryRow(ctx, `SELECT current_database()`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(name, "_test") {
		t.Fatal("integration tests require a database name ending in _test")
	}
	schema, err := os.ReadFile("../../db/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(schema)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `TRUNCATE idempotency_records,outbox_events,notifications,offers,memberships,invitations,sessions,users RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	return &Store{DB: db}
}
func testUser(t *testing.T, s *Store) string {
	t.Helper()
	id := NewID()
	if _, err := s.DB.Exec(context.Background(), `INSERT INTO users(id,subject,name) VALUES($1,$2,'Tester')`, id, id); err != nil {
		t.Fatal(err)
	}
	return id
}
func testPlan(t *testing.T, s *Store, host string, spaces int) Plan {
	t.Helper()
	p, err := s.Create(context.Background(), host, "", CreatePlan{Title: "Evening badminton", Category: "Badminton", Area: "North campus", Location: "Sports hall", StartsAt: time.Now().Add(3 * time.Hour), JoinDeadline: time.Now().Add(2 * time.Hour), Spaces: spaces})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestConcurrentClaimsAndRepeatedJoin(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	host := testUser(t, s)
	plan := testPlan(t, s, host, 2)
	users := make([]string, 100)
	for i := range users {
		users[i] = testUser(t, s)
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(users))
	for _, u := range users {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.Join(ctx, u, plan.ID); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	p, err := s.GetPlan(ctx, host, plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Confirmed != 2 || p.Waiting != 98 || p.Available != 0 {
		t.Fatalf("allocation mismatch: %+v", p)
	}
	before, err := s.GetPlan(ctx, users[0], plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.Join(ctx, users[0], plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.MyStatus != after.MyStatus || after.Confirmed != 2 || after.Waiting != 98 {
		t.Fatalf("repeated join changed membership: before=%+v after=%+v", before, after)
	}
}

func TestWaitlistOfferExpiryAndFairness(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	host := testUser(t, s)
	a, b, c := testUser(t, s), testUser(t, s), testUser(t, s)
	plan := testPlan(t, s, host, 1)
	for _, u := range []string{a, b, c} {
		if _, err := s.Join(ctx, u, plan.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Leave(ctx, b, plan.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Join(ctx, b, plan.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Leave(ctx, a, plan.ID); err != nil {
		t.Fatal(err)
	}
	cp, err := s.GetPlan(ctx, c, plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cp.MyStatus != "offered" || cp.Available != 0 {
		t.Fatalf("oldest waiter should hold offer: %+v", cp)
	}
	if _, err = s.DB.Exec(ctx, `UPDATE offers SET expires_at=clock_timestamp()-interval '1 second' WHERE status='active'`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.OfferAction(ctx, c, plan.ID, true); err == nil {
		t.Fatal("expired offer was accepted")
	}
	if err = s.ExpireOffers(ctx); err != nil {
		t.Fatal(err)
	}
	bp, err := s.GetPlan(ctx, b, plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bp.MyStatus != "offered" {
		t.Fatalf("next waiter did not receive offer: %+v", bp)
	}
	if _, err = s.OfferAction(ctx, b, plan.ID, true); err != nil {
		t.Fatal(err)
	}
	p, err := s.GetPlan(ctx, host, plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Confirmed != 1 || p.Reserved != 0 {
		t.Fatalf("offer acceptance broke capacity: %+v", p)
	}
}

func TestHostCancellationInvalidatesOffers(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	host := testUser(t, s)
	a, b := testUser(t, s), testUser(t, s)
	plan := testPlan(t, s, host, 1)
	_, _ = s.Join(ctx, a, plan.ID)
	_, _ = s.Join(ctx, b, plan.ID)
	_, _ = s.Leave(ctx, a, plan.ID)
	if _, err := s.Cancel(ctx, b, plan.ID); err == nil {
		t.Fatal("non-host cancelled plan")
	}
	if _, err := s.Cancel(ctx, host, plan.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OfferAction(ctx, b, plan.ID, true); err == nil {
		t.Fatal("cancelled offer was accepted")
	}
	p, err := s.GetPlan(ctx, host, plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != "cancelled" {
		t.Fatal("plan not cancelled")
	}
}
