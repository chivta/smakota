package telegram

import (
	"errors"

	"github.com/arvlas/calbot/internal/domain"
)

// Every user-facing string the bot sends. Nothing outside this map contains
// Ukrainian text. Copy rules from the PRD: neutral and non-judgmental, no
// shaming for a missed day, and going over target reads the same as going under.
var msg = map[string]string{
	"start":               "Привіт. Запиши, що ти їла, текстом або голосом.",
	"onboarding.intro":    "Кілька питань, щоб порахувати норму. Це займе хвилину.",
	"onboarding.sex":      "Стать",
	"onboarding.age":      "Скільки тобі років?",
	"onboarding.height":   "Зріст у сантиметрах?",
	"onboarding.weight":   "Поточна вага, кг?",
	"onboarding.goal":     "Бажана вага, кг?",
	"onboarding.activity": "Наскільки активний день?",
	"onboarding.done":     "Готово. Підтримка ваги — %d ккал, твоя норма на дефіциті — %d ккал, білок — %d г. Можна змінити вручну: /target",

	"ask.what":    "Що записати?",
	"ask.portion": "Скільки?",
	"ask.grams":   "Введи грами або обери порцію.",
	"ask.clarify": "Опиши точніше, текстом або голосом.",
	"ask.manual":  "Введи калорійність на 100 г.",

	"resolve.one":    "%s\n%s",
	"resolve.many":   "Обери зі списку:",
	"resolve.none":   "Не знайшла такого. Опиши інакше або введи калорійність вручну.",
	"resolve.queued": "Записала запит, додам це до бази.",

	"saved":        "Записала: %s, %.0f г, %.0f ккал.",
	"deleted":      "Видалила.",
	"updated":      "Змінила.",
	"day.header":   "%.0f з %d ккал · лишилось %d\nБілок %.0f з %d г",
	"day.empty":    "За сьогодні поки порожньо.",
	"day.locked":   "Цей день уже не редагується.",
	"repeat.done":  "Перенесла вчорашній день: %d записів.",
	"repeat.empty": "Вчора нічого не записано.",

	"weight.ask":   "Поточна вага, кг?",
	"weight.saved": "Записала.",
	"weight.trend": "Середнє за 7 днів: %.1f кг.",

	"nudge":          "Сьогодні ще нічого не записано.",
	"weekly":         "За тиждень: %d днів із записами, у середньому %.0f ккал і %.0f г білка на день.",
	"settings.saved": "Зберегла.",

	"btn.confirm": "Записати",
	"btn.wrong":   "Не те",
	"btn.cancel":  "Скасувати",
	"btn.manual":  "Ввести вручну",
	"btn.today":   "Сьогодні",
	"btn.repeat":  "Повторити вчорашній день",
	"btn.grams":   "%.0f г",
	"btn.portion": "%s (%.0f г)",

	"state.raw":    "сирі",
	"state.cooked": "готові",
}

// errMsg maps a machine-readable error code onto its Ukrainian text. The API
// never sends human messages; this is the one place codes become words.
var errMsg = map[string]string{
	"NOT_FOUND":      "Не знайшла.",
	"INVALID_INPUT":  "Не зрозуміла. Спробуй інакше.",
	"UNAUTHORIZED":   "Бот приватний.",
	"FORBIDDEN":      "Немає доступу.",
	"CONFLICT":       "Вже є такий запис.",
	"RATE_LIMITED":   "Забагато запитів. Спробуй за хвилину.",
	"DAY_LOCKED":     "Цей день уже не редагується.",
	"UNRESOLVED":     "Не знайшла такого.",
	"NOT_EXPORTABLE": "Недоступно.",
	"INTERNAL":       "Щось пішло не так. Спробуй ще раз.",
}

// T returns the string for a key. A missing key returns the key itself, which
// shows up in testing rather than sending an empty message.
func T(key string) string {
	s, ok := msg[key]
	if !ok {
		return key
	}
	return s
}

// TErr renders any error as user-facing text, falling back to the generic
// message for anything that is not an *AppError.
func TErr(err error) string {
	var appErr *domain.AppError
	if !errors.As(err, &appErr) {
		return errMsg["INTERNAL"]
	}
	s, ok := errMsg[appErr.Code]
	if !ok {
		return errMsg["INTERNAL"]
	}
	return s
}
