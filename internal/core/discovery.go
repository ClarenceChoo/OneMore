package core

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/jackc/pgx/v5"
)

type DiscoveryPreferences struct {
	Categories   []string `json:"categories"`
	Area         string   `json:"area"`
	Availability string   `json:"availability"`
	Timezone     string   `json:"timezone"`
}

var discoveryCategories = map[string]bool{"Badminton": true, "Board games": true, "Study": true, "Food": true, "Other": true}

func (s *Store) GetPreferences(ctx context.Context, userID string) (DiscoveryPreferences, error) {
	p := DiscoveryPreferences{Categories: []string{}, Availability: "any", Timezone: "UTC"}
	err := s.DB.QueryRow(ctx, `SELECT categories,area,availability,timezone FROM discovery_preferences WHERE user_id=$1`, userID).Scan(&p.Categories, &p.Area, &p.Availability, &p.Timezone)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, nil
	}
	if p.Categories == nil {
		p.Categories = []string{}
	}
	return p, err
}

func (s *Store) SavePreferences(ctx context.Context, userID string, p DiscoveryPreferences) (DiscoveryPreferences, error) {
	p.Area = strings.TrimSpace(p.Area)
	if len([]rune(p.Area)) > 60 || len(p.Categories) > len(discoveryCategories) {
		return p, Bad("invalid_preferences", "Choose up to five interests and an area under 60 characters.")
	}
	seen := map[string]bool{}
	for _, category := range p.Categories {
		if !discoveryCategories[category] || seen[category] {
			return p, Bad("invalid_preferences", "Choose interests from the list without duplicates.")
		}
		seen[category] = true
	}
	if p.Categories == nil {
		p.Categories = []string{}
	}
	if p.Availability != "any" && p.Availability != "weekday_evenings" && p.Availability != "weekends" {
		return p, Bad("invalid_preferences", "Choose a valid availability option.")
	}
	if len(p.Timezone) > 80 {
		return p, Bad("invalid_preferences", "Choose a valid time zone.")
	}
	if _, err := time.LoadLocation(p.Timezone); err != nil {
		return p, Bad("invalid_preferences", "Choose a valid time zone.")
	}
	_, err := s.DB.Exec(ctx, `INSERT INTO discovery_preferences(user_id,categories,area,availability,timezone) VALUES($1,$2,$3,$4,$5)
ON CONFLICT(user_id) DO UPDATE SET categories=EXCLUDED.categories,area=EXCLUDED.area,availability=EXCLUDED.availability,timezone=EXCLUDED.timezone,updated_at=clock_timestamp()`, userID, p.Categories, p.Area, p.Availability, p.Timezone)
	return p, err
}

type DiscoveryEventInput struct {
	PlanID string `json:"planId"`
	Kind   string `json:"kind"`
	Source string `json:"source"`
}

func (s *Store) RecordDiscoveryEvents(ctx context.Context, userID string, events []DiscoveryEventInput) error {
	if len(events) == 0 || len(events) > 25 {
		return Bad("invalid_events", "Send between one and 25 discovery events.")
	}
	for _, event := range events {
		if !uuidPattern.MatchString(event.PlanID) || (event.Kind != "impression" && event.Kind != "click") || (event.Source != "soonest" && event.Source != "for_you") {
			return Bad("invalid_events", "Check the discovery events and try again.")
		}
	}
	batch := &pgx.Batch{}
	for _, event := range events {
		batch.Queue(`INSERT INTO discovery_events(user_id,invitation_id,kind,source) SELECT $1,i.id,$3,$4 FROM invitations i WHERE i.id=$2::uuid
ON CONFLICT(user_id,invitation_id,kind,source) DO UPDATE SET count=discovery_events.count+1,last_at=clock_timestamp()`, userID, event.PlanID, event.Kind, event.Source)
	}
	results := s.DB.SendBatch(ctx, batch)
	for range events {
		if _, err := results.Exec(); err != nil {
			_ = results.Close()
			return err
		}
	}
	return results.Close()
}

