package core

import "time"

type User struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type Plan struct {
	ID             string     `json:"id"`
	HostID         string     `json:"hostId"`
	HostName       string     `json:"hostName"`
	Title          string     `json:"title"`
	Description    string     `json:"description"`
	Category       string     `json:"category"`
	Tags           []string   `json:"tags"`
	Area           string     `json:"area"`
	Location       string     `json:"location"`
	StartsAt       time.Time  `json:"startsAt"`
	JoinDeadline   time.Time  `json:"joinDeadline"`
	Spaces         int        `json:"spaces"`
	Confirmed      int        `json:"confirmed"`
	Reserved       int        `json:"reserved"`
	Waiting        int        `json:"waiting"`
	Available      int        `json:"available"`
	Status         string     `json:"status"`
	MyStatus       string     `json:"myStatus"`
	OfferExpiresAt *time.Time `json:"offerExpiresAt,omitempty"`
}

type CreatePlan struct {
	Title        string    `json:"title"`
	Description  string    `json:"description"`
	Category     string    `json:"category"`
	Tags         []string  `json:"tags"`
	Area         string    `json:"area"`
	Location     string    `json:"location"`
	StartsAt     time.Time `json:"startsAt"`
	JoinDeadline time.Time `json:"joinDeadline"`
	Spaces       int       `json:"spaces"`
}

type Notification struct {
	ID           string    `json:"id"`
	Message      string    `json:"message"`
	InvitationID string    `json:"invitationId"`
	Read         bool      `json:"read"`
	CreatedAt    time.Time `json:"createdAt"`
}

type AppError struct {
	Code, Message string
	Status        int
}

func (e *AppError) Error() string         { return e.Message }
func Bad(code, message string) error      { return &AppError{code, message, 400} }
func Conflict(code, message string) error { return &AppError{code, message, 409} }
func Forbidden(message string) error      { return &AppError{"forbidden", message, 403} }
func NotFound() error                     { return &AppError{"not_found", "Plan not found.", 404} }
