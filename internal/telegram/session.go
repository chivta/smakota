package telegram

import (
	"sync"
	"time"

	"github.com/arvlas/calbot/internal/domain"
)

// step is where a user is in a multi-message exchange.
type step string

const (
	stepNone            step = ""
	stepPortion         step = "portion"
	stepClarify         step = "clarify"
	stepManualKcal      step = "manual_kcal"
	stepWeight          step = "weight"
	stepOnboardSex      step = "onboard_sex"
	stepOnboardAge      step = "onboard_age"
	stepOnboardHeight   step = "onboard_height"
	stepOnboardWeight   step = "onboard_weight"
	stepOnboardGoal     step = "onboard_goal"
	stepOnboardActivity step = "onboard_activity"
)

// maxRefinements is the PRD's budget: after two failed "не те" rounds the bot
// offers manual entry instead of asking again.
const maxRefinements = 2

// sessionTTL drops abandoned exchanges so a stale portion prompt cannot capture
// an unrelated message days later.
const sessionTTL = 30 * time.Minute

type session struct {
	Step        step
	FoodID      int64
	FoodName    string
	Portions    []domain.Portion
	RawInput    string
	Refinements int
	Draft       RegisterRequest
	UpdatedAt   time.Time
}

// sessionStore keeps conversation state in memory. Long polling allows one
// consumer per token, so there is one process and no shared state to
// coordinate. A restart loses in-flight prompts, which costs the user one
// re-ask.
type sessionStore struct {
	mu   sync.Mutex
	data map[int64]*session
}

func newSessionStore() *sessionStore {
	return &sessionStore{data: make(map[int64]*session)}
}

// get returns the live session for a user, or an empty one if the last exchange
// has expired.
func (s *sessionStore) get(userID int64) session {
	s.mu.Lock()
	defer s.mu.Unlock()

	cur, ok := s.data[userID]
	if !ok {
		return session{}
	}
	if time.Since(cur.UpdatedAt) > sessionTTL {
		delete(s.data, userID)
		return session{}
	}
	return *cur
}

func (s *sessionStore) set(userID int64, sess session) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess.UpdatedAt = time.Now()
	s.data[userID] = &sess
}

func (s *sessionStore) clear(userID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.data, userID)
}
