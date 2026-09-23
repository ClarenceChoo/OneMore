package core

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type lockedPlan struct {
	HostID, Status     string
	Spaces             int
	StartsAt, Deadline time.Time
}

func lockPlan(ctx context.Context, tx pgx.Tx, id string) (lockedPlan, error) {
	var p lockedPlan
	err := tx.QueryRow(ctx, `SELECT host_id,status,spaces,starts_at,join_deadline FROM invitations WHERE id=$1::uuid FOR UPDATE`, id).Scan(&p.HostID, &p.Status, &p.Spaces, &p.StartsAt, &p.Deadline)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, NotFound()
	}
	return p, err
}
func dbNow(ctx context.Context, tx pgx.Tx) (time.Time, error) {
	var t time.Time
	err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&t)
	return t, err
}
func allocation(ctx context.Context, tx pgx.Tx, id string, now time.Time) (int, error) {
	var n int
	err := tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM memberships WHERE invitation_id=$1 AND status='confirmed')+(SELECT count(*) FROM offers o JOIN memberships m ON m.id=o.membership_id WHERE m.invitation_id=$1 AND o.status='active' AND o.expires_at>$2)`, id, now).Scan(&n)
	return n, err
}
func promote(ctx context.Context, tx pgx.Tx, id string, p lockedPlan, now time.Time) error {
	if p.Status != "active" || !now.Before(p.Deadline) || !now.Before(p.StartsAt) {
		return nil
	}
	for {
		used, err := allocation(ctx, tx, id, now)
		if err != nil {
			return err
		}
		if used >= p.Spaces {
			return nil
		}
		var memberID int64
		var userID string
		err = tx.QueryRow(ctx, `SELECT id,user_id FROM memberships WHERE invitation_id=$1 AND status='waitlisted' ORDER BY waitlist_order,id LIMIT 1`, id).Scan(&memberID, &userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		expires := now.Add(10 * time.Minute)
		if p.Deadline.Before(expires) {
			expires = p.Deadline
		}
		_, err = tx.Exec(ctx, `UPDATE memberships SET status='offered' WHERE id=$1`, memberID)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO offers(id,membership_id,expires_at,status) VALUES($1,$2,$3,'active')`, NewID(), memberID, expires)
		if err != nil {
			return err
		}
		if err = addEvent(ctx, tx, "offer_created", id, userID, "A space is open. Accept your offer before it expires."); err != nil {
			return err
		}
	}
}
func reconcile(ctx context.Context, tx pgx.Tx, id string, p lockedPlan, now time.Time) error {
	rows, err := tx.Query(ctx, `SELECT o.id,m.id,m.user_id FROM offers o JOIN memberships m ON m.id=o.membership_id WHERE m.invitation_id=$1 AND o.status='active' AND o.expires_at<=$2`, id, now)
	if err != nil {
		return err
	}
	type expired struct {
		offer  string
		member int64
		user   string
	}
	items := []expired{}
	for rows.Next() {
		var e expired
		if err = rows.Scan(&e.offer, &e.member, &e.user); err != nil {
			rows.Close()
			return err
		}
		items = append(items, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, e := range items {
		if _, err = tx.Exec(ctx, `UPDATE offers SET status='expired' WHERE id=$1`, e.offer); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE memberships SET status='left' WHERE id=$1 AND status='offered'`, e.member); err != nil {
			return err
		}
		if err = addEvent(ctx, tx, "offer_expired", id, e.user, "Your offer expired."); err != nil {
			return err
		}
	}
	return promote(ctx, tx, id, p, now)
}
func (s *Store) mutate(ctx context.Context, userID, id string, fn func(context.Context, pgx.Tx, lockedPlan, time.Time) error) (Plan, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Plan{}, err
	}
	defer tx.Rollback(ctx)
	p, err := lockPlan(ctx, tx, id)
	if err != nil {
		return Plan{}, err
	}
	now, err := dbNow(ctx, tx)
	if err != nil {
		return Plan{}, err
	}
	if err = reconcile(ctx, tx, id, p, now); err != nil {
		return Plan{}, err
	}
	if err = fn(ctx, tx, p, now); err != nil {
		return Plan{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Plan{}, err
	}
	return s.GetPlan(ctx, userID, id)
}

func (s *Store) Join(ctx context.Context, userID, id string) (Plan, error) {
	return s.mutate(ctx, userID, id, func(ctx context.Context, tx pgx.Tx, p lockedPlan, now time.Time) error {
		if p.HostID == userID {
			return Bad("host_cannot_join", "You already host this plan.")
		}
		if p.Status != "active" || !now.Before(p.Deadline) || !now.Before(p.StartsAt) {
			return Conflict("closed", "Joining has closed for this plan.")
		}
		var status string
		err := tx.QueryRow(ctx, `SELECT status FROM memberships WHERE invitation_id=$1 AND user_id=$2`, id, userID).Scan(&status)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if status == "confirmed" || status == "waitlisted" || status == "offered" {
			return nil
		}
		used, err := allocation(ctx, tx, id, now)
		if err != nil {
			return err
		}
		newStatus := "waitlisted"
		if used < p.Spaces {
			newStatus = "confirmed"
		}
		if status == "left" {
			_, err = tx.Exec(ctx, `UPDATE memberships SET status=$3,joined_at=$4,waitlist_order=nextval('waitlist_order_seq') WHERE invitation_id=$1 AND user_id=$2`, id, userID, newStatus, now)
		} else {
			_, err = tx.Exec(ctx, `INSERT INTO memberships(invitation_id,user_id,status,waitlist_order) VALUES($1,$2,$3,nextval('waitlist_order_seq'))`, id, userID, newStatus)
		}
		if err != nil {
			return err
		}
		if err = recordJoinEvent(ctx, tx, userID, id); err != nil {
			return err
		}
		return addEvent(ctx, tx, "member_joined", id, userID, "")
	})
}

func (s *Store) Leave(ctx context.Context, userID, id string) (Plan, error) {
	return s.mutate(ctx, userID, id, func(ctx context.Context, tx pgx.Tx, p lockedPlan, now time.Time) error {
		var memberID int64
		var status string
		err := tx.QueryRow(ctx, `SELECT id,status FROM memberships WHERE invitation_id=$1 AND user_id=$2`, id, userID).Scan(&memberID, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if status == "left" {
			return nil
		}
		if status == "offered" {
			if _, err = tx.Exec(ctx, `UPDATE offers SET status='declined' WHERE membership_id=$1 AND status='active'`, memberID); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE memberships SET status='left' WHERE id=$1`, memberID); err != nil {
			return err
		}
		if err = addEvent(ctx, tx, "member_left", id, userID, ""); err != nil {
			return err
		}
		return promote(ctx, tx, id, p, now)
	})
}

