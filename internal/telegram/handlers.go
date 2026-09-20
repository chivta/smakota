package telegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/arvlas/calbot/internal/domain"
	tele "gopkg.in/telebot.v4"
)

func (b *Bot) onStart(c tele.Context) error {
	_, err := b.api.Me(context.Background(), c.Sender().ID)
	if err == nil {
		return b.send(c, T("start"))
	}

	var appErr *domain.AppError
	if !errors.As(err, &appErr) || appErr.Code != domain.ErrNotFound.Code {
		return b.fail(c, err, "load user")
	}

	b.sessions.set(c.Sender().ID, session{Step: stepOnboardSex})
	return b.send(c, T("onboarding.intro")+"\n\n"+T("onboarding.sex"), b.sexMarkup())
}

func (b *Bot) onCancel(c tele.Context) error {
	b.sessions.clear(c.Sender().ID)
	return b.send(c, T("settings.saved"))
}

// onText is the single entry point for free-form input. It first checks whether
// the user is mid-exchange, since a bare number after a portion prompt means
// grams, not a food name.
func (b *Bot) onText(c tele.Context) error {
	userID := c.Sender().ID
	sess := b.sessions.get(userID)
	text := strings.TrimSpace(c.Text())

	switch sess.Step {
	case stepPortion:
		return b.handlePortionInput(c, sess, text)
	case stepClarify:
		return b.handleClarification(c, sess, text)
	case stepManualKcal:
		return b.handleManualKcal(c, sess, text)
	case stepWeight:
		return b.handleWeightInput(c, text)
	case stepOnboardAge, stepOnboardHeight, stepOnboardWeight, stepOnboardGoal:
		return b.handleOnboardingNumber(c, sess, text)
	}

	return b.resolveAndOffer(c, text, "")
}

// resolveAndOffer runs the shared pipeline and branches on confidence: one
// pre-filled result for a clear match, a list of up to five otherwise.
func (b *Bot) resolveAndOffer(c tele.Context, input, clarification string) error {
	ctx, cancel := context.WithTimeout(context.Background(), apiTimeout)
	defer cancel()

	userID := c.Sender().ID
	result, err := b.api.Resolve(ctx, userID, input, clarification, domain.InputText)
	if err != nil {
		var appErr *domain.AppError
		if errors.As(err, &appErr) && appErr.Code == domain.ErrUnresolved.Code {
			return b.offerManual(c, input)
		}
		return b.fail(c, err, "resolve input")
	}

	if len(result.Candidates) == 0 {
		return b.offerManual(c, input)
	}

	top := result.Candidates[0].Food
	grams := quantityGrams(result.Quantity, top)

	// A quantity in the phrase skips the portion step entirely and goes
	// straight to the save confirmation.
	if result.HighConfidence() && grams > 0 {
		b.sessions.clear(userID)
		return b.send(c, fmt.Sprintf(T("resolve.one"), candidateLine(top),
			fmt.Sprintf("%.0f г", grams)),
			b.confirmMarkup(top.ID, grams))
	}

	if result.HighConfidence() {
		b.sessions.set(userID, session{
			Step:     stepPortion,
			FoodID:   top.ID,
			FoodName: top.Name,
			Portions: top.Portions,
			RawInput: input,
		})
		return b.send(c, candidateLine(top)+"\n"+T("ask.portion"),
			b.portionMarkup(top.ID, top.Portions))
	}

	b.sessions.set(userID, session{RawInput: input})
	return b.send(c, T("resolve.many"), b.candidateMarkup(result.Candidates))
}

// offerManual is the exit after the refinement budget runs out: enter the
// kcal by hand, and the query still reaches the review queue.
func (b *Bot) offerManual(c tele.Context, input string) error {
	b.sessions.set(c.Sender().ID, session{Step: stepManualKcal, RawInput: input})
	return b.send(c, T("resolve.none")+"\n"+T("resolve.queued"))
}

func (b *Bot) onPick(c tele.Context) error {
	foodID, err := strconv.ParseInt(c.Data(), 10, 64)
	if err != nil {
		return b.fail(c, domain.ErrInvalidInput, "parse callback")
	}

	sess := b.sessions.get(c.Sender().ID)
	sess.Step = stepPortion
	sess.FoodID = foodID
	b.sessions.set(c.Sender().ID, sess)

	return b.edit(c, T("ask.grams"), b.portionMarkup(foodID, sess.Portions))
}

