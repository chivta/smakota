package telegram

import tele "gopkg.in/telebot.v4"

// Callback unique prefixes. Telegram caps callback data at 64 bytes, so these
// stay short and carry only an ID.
const (
	cbPick     = "pick"
	cbConfirm  = "ok"
	cbWrong    = "no"
	cbManual   = "man"
	cbPortion  = "por"
	cbDelete   = "del"
	cbRepeat   = "rep"
	cbSex      = "sex"
	cbActivity = "act"
	cbRecent   = "rec"
)

func (b *Bot) routes() {
	b.bot.Use(b.gate)

	b.bot.Handle("/start", b.onStart)
	b.bot.Handle("/day", b.onDay)
	b.bot.Handle("/recents", b.onRecents)
	b.bot.Handle("/weight", b.onWeightCommand)
	b.bot.Handle("/cancel", b.onCancel)

	b.bot.Handle(tele.OnText, b.onText)

	b.bot.Handle(&tele.Btn{Unique: cbPick}, b.onPick)
	b.bot.Handle(&tele.Btn{Unique: cbConfirm}, b.onConfirm)
	b.bot.Handle(&tele.Btn{Unique: cbWrong}, b.onWrong)
	b.bot.Handle(&tele.Btn{Unique: cbManual}, b.onManual)
	b.bot.Handle(&tele.Btn{Unique: cbPortion}, b.onPortionPick)
	b.bot.Handle(&tele.Btn{Unique: cbDelete}, b.onDelete)
	b.bot.Handle(&tele.Btn{Unique: cbRepeat}, b.onRepeat)
	b.bot.Handle(&tele.Btn{Unique: cbSex}, b.onSexPick)
	b.bot.Handle(&tele.Btn{Unique: cbActivity}, b.onActivityPick)
	b.bot.Handle(&tele.Btn{Unique: cbRecent}, b.onRecentPick)
}

// registerCommands publishes the command menu, which is what makes /day
// discoverable. Without it Telegram shows no "/" hint and the day view is
// reachable only by someone who already knows the command exists.
func (b *Bot) registerCommands() error {
	return b.bot.SetCommands([]tele.Command{
		{Text: "day", Description: T("cmd.day")},
		{Text: "recents", Description: T("cmd.recents")},
		{Text: "weight", Description: T("cmd.weight")},
		{Text: "cancel", Description: T("cmd.cancel")},
	})
}
