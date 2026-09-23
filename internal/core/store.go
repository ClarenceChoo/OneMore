package core

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type Store struct {
	DB    *pgxpool.Pool
	Redis *redis.Client
}

func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

const planSelect = `SELECT i.id,i.host_id,u.name,i.title,i.description,i.category,i.tags,i.area,i.location,i.starts_at,i.join_deadline,i.spaces,i.status,
 (SELECT count(*) FROM memberships m WHERE m.invitation_id=i.id AND m.status='confirmed')::int,
 (SELECT count(*) FROM offers o JOIN memberships m ON m.id=o.membership_id WHERE m.invitation_id=i.id AND o.status='active' AND o.expires_at>now())::int,
 (SELECT count(*) FROM memberships m WHERE m.invitation_id=i.id AND m.status='waitlisted')::int,
 COALESCE((SELECT m.status FROM memberships m WHERE m.invitation_id=i.id AND m.user_id=$1::uuid), CASE WHEN i.host_id=$1::uuid THEN 'host' ELSE '' END),
 (SELECT o.expires_at FROM offers o JOIN memberships m ON m.id=o.membership_id WHERE m.invitation_id=i.id AND m.user_id=$1::uuid AND o.status='active' LIMIT 1)
 FROM invitations i JOIN users u ON u.id=i.host_id `

func scanPlan(row pgx.Row) (Plan, error) {
	var p Plan
	err := row.Scan(&p.ID, &p.HostID, &p.HostName, &p.Title, &p.Description, &p.Category, &p.Tags, &p.Area, &p.Location, &p.StartsAt, &p.JoinDeadline, &p.Spaces, &p.Status, &p.Confirmed, &p.Reserved, &p.Waiting, &p.MyStatus, &p.OfferExpiresAt)
	p.Available = p.Spaces - p.Confirmed - p.Reserved
	if p.Tags == nil {
		p.Tags = []string{}
	}
	return p, err
}