// onWrong is the always-available "не те". After the refinement budget it stops
// asking and offers manual entry.
func (b *Bot) onWrong(c tele.Context) error {
	sess := b.sessions.get(c.Sender().ID)
	sess.Refinements++

	if sess.Refinements > maxRefinements {
		return b.offerManual(c, sess.RawInput)
	}

	sess.Step = stepClarify
	b.sessions.set(c.Sender().ID, sess)
	return b.edit(c, T("ask.clarify"))
}

// handleClarification re-resolves with the original input plus the new context,
// rather than starting over from the clarification alone.
func (b *Bot) handleClarification(c tele.Context, sess session, text string) error {
	return b.resolveAndOffer(c, sess.RawInput, text)
}

func (b *Bot) onManual(c tele.Context) error {
	sess := b.sessions.get(c.Sender().ID)
	sess.Step = stepManualKcal
	b.sessions.set(c.Sender().ID, sess)
	return b.edit(c, T("ask.manual"))
}

func (b *Bot) handleManualKcal(c tele.Context, sess session, text string) error {
	kcal, err := strconv.ParseFloat(strings.ReplaceAll(text, ",", "."), 64)
	if err != nil || kcal <= 0 {
		return b.send(c, T("ask.manual"))
	}

	ctx, cancel := context.WithTimeout(context.Background(), apiTimeout)
	defer cancel()

	name := sess.RawInput
	if name == "" {
		name = text
	}

	food, err := b.api.CreatePersonalFood(ctx, c.Sender().ID, name, kcal)
	if err != nil {
		return b.fail(c, err, "create personal food")
	}

	b.sessions.set(c.Sender().ID, session{
		Step:     stepPortion,
		FoodID:   food.ID,
		FoodName: food.Name,
		Portions: food.Portions,
	})
	return b.send(c, T("ask.portion"), b.portionMarkup(food.ID, food.Portions))
}

func (b *Bot) onPortionPick(c tele.Context) error {
	parts := strings.Split(c.Data(), "|")
	if len(parts) != 2 {
		return b.fail(c, domain.ErrInvalidInput, "parse portion callback")
	}

	foodID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return b.fail(c, domain.ErrInvalidInput, "parse food id")
	}

	grams, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return b.fail(c, domain.ErrInvalidInput, "parse grams")
	}

	return b.saveEntry(c, foodID, grams)
}

func (b *Bot) handlePortionInput(c tele.Context, sess session, text string) error {
	grams, err := parseGrams(text, sess.Portions)
	if err != nil {
		return b.send(c, T("ask.grams"))
	}
	return b.saveEntry(c, sess.FoodID, grams)
}

// parseGrams accepts a bare number as grams, or a count of a named portion.
func parseGrams(text string, portions []domain.Portion) (float64, error) {
	cleaned := strings.ReplaceAll(strings.TrimSpace(text), ",", ".")

	grams, err := strconv.ParseFloat(strings.TrimSuffix(cleaned, "г"), 64)
	if err == nil && grams > 0 {
		return grams, nil
	}

	fields := strings.Fields(strings.ToLower(cleaned))
	if len(fields) == 2 {
		count, convErr := strconv.ParseFloat(fields[0], 64)
		if convErr == nil {
			for _, p := range portions {
				if strings.HasPrefix(strings.ToLower(p.Name), fields[1][:minInt(len(fields[1]), 4)]) {
					return p.Grams * count, nil
				}
			}
		}
	}

	return 0, domain.ErrInvalidInput
}

func (b *Bot) saveEntry(c tele.Context, foodID int64, grams float64) error {
	ctx, cancel := context.WithTimeout(context.Background(), apiTimeout)
	defer cancel()

	entry, err := b.api.AddEntry(ctx, c.Sender().ID, foodID, grams)
	if err != nil {
		return b.fail(c, err, "save entry")
	}

	b.sessions.clear(c.Sender().ID)
	return b.send(c, fmt.Sprintf(T("saved"), esc(entry.FoodName), entry.Grams, entry.Nutrition.Kcal))
}

