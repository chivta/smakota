package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/arvlas/calbot/internal/domain"
	tele "gopkg.in/telebot.v4"
)

// Onboarding collects six answers and ends with both numbers and a one-sentence
// rationale, per the PRD. Sex and activity are buttons; the rest are numbers.

func (b *Bot) sexMarkup() *tele.ReplyMarkup {
	m := &tele.ReplyMarkup{}
	m.Inline(m.Row(
		m.Data("Жінка", cbSex, string(domain.SexFemale)),
		m.Data("Чоловік", cbSex, string(domain.SexMale)),
	))
	return m
}

func (b *Bot) activityMarkup() *tele.ReplyMarkup {
	m := &tele.ReplyMarkup{}
	m.Inline(
		m.Row(m.Data("Сидячий", cbActivity, string(domain.ActivitySedentary))),
		m.Row(m.Data("Трохи руху", cbActivity, string(domain.ActivityLight))),
		m.Row(m.Data("Помірний", cbActivity, string(domain.ActivityModerate))),
		m.Row(m.Data("Активний", cbActivity, string(domain.ActivityHigh))),
	)
	return m
}

func (b *Bot) onSexPick(c tele.Context) error {
	sess := b.sessions.get(c.Sender().ID)
	sess.Draft.Sex = domain.Sex(c.Data())
	sess.Step = stepOnboardAge
	b.sessions.set(c.Sender().ID, sess)

	return b.edit(c, T("onboarding.age"))
}

func (b *Bot) onActivityPick(c tele.Context) error {
	sess := b.sessions.get(c.Sender().ID)
	sess.Draft.ActivityLevel = domain.ActivityLevel(c.Data())
	b.sessions.set(c.Sender().ID, sess)

	return b.finishOnboarding(c, sess)
}

// handleOnboardingNumber advances one step per answer. Each step names the next
// question, so the order lives in one switch rather than spread across handlers.
func (b *Bot) handleOnboardingNumber(c tele.Context, sess session, text string) error {
	value, err := strconv.ParseFloat(strings.ReplaceAll(text, ",", "."), 64)
	if err != nil || value <= 0 {
		return b.send(c, T("onboarding."+stepQuestion(sess.Step)))
	}

	switch sess.Step {
	case stepOnboardAge:
		sess.Draft.Age = int(value)
		sess.Step = stepOnboardHeight
	case stepOnboardHeight:
		sess.Draft.HeightCM = int(value)
		sess.Step = stepOnboardWeight
	case stepOnboardWeight:
		sess.Draft.WeightKG = value
		sess.Step = stepOnboardGoal
	case stepOnboardGoal:
		sess.Draft.GoalWeightKG = value
		sess.Step = stepOnboardActivity
		b.sessions.set(c.Sender().ID, sess)
		return b.send(c, T("onboarding.activity"), b.activityMarkup())
	}

	b.sessions.set(c.Sender().ID, sess)
	return b.send(c, T("onboarding."+stepQuestion(sess.Step)))
}

// stepQuestion maps an onboarding step onto its copy key.
func stepQuestion(s step) string {
	switch s {
	case stepOnboardAge:
		return "age"
	case stepOnboardHeight:
		return "height"
	case stepOnboardWeight:
		return "weight"
	case stepOnboardGoal:
		return "goal"
	case stepOnboardActivity:
		return "activity"
	}
	return "intro"
}

func (b *Bot) finishOnboarding(c tele.Context, sess session) error {
	ctx, cancel := context.WithTimeout(context.Background(), apiTimeout)
	defer cancel()

	result, err := b.api.Register(ctx, c.Sender().ID, sess.Draft)
	if err != nil {
		return b.fail(c, err, "register user")
	}

	b.sessions.clear(c.Sender().ID)
	return b.edit(c, fmt.Sprintf(T("onboarding.done"),
		result.Maintenance, result.Target, result.Protein))
}