func (s *Store) GetPlan(ctx context.Context, userID, id string) (Plan, error) {
	p, err := scanPlan(s.DB.QueryRow(ctx, planSelect+`WHERE i.id=$2::uuid`, userID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return p, NotFound()
	}
	return p, err
}

type FeedFilter struct {
	Category, Area string
	Before         time.Time
	OpenOnly       bool
	Cursor         string
}
type FeedPage struct {
	Plans      []Plan `json:"plans"`
	NextCursor string `json:"nextCursor,omitempty"`
}
type cursor struct {
	Time time.Time `json:"time"`
	ID   string    `json:"id"`
}

func (s *Store) ListPlans(ctx context.Context, userID string, f FeedFilter) (FeedPage, error) {
	out := FeedPage{Plans: []Plan{}}
	c := cursor{}
	if f.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(f.Cursor)
		if err != nil || json.Unmarshal(raw, &c) != nil || !uuidPattern.MatchString(c.ID) {
			return out, Bad("invalid_cursor", "The feed cursor is invalid.")
		}
	}
	cacheData, _ := json.Marshal(f)
	cacheHash := sha256.Sum256(cacheData)
	cacheKey := "onemore:feed:" + hex.EncodeToString(cacheHash[:])
	if f.Before.IsZero() {
		f.Before = time.Now().UTC().Add(30 * 24 * time.Hour)
	}
	var ids []string
	if s.Redis != nil {
		cacheCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		if raw, err := s.Redis.Get(cacheCtx, cacheKey).Bytes(); err == nil {
			_ = json.Unmarshal(raw, &ids)
		}
		cancel()
	}
	if ids == nil {
		ids = []string{}
		rows, err := s.DB.Query(ctx, `SELECT i.id FROM invitations i WHERE i.status='active' AND i.starts_at>now() AND i.starts_at<=$1 AND ($2='' OR i.category=$2) AND ($3='' OR i.area=$3)
 AND (NOT $4 OR i.spaces>(SELECT count(*) FROM memberships m WHERE m.invitation_id=i.id AND m.status='confirmed')+(SELECT count(*) FROM offers o JOIN memberships m ON m.id=o.membership_id WHERE m.invitation_id=i.id AND o.status='active' AND o.expires_at>now()))
 AND ($5::timestamptz IS NULL OR (i.starts_at,i.id)>($5::timestamptz,$6::uuid)) ORDER BY i.starts_at,i.id LIMIT 25`, f.Before, f.Category, f.Area, f.OpenOnly, nullableTime(c.Time), nullableString(c.ID))
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return out, err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
		if s.Redis != nil {
			raw, _ := json.Marshal(ids)
			cacheCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
			_ = s.Redis.Set(cacheCtx, cacheKey, raw, 5*time.Second).Err()
			cancel()
		}
	}
	for _, id := range ids {
		p, err := s.GetPlan(ctx, userID, id)
		if err != nil {
			return out, err
		}
		out.Plans = append(out.Plans, p)
	}
	if len(out.Plans) > 24 {
		out.Plans = out.Plans[:24]
		last := out.Plans[23]
		raw, _ := json.Marshal(cursor{last.StartsAt, last.ID})
		out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return out, nil
}
func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (s *Store) MyPlans(ctx context.Context, userID string) ([]Plan, error) {
	rows, err := s.DB.Query(ctx, planSelect+`WHERE i.host_id=$1::uuid OR EXISTS(SELECT 1 FROM memberships m WHERE m.invitation_id=i.id AND m.user_id=$1::uuid AND m.status<>'left') ORDER BY i.starts_at DESC LIMIT 100`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Plan{}
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func validateCreate(in *CreatePlan) error {
	if in.Tags == nil {
		in.Tags = []string{}
	}
	in.Title = strings.TrimSpace(in.Title)
	in.Description = strings.TrimSpace(in.Description)
	in.Category = strings.TrimSpace(in.Category)
	in.Area = strings.TrimSpace(in.Area)
	in.Location = strings.TrimSpace(in.Location)
	if len([]rune(in.Title)) < 3 || len([]rune(in.Title)) > 80 {
		return Bad("invalid_title", "Use a title between 3 and 80 characters.")
	}
	if len([]rune(in.Description)) > 500 {
		return Bad("invalid_description", "Keep the description under 500 characters.")
	}
	if in.Category == "" || len(in.Category) > 40 || in.Area == "" || len(in.Area) > 60 || in.Location == "" || len(in.Location) > 120 {
		return Bad("invalid_place", "Add a category, area, and meeting place.")
	}
	if in.Spaces < 1 || in.Spaces > 20 {
		return Bad("invalid_spaces", "Choose 1 to 20 spaces.")
	}
	if in.StartsAt.Before(time.Now().UTC().Add(5*time.Minute)) || in.StartsAt.After(time.Now().UTC().Add(30*24*time.Hour)) {
		return Bad("invalid_start", "Choose a start time between five minutes and 30 days from now.")
	}
	if in.JoinDeadline.After(in.StartsAt) || in.JoinDeadline.Before(time.Now().UTC().Add(time.Minute)) {
		return Bad("invalid_deadline", "The join deadline must be before the start and at least one minute from now.")
	}
	if len(in.Tags) > 5 {
		return Bad("invalid_tags", "Use no more than five tags.")
	}
	for i, t := range in.Tags {
		in.Tags[i] = strings.TrimSpace(t)
		if in.Tags[i] == "" || len(in.Tags[i]) > 24 {
			return Bad("invalid_tags", "Each tag must be 1 to 24 characters.")
		}
	}
	return nil
}

func (s *Store) Create(ctx context.Context, userID, key string, in CreatePlan) (Plan, error) {
	if err := validateCreate(&in); err != nil {
		return Plan{}, err
	}
	if len(key) > 120 {
		return Plan{}, Bad("invalid_key", "The request key is too long.")
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Plan{}, err
	}
	defer tx.Rollback(ctx)
	if key != "" {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, userID+":"+key); err != nil {
			return Plan{}, err
		}
		var existing string
		err = tx.QueryRow(ctx, `SELECT invitation_id FROM idempotency_records WHERE user_id=$1 AND key=$2`, userID, key).Scan(&existing)
		if err == nil {
			if err = tx.Commit(ctx); err != nil {
				return Plan{}, err
			}
			return s.GetPlan(ctx, userID, existing)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return Plan{}, err
		}
	}
	id := NewID()
	_, err = tx.Exec(ctx, `INSERT INTO invitations(id,host_id,title,description,category,tags,area,location,starts_at,join_deadline,spaces) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, id, userID, in.Title, in.Description, in.Category, in.Tags, in.Area, in.Location, in.StartsAt, in.JoinDeadline, in.Spaces)
	if err != nil {
		return Plan{}, err
	}
	if key != "" {
		_, err = tx.Exec(ctx, `INSERT INTO idempotency_records(user_id,key,invitation_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, userID, key, id)
		if err != nil {
			return Plan{}, err
		}
		var actual string
		if err = tx.QueryRow(ctx, `SELECT invitation_id FROM idempotency_records WHERE user_id=$1 AND key=$2`, userID, key).Scan(&actual); err != nil {
			return Plan{}, err
		}
		if actual != id {
			return Plan{}, Conflict("duplicate_key", "This request key was already used. Retry after refreshing.")
		}
	}
	if err = addEvent(ctx, tx, "plan_created", id, "", ""); err != nil {
		return Plan{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Plan{}, err
	}
	return s.GetPlan(ctx, userID, id)
}

func addEvent(ctx context.Context, tx pgx.Tx, kind, planID, userID, message string) error {
	_, err := tx.Exec(ctx, `INSERT INTO outbox_events(id,kind,invitation_id,user_id,message) VALUES($1,$2,$3,$4,$5)`, NewID(), kind, planID, nullableString(userID), message)
	return err
}
