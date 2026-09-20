package resolver

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
)

const llmTimeout = 2 * time.Second

// LLM maps ambiguous input to one of the candidates already found by search.
type LLM struct {
	client openai.Client
	model  string
}

// NewLLM configures the low-confidence resolver fallback.
func NewLLM(client openai.Client, model string) *LLM {
	return &LLM{client: client, model: model}
}

func (l *LLM) selectCandidate(ctx context.Context, phrase, clarification string, candidates []Candidate) (int, bool) {
	if l == nil || l.model == "" || len(candidates) == 0 {
		return 0, false
	}

	var prompt strings.Builder
	prompt.WriteString("Food: ")
	prompt.WriteString(phrase)
	if clarification != "" {
		prompt.WriteString("\nClarification: ")
		prompt.WriteString(clarification)
	}
	prompt.WriteString("\nCandidates:\n")
	for index, candidate := range candidates {
		fmt.Fprintf(&prompt, "%d. %s\n", index+1, candidate.Food.Name)
	}
	prompt.WriteString("Reply with one candidate number or none.")

	llmCtx, cancel := context.WithTimeout(ctx, llmTimeout)
	defer cancel()

	completion, err := l.client.Chat.Completions.New(llmCtx, openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{{
			OfSystem: &openai.ChatCompletionSystemMessageParam{
				Content: openai.ChatCompletionSystemMessageParamContentUnion{
					OfString: openai.String("Match Ukrainian or Russian food input. Return only a candidate number or none."),
				},
			},
		}, {
			OfUser: &openai.ChatCompletionUserMessageParam{
				Content: openai.ChatCompletionUserMessageParamContentUnion{
					OfString: openai.String(prompt.String()),
				},
			},
		}},
		Model:               l.model,
		MaxCompletionTokens: openai.Int(4),
		Temperature:         openai.Float(0),
	})
	if err != nil || len(completion.Choices) == 0 {
		return 0, false
	}

	answer := strings.TrimSpace(strings.ToLower(completion.Choices[0].Message.Content))
	if answer == "none" {
		return -1, true
	}

	selected, err := strconv.Atoi(answer)
	if err != nil || selected < 1 || selected > len(candidates) {
		return 0, false
	}
	return selected - 1, true
}
