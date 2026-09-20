// Package telegram is a client of the HTTP API. It holds the conversation
// shape and the Ukrainian copy; it holds no business logic and no database.
package telegram

import (
	"context"
	"fmt"
	"html"
	"time"

	"github.com/rs/zerolog/log"
	"golang.org/x/time/rate"
	tele "gopkg.in/telebot.v4"
)

// Telegram's ceiling is around 30 calls per second for the whole bot. One
// limiter covers every send, edit and delete, so a burst of worker edits cannot
// starve a user's reply.
const (
	sendRate  = 25
	sendBurst = 5

	pollTimeout = 10 * time.Second
)

type Bot struct {
	bot      *tele.Bot
	api      *Client
	limiter  *rate.Limiter
	sessions *sessionStore
	allowed  map[int64]bool
}

type Options struct {
	Token        string
	APIURL       string
	APIBaseURL   string
	ServiceToken string
	AllowedUsers []int64
}

// New builds the bot. An empty allow list means the bot answers anyone, which
// is logged at startup rather than left to be discovered.
func New(opts Options) (*Bot, error) {
	settings := tele.Settings{
		Token:  opts.Token,
		Poller: &tele.LongPoller{Timeout: pollTimeout},
	}
	if opts.APIURL != "" {
		settings.URL = opts.APIURL
	}

	inner, err := tele.NewBot(settings)
	if err != nil {
		return nil, fmt.Errorf("new telebot: %w", err)
	}

	allowed := make(map[int64]bool, len(opts.AllowedUsers))
	for _, id := range opts.AllowedUsers {
		allowed[id] = true
	}
	if len(allowed) == 0 {
		log.Warn().Msg("ALLOWED_USERS is empty, the bot answers everyone")
	}

	b := &Bot{
		bot:      inner,
		api:      NewClient(opts.APIBaseURL, opts.ServiceToken),
		limiter:  rate.NewLimiter(sendRate, sendBurst),
		sessions: newSessionStore(),
		allowed:  allowed,
	}
	b.routes()

	return b, nil
}

// Start runs the poller until ctx is cancelled.
func (b *Bot) Start(ctx context.Context) {
	go func() {
		<-ctx.Done()
		b.bot.Stop()
	}()
	b.bot.Start()
}

// gate drops updates from anyone outside the allow list. The bot's username is
// public, so without this anyone can message it.
func (b *Bot) gate(next tele.HandlerFunc) tele.HandlerFunc {
	return func(c tele.Context) error {
		if len(b.allowed) == 0 {
			return next(c)
		}
		if b.allowed[c.Sender().ID] {
			return next(c)
		}
		log.Warn().Int64("telegram_id", c.Sender().ID).Msg("update from a user outside the allow list")
		return nil
	}
}

// send rate-limits every outbound call and renders under ModeHTML, whose
// escaping rules are far less error-prone than Markdown's.
func (b *Bot) send(c tele.Context, what any, opts ...any) error {
	err := b.limiter.Wait(context.Background())
	if err != nil {
		return fmt.Errorf("rate limiter: %w", err)
	}
	return c.Send(what, append(opts, tele.ModeHTML)...)
}

func (b *Bot) edit(c tele.Context, what any, opts ...any) error {
	err := b.limiter.Wait(context.Background())
	if err != nil {
		return fmt.Errorf("rate limiter: %w", err)
	}
	return c.Edit(what, append(opts, tele.ModeHTML)...)
}

// esc escapes a value interpolated into an HTML-mode message. One raw "<" makes
// Telegram reject the whole message.
func esc(s string) string {
	return html.EscapeString(s)
}

// fail reports an error to the user in Ukrainian and logs it once, here, where
// it is handled.
func (b *Bot) fail(c tele.Context, err error, what string) error {
	log.Error().Err(err).Str("op", what).Int64("telegram_id", c.Sender().ID).Msg("bot handler failed")
	return b.send(c, TErr(err))
}
