package domain

import "time"

// LogEntry is one food logged by one user. Nutrition is snapshotted at save
// time, so a later catalogue correction never silently rewrites a past day.
type LogEntry struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"-"`
	FoodID    *int64    `json:"food_id"` // null for an inline entry
	FoodName  string    `json:"food_name"`
	Grams     float64   `json:"grams"`
	Nutrition Nutrition `json:"nutrition"`
	LoggedAt  time.Time `json:"logged_at"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// InputType records how a review item reached the queue.
type InputType string

const (
	InputText InputType = "text"
)

// ReviewReason says why a row reached the review queue.
type ReviewReason string

const (
	ReviewManualEntry    ReviewReason = "manual_entry"
	ReviewIngestReject   ReviewReason = "ingest_reject"
	ReviewIngestConflict ReviewReason = "ingest_conflict"
)

// ReviewItem is one entry in the queue that drives catalogue expansion.
type ReviewItem struct {
	ID        int64        `json:"id"`
	UserID    *int64       `json:"-"`
	RawInput  string       `json:"raw_input"`
	InputType InputType    `json:"input_type"`
	Reason    ReviewReason `json:"reason"`
	Payload   []byte       `json:"payload,omitempty"`
	Resolved  bool         `json:"resolved"`
	CreatedAt time.Time    `json:"created_at"`
}