func (b *Bot) onConfirm(c tele.Context) error {
	parts := strings.Split(c.Data(), "|")
	if len(parts) != 2 {
		return b.fail(c, domain.ErrInvalidInput, "parse confirm callback")
	}

	foodID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return b.fail(c, domain.ErrInvalidInput, "parse food id")
	}

	grams, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return b.fail(c, domain.ErrInvalidInput, "parse grams")
	}

	return b.saveEntry(c, foodID, grams)
}

func (b *Bot) onDay(c tele.Context) error {
	ctx, cancel := context.WithTimeout(context.Background(), apiTimeout)
	defer cancel()

	day, err := b.api.Day(ctx, c.Sender().ID, "today")
	if err != nil {
		return b.fail(c, err, "load day")
	}

	return b.send(c, dayText(day))
}

func (b *Bot) onRecents(c tele.Context) error {
	ctx, cancel := context.WithTimeout(context.Background(), apiTimeout)
	defer cancel()

	recents, err := b.api.Recents(ctx, c.Sender().ID)
	if err != nil {
		return b.fail(c, err, "load recents")
	}

	if len(recents) == 0 {
		return b.send(c, T("ask.what"))
	}
	return b.send(c, T("ask.what"), b.recentsMarkup(recents))
}

// onRecentPick is the one-tap re-log at that user's last-used amount.
func (b *Bot) onRecentPick(c tele.Context) error {
	parts := strings.Split(c.Data(), "|")
	if len(parts) != 2 {
		return b.fail(c, domain.ErrInvalidInput, "parse recent callback")
	}

	foodID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return b.fail(c, domain.ErrInvalidInput, "parse food id")
	}

	grams, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return b.fail(c, domain.ErrInvalidInput, "parse grams")
	}

	return b.saveEntry(c, foodID, grams)
}

func (b *Bot) onRepeat(c tele.Context) error {
	ctx, cancel := context.WithTimeout(context.Background(), apiTimeout)
	defer cancel()

	today := time.Now().Format("2006-01-02")
	copied, err := b.api.RepeatDay(ctx, c.Sender().ID, today)
	if err != nil {
		return b.fail(c, err, "repeat day")
	}

	if copied == 0 {
		return b.send(c, T("repeat.empty"))
	}
	return b.send(c, fmt.Sprintf(T("repeat.done"), copied))
}

func (b *Bot) onDelete(c tele.Context) error {
	entryID, err := strconv.ParseInt(c.Data(), 10, 64)
	if err != nil {
		return b.fail(c, domain.ErrInvalidInput, "parse entry id")
	}

	ctx, cancel := context.WithTimeout(context.Background(), apiTimeout)
	defer cancel()

	err = b.api.DeleteEntry(ctx, c.Sender().ID, entryID)
	if err != nil {
		return b.fail(c, err, "delete entry")
	}

	return b.edit(c, T("deleted"))
}

func (b *Bot) onWeightCommand(c tele.Context) error {
	b.sessions.set(c.Sender().ID, session{Step: stepWeight})
	return b.send(c, T("weight.ask"))
}

func (b *Bot) handleWeightInput(c tele.Context, text string) error {
	kg, err := strconv.ParseFloat(strings.ReplaceAll(text, ",", "."), 64)
	if err != nil || kg <= 0 {
		return b.send(c, T("weight.ask"))
	}

	ctx, cancel := context.WithTimeout(context.Background(), apiTimeout)
	defer cancel()

	err = b.api.AddWeight(ctx, c.Sender().ID, kg)
	if err != nil {
		return b.fail(c, err, "save weight")
	}

	b.sessions.clear(c.Sender().ID)
	return b.send(c, T("weight.saved"))
}

// quantityGrams turns whatever the resolver parsed out of the phrase into
// grams: a mass unit directly, or a count of one of this food's named portions.
func quantityGrams(q *Quantity, food domain.Food) float64 {
	if q == nil {
		return 0
	}
	if q.Grams > 0 {
		return q.Grams
	}
	if q.Count <= 0 {
		return 0
	}

	for _, p := range food.Portions {
		if q.PortionName == "" || strings.HasPrefix(strings.ToLower(p.Name), strings.ToLower(q.PortionName)) {
			return p.Grams * q.Count
		}
	}
	return 0
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