func recordJoinEvent(ctx context.Context, tx pgx.Tx, userID, planID string) error {
	_, err := tx.Exec(ctx, `INSERT INTO discovery_events(user_id,invitation_id,kind,source)
VALUES($1,$2,'join',COALESCE((SELECT source FROM discovery_events WHERE user_id=$1 AND invitation_id=$2 AND kind='click' AND last_at>clock_timestamp()-interval '24 hours' ORDER BY last_at DESC LIMIT 1),'direct'))
ON CONFLICT(user_id,invitation_id,kind,source) DO UPDATE SET count=discovery_events.count+1,last_at=clock_timestamp()`, userID, planID)
	return err
}

func matchesAvailability(start time.Time, preference string, location *time.Location) bool {
	local := start.In(location)
	switch preference {
	case "weekday_evenings":
		return local.Weekday() >= time.Monday && local.Weekday() <= time.Friday && local.Hour() >= 18 && local.Hour() < 23
	case "weekends":
		return local.Weekday() == time.Saturday || local.Weekday() == time.Sunday
	default:
		return false
	}
}

func (s *Store) Recommendations(ctx context.Context, userID string) (FeedPage, error) {
	out := FeedPage{Plans: []Plan{}}
	prefs, err := s.GetPreferences(ctx, userID)
	if err != nil {
		return out, err
	}
	affinity := map[string]int{}
	rows, err := s.DB.Query(ctx, `SELECT i.category,sum((CASE WHEN e.kind='join' THEN 2 ELSE 1 END)*least(e.count,3))::int
FROM discovery_events e JOIN invitations i ON i.id=e.invitation_id WHERE e.user_id=$1 AND e.kind IN ('click','join') AND e.last_at>clock_timestamp()-interval '90 days' GROUP BY i.category`, userID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var category string
		var score int
		if err = rows.Scan(&category, &score); err != nil {
			rows.Close()
			return out, err
		}
		affinity[category] = score
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	preferred := append([]string{}, prefs.Categories...)
	for category := range affinity {
		if !containsString(preferred, category) {
			preferred = append(preferred, category)
		}
	}
	rows, err = s.DB.Query(ctx, `SELECT i.id FROM invitations i WHERE i.status='active' AND i.starts_at>clock_timestamp() AND i.starts_at<=clock_timestamp()+interval '30 days' AND i.join_deadline>clock_timestamp() AND i.host_id<>$1::uuid
ORDER BY (i.category=ANY($2::text[])) DESC,(lower(i.area)=lower($3)) DESC,i.starts_at,i.id LIMIT 200`, userID, preferred, prefs.Area)
	if err != nil {
		return out, err
	}
	ids := []string{}
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
	plans, err := s.plansByIDs(ctx, userID, ids)
	if err != nil {
		return out, err
	}
	location, err := time.LoadLocation(prefs.Timezone)
	if err != nil {
		location = time.UTC
	}
	type scored struct {
		plan  Plan
		score int
	}
	ranked := make([]scored, 0, len(plans))
	for _, plan := range plans {
		score := 0
		if containsString(prefs.Categories, plan.Category) {
			score += 5
			plan.RecommendationReason = "Matches your interest in " + plan.Category
		}
		if prefs.Area != "" && strings.EqualFold(prefs.Area, plan.Area) {
			score += 3
			if plan.RecommendationReason == "" {
				plan.RecommendationReason = "In your preferred area"
			}
		}
		if matchesAvailability(plan.StartsAt, prefs.Availability, location) {
			score += 3
			if plan.RecommendationReason == "" {
				plan.RecommendationReason = "Fits your availability"
			}
		}
		if affinity[plan.Category] > 0 {
			bonus := affinity[plan.Category]
			if bonus > 3 {
				bonus = 3
			}
			score += bonus
			if plan.RecommendationReason == "" {
				plan.RecommendationReason = "Similar to plans you explored"
			}
		}
		if plan.Available > 0 {
			score++
		}
		if plan.RecommendationReason == "" {
			plan.RecommendationReason = "Coming up soon"
		}
		ranked = append(ranked, scored{plan, score})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		if !ranked[i].plan.StartsAt.Equal(ranked[j].plan.StartsAt) {
			return ranked[i].plan.StartsAt.Before(ranked[j].plan.StartsAt)
		}
		return ranked[i].plan.ID < ranked[j].plan.ID
	})
	for i, item := range ranked {
		if i == 24 {
			break
		}
		out.Plans = append(out.Plans, item.plan)
	}
	return out, nil
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
