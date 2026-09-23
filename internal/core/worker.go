package core

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
)

func (s *Store) Notifications(ctx context.Context, userID string) ([]Notification, error) {
	rows, err := s.DB.Query(ctx, `SELECT id,message,invitation_id,read_at IS NOT NULL,created_at FROM notifications WHERE user_id=$1 ORDER BY created_at DESC LIMIT 50`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Notification{}
	for rows.Next() {
		var n Notification
		if err = rows.Scan(&n.ID, &n.Message, &n.InvitationID, &n.Read, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
func (s *Store) ReadNotification(ctx context.Context, userID, id string, w http.ResponseWriter) error {
	tag, err := s.DB.Exec(ctx, `UPDATE notifications SET read_at=COALESCE(read_at,clock_timestamp()) WHERE id=$1 AND user_id=$2`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return &AppError{"not_found", "Notification not found.", 404}
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Store) DeliverOutbox(ctx context.Context) error {
	for i := 0; i < 50; i++ {
		tx, err := s.DB.Begin(ctx)
		if err != nil {
			return err
		}
		var id, kind, planID, message string
		var userID *string
		err = tx.QueryRow(ctx, `SELECT id,kind,invitation_id,user_id,message FROM outbox_events WHERE delivered_at IS NULL ORDER BY created_at,id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&id, &kind, &planID, &userID, &message)
		if errors.Is(err, pgx.ErrNoRows) {
			tx.Rollback(ctx)
			return nil
		}
		if err != nil {
			tx.Rollback(ctx)
			return err
		}
		if userID != nil && message != "" {
			_, err = tx.Exec(ctx, `INSERT INTO notifications(id,user_id,event_id,message,invitation_id) VALUES($1,$2,$3,$4,$5) ON CONFLICT(event_id) DO NOTHING`, NewID(), *userID, id, message, planID)
			if err != nil {
				tx.Rollback(ctx)
				return err
			}
		}
		if s.Redis != nil {
			err = s.Redis.XAdd(ctx, &redis.XAddArgs{Stream: "onemore:events", MaxLen: 10000, Approx: true, Values: map[string]any{"id": id, "kind": kind, "planId": planID}}).Err()
			if err != nil {
				tx.Rollback(ctx)
				return err
			}
		}
		_, err = tx.Exec(ctx, `UPDATE outbox_events SET delivered_at=clock_timestamp() WHERE id=$1`, id)
		if err != nil {
			tx.Rollback(ctx)
			return err
		}
		if err = tx.Commit(ctx); err != nil {
			return err
		}
		if s.Redis != nil {
			_ = s.Redis.Publish(ctx, "onemore:refresh", id).Err()
		}
	}
	return nil
}
func (s *Store) RunWorker(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if err := s.ExpireOffers(ctx); err != nil && ctx.Err() == nil {
			log.Printf("offer expiry: %v", err)
		}
		if err := s.DeliverOutbox(ctx); err != nil && ctx.Err() == nil {
			log.Printf("outbox delivery: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
