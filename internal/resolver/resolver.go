// Package resolver turns free-form food input into ranked catalogue candidates.
package resolver

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/arvlas/calbot/internal/domain"
)

const (
	searchLimit     = 10
	resultLimit     = 5
	confidenceFloor = 0.35
	highScore       = 0.75
	highScoreLead   = 0.15
)

type foodSearch interface {
	Search(ctx context.Context, params SearchParams) ([]Candidate, error)
}

// SearchParams describes the catalogue search needed by Resolver.
type SearchParams struct {
	Query  string
	UserID int64
	Limit  int
}

// Candidate is one food, its normalized trigram match score, and the history
// bonus that was added to it when ranking.
type Candidate struct {
	Food  domain.Food `json:"food"`
	Score float64     `json:"score"`
	Bonus float64     `json:"bonus"`
}

// ConfidenceBand controls whether the client shows one match or a choice list.
type ConfidenceBand string

const (
	ConfidenceNone ConfidenceBand = "none"
	ConfidenceLow  ConfidenceBand = "low"
	ConfidenceHigh ConfidenceBand = "high"
)

// Request contains one resolver pass, including optional refinement context.
type Request struct {
	RawInput      string
	InputType     domain.InputType
	UserID        int64
	Clarification string
}

// Result contains the ranked matches and any amount parsed from the input.
type Result struct {
	Candidates []Candidate    `json:"candidates"`
	Quantity   *Quantity      `json:"quantity,omitempty"`
	Confidence ConfidenceBand `json:"confidence"`
}

// Resolver coordinates catalogue search and the semantic fallback. Recording
// the attempt and queueing review items belong to the caller, which is the only
// layer that knows whether the user went on to accept the result.
type Resolver struct {
	search foodSearch
	llm    *LLM
}

func NewResolver(search foodSearch, llm *LLM) *Resolver {
	return &Resolver{search: search, llm: llm}
}

// Resolve turns one free-form phrase into a ranked candidate list.
func (r *Resolver) Resolve(ctx context.Context, req Request) (Result, error) {
	quantity, phrase := ParseQuantity(req.RawInput)
	query := Normalize(phrase)
	if req.Clarification != "" {
		query = strings.TrimSpace(query + " " + Normalize(req.Clarification))
	}

	candidates, err := r.search.Search(ctx, SearchParams{
		Query:  query,
		UserID: req.UserID,
		Limit:  searchLimit,
	})
	if err != nil {
		return Result{}, fmt.Errorf("search foods: %w", err)
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})

	llmAnswered := false
	llmChoice := 0
	needsFallback := req.Clarification != "" || len(candidates) == 0 || candidates[0].Score < confidenceFloor
	if needsFallback && len(candidates) > 0 {
		llmChoice, llmAnswered = r.llm.selectCandidate(ctx, phrase, req.Clarification, candidates)
		if llmAnswered && llmChoice >= 0 {
			chosen := candidates[llmChoice]
			candidates = []Candidate{chosen}
		}
	}

	if llmAnswered && llmChoice < 0 {
		candidates = nil
	} else if !llmAnswered {
		candidates = aboveFloor(candidates)
	}
	if len(candidates) > resultLimit {
		candidates = candidates[:resultLimit]
	}

	result := Result{
		Candidates: candidates,
		Confidence: confidenceFor(candidates),
	}
	if result.Confidence == ConfidenceHigh {
		result.Candidates = result.Candidates[:1]
	}
	if quantity.Grams > 0 || quantity.Count > 0 {
		result.Quantity = &quantity
	}

	return result, nil
}

func aboveFloor(candidates []Candidate) []Candidate {
	kept := candidates[:0]
	for _, candidate := range candidates {
		if candidate.Score >= confidenceFloor {
			kept = append(kept, candidate)
		}
	}
	return kept
}

func confidenceFor(candidates []Candidate) ConfidenceBand {
	if len(candidates) == 0 {
		return ConfidenceNone
	}
	if candidates[0].Score <= highScore {
		return ConfidenceLow
	}
	if len(candidates) > 1 && candidates[0].Score-candidates[1].Score < highScoreLead {
		return ConfidenceLow
	}
	return ConfidenceHigh
}
