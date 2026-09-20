package telegram

import (
	"fmt"
	"strings"

	"github.com/arvlas/calbot/internal/domain"
	tele "gopkg.in/telebot.v4"
)

// candidateLine is one row in the "обери зі списку" list. The raw or cooked
// state is shown, per the PRD, because it changes the number.
func candidateLine(c domain.Food) string {
	var b strings.Builder
	if c.Brand != "" {
		b.WriteString(esc(c.Brand))
		b.WriteString(" ")
	}
	b.WriteString(esc(c.Name))
	if c.State == domain.StateRaw || c.State == domain.StateCooked {
		b.WriteString(" (")
		b.WriteString(T("state." + string(c.State)))
		b.WriteString(")")
	}
	b.WriteString(fmt.Sprintf(", %.0f ккал/100 г", c.Nutrition.Kcal))
	return b.String()
}

// dayText renders the flat chronological list. No meal grouping, and going over
// the target reads exactly like going under.
func dayText(d DayView) string {
	header := fmt.Sprintf(T("day.header"),
		d.Consumed.Kcal, d.KcalTarget, d.KcalTarget-int(d.Consumed.Kcal),
		d.Consumed.Protein, d.ProteinTarget)

	if len(d.Entries) == 0 {
		return header + "\n\n" + T("day.empty")
	}

	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n")
	for _, e := range d.Entries {
		b.WriteString(fmt.Sprintf("\n%s · %.0f г · %.0f ккал",
			esc(e.FoodName), e.Grams, e.Nutrition.Kcal))
	}
	return b.String()
}

// candidateMarkup lists up to five candidates, one button each.
func (b *Bot) candidateMarkup(cands []Candidate) *tele.ReplyMarkup {
	m := &tele.ReplyMarkup{}
	rows := make([]tele.Row, 0, len(cands)+1)

	for _, c := range cands {
		btn := m.Data(truncate(c.Food.Name, 40), cbPick, fmt.Sprint(c.Food.ID))
		rows = append(rows, m.Row(btn))
	}
	rows = append(rows, m.Row(m.Data(T("btn.manual"), cbManual)))

	m.Inline(rows...)
	return m
}

// confirmMarkup is the one-tap save for a high-confidence match, with the
// always-available way out.
func (b *Bot) confirmMarkup(foodID int64, grams float64) *tele.ReplyMarkup {
	m := &tele.ReplyMarkup{}
	m.Inline(m.Row(
		m.Data(T("btn.confirm"), cbConfirm, fmt.Sprint(foodID), fmt.Sprintf("%.0f", grams)),
		m.Data(T("btn.wrong"), cbWrong),
	))
	return m
}

// portionMarkup offers the entry's named portions beside the numeric input.
func (b *Bot) portionMarkup(foodID int64, portions []domain.Portion) *tele.ReplyMarkup {
	m := &tele.ReplyMarkup{}
	row := make(tele.Row, 0, 3)

	for i, p := range portions {
		if i == 3 {
			break
		}
		label := fmt.Sprintf(T("btn.portion"), p.Name, p.Grams)
		row = append(row, m.Data(label, cbPortion, fmt.Sprint(foodID), fmt.Sprintf("%.0f", p.Grams)))
	}

	if len(row) == 0 {
		row = append(row, m.Data(fmt.Sprintf(T("btn.grams"), 100.0), cbPortion, fmt.Sprint(foodID), "100"))
	}
	m.Inline(m.Row(row...))
	return m
}

// recentsMarkup is the quick-add keyboard: each button re-logs that food at the
// user's last-used amount in one tap.
func (b *Bot) recentsMarkup(recents []RecentFood) *tele.ReplyMarkup {
	m := &tele.ReplyMarkup{}
	rows := make([]tele.Row, 0, len(recents)+1)

	for _, r := range recents {
		label := fmt.Sprintf("%s · %.0f г", truncate(r.Food.Name, 30), r.LastGrams)
		rows = append(rows, m.Row(m.Data(label, cbRecent,
			fmt.Sprint(r.Food.ID), fmt.Sprintf("%.0f", r.LastGrams))))
	}
	rows = append(rows, m.Row(m.Data(T("btn.repeat"), cbRepeat)))

	m.Inline(rows...)
	return m
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}
