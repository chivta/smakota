package domain

import "time"

// LogEntry is one food logged by one user. Nutrition is snapshotted at save
// time, so a later catalogue correction never silently rewrites a past day.
type LogEntry struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"-"`
	FoodID    int64     `json:"food_id"`
	FoodName  string    `json:"food_name"`
	Grams     float64   `json:"grams"`
	Nutrition Nutrition `json:"nutrition"`
	LoggedAt  time.Time `json:"logged_at"`
	Day       time.Time `json:"day"`
}

// Day is the flat chronological view of one date. No meal grouping.
type Day struct {
	Date          time.Time  `json:"date"`
	Entries       []LogEntry `json:"entries"`
	Consumed      Nutrition  `json:"consumed"`
	KcalTarget    int        `json:"kcal_target"`
	ProteinTarget int        `json:"protein_target"`
}

// Remaining is target minus consumed. It goes negative past the target, and the
// UI renders that identically to going under.
func (d Day) Remaining() int {
	return d.KcalTarget - int(d.Consumed.Kcal)
}

// InputType records how an entry reached the resolver.
type InputType string

const (
	InputText    InputType = "text"
	InputVoice   InputType = "voice"
	InputPhoto   InputType = "photo"
	InputBarcode InputType = "barcode"
	InputRecent  InputType = "recent"
)

// Attempt is one pass through the resolver, logged for the acceptance metrics.
type Attempt struct {
	ID                     int64     `json:"id"`
	UserID                 int64     `json:"-"`
	InputType              InputType `json:"input_type"`
	LatencyMS              int       `json:"latency_ms"`
	FirstCandidateAccepted bool      `json:"first_candidate_accepted"`
	RefinementUsed         bool      `json:"refinement_used"`
	Resolved               bool      `json:"resolved"`
	FoodID                 *int64    `json:"food_id,omitempty"`
	CreatedAt              time.Time `json:"created_at"`
}

// ReviewReason says why a row reached the review queue.
type ReviewReason string

const (
	ReviewUnresolved     ReviewReason = "unresolved"
	ReviewRefined        ReviewReason = "refined"
	ReviewManualEntry    ReviewReason = "manual_entry"
	ReviewBarcodeMiss    ReviewReason = "barcode_miss"
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

// Weight is one optional weigh-in. Shown only as a 7-day rolling average.
type Weight struct {
	ID         int64     `json:"id"`
	UserID     int64     `json:"-"`
	WeightKG   float64   `json:"weight_kg"`
	MeasuredOn time.Time `json:"measured_on"`
}