func (s *Store) OfferAction(ctx context.Context, userID, id string, accept bool) (Plan, error) {
	return s.mutate(ctx, userID, id, func(ctx context.Context, tx pgx.Tx, p lockedPlan, now time.Time) error {
		if p.Status != "active" {
			return Conflict("closed", "This plan was cancelled.")
		}
		var memberStatus string
		err := tx.QueryRow(ctx, `SELECT status FROM memberships WHERE invitation_id=$1 AND user_id=$2`, id, userID).Scan(&memberStatus)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if (accept && memberStatus == "confirmed") || (!accept && memberStatus == "left") {
			return nil
		}
		var offerID string
		var expires time.Time
		var memberID int64
		err = tx.QueryRow(ctx, `SELECT o.id,o.expires_at,m.id FROM offers o JOIN memberships m ON m.id=o.membership_id WHERE m.invitation_id=$1 AND m.user_id=$2 AND o.status='active'`, id, userID).Scan(&offerID, &expires, &memberID)
		if errors.Is(err, pgx.ErrNoRows) {
			var lastStatus string
			_ = tx.QueryRow(ctx, `SELECT o.status FROM offers o JOIN memberships m ON m.id=o.membership_id WHERE m.invitation_id=$1 AND m.user_id=$2 ORDER BY o.created_at DESC LIMIT 1`, id, userID).Scan(&lastStatus)
			if lastStatus == "expired" {
				return Conflict("offer_expired", "This offer has expired.")
			}
			return Conflict("no_offer", "There is no active offer to respond to.")
		}
		if err != nil {
			return err
		}
		if !now.Before(expires) || p.Status != "active" {
			return Conflict("offer_expired", "This offer has expired.")
		}
		next := "declined"
		memberStatus = "left"
		kind := "offer_declined"
		message := "You declined the offer."
		if accept {
			next = "accepted"
			memberStatus = "confirmed"
			kind = "offer_accepted"
			message = "Your spot is confirmed."
		}
		if _, err = tx.Exec(ctx, `UPDATE offers SET status=$2 WHERE id=$1`, offerID, next); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE memberships SET status=$2 WHERE id=$1`, memberID, memberStatus); err != nil {
			return err
		}
		if err = addEvent(ctx, tx, kind, id, userID, message); err != nil {
			return err
		}
		if !accept {
			return promote(ctx, tx, id, p, now)
		}
		return nil
	})
}

func (s *Store) Cancel(ctx context.Context, userID, id string) (Plan, error) {
	return s.mutate(ctx, userID, id, func(ctx context.Context, tx pgx.Tx, p lockedPlan, now time.Time) error {
		if p.HostID != userID {
			return Forbidden("Only the host can cancel this plan.")
		}
		if p.Status == "cancelled" {
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE invitations SET status='cancelled' WHERE id=$1`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE offers SET status='cancelled' WHERE status='active' AND membership_id IN (SELECT id FROM memberships WHERE invitation_id=$1)`, id); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT user_id FROM memberships WHERE invitation_id=$1 AND status<>'left'`, id)
		if err != nil {
			return err
		}
		users := []string{}
		for rows.Next() {
			var u string
			if err = rows.Scan(&u); err != nil {
				rows.Close()
				return err
			}
			users = append(users, u)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, u := range users {
			if err = addEvent(ctx, tx, "plan_cancelled", id, u, "A plan you joined was cancelled."); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE memberships SET status='left' WHERE invitation_id=$1 AND status='offered'`, id); err != nil {
			return err
		}
		return addEvent(ctx, tx, "plan_cancelled", id, "", "")
	})
}

func (s *Store) ExpireOffers(ctx context.Context) error {
	rows, err := s.DB.Query(ctx, `SELECT DISTINCT m.invitation_id FROM offers o JOIN memberships m ON m.id=o.membership_id WHERE o.status='active' AND o.expires_at<=clock_timestamp() LIMIT 100`)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		_, err = s.mutate(ctx, id, id, func(context.Context, pgx.Tx, lockedPlan, time.Time) error { return nil })
		if err != nil {
			return err
		}
	}
	return nil
}
