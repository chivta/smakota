package scheduler

import "time"

// The schedule predicates. Catalogue crawls are staggered across weekdays on
// purpose, so one source's rate limit cannot starve another.

// Daily fires once a day at hour:minute.
func Daily(hour, minute int) func(time.Time) bool {
	return func(t time.Time) bool {
		return t.Hour() == hour && t.Minute() == minute
	}
}

// Weekly fires once a week on the given weekday.
func Weekly(day time.Weekday, hour, minute int) func(time.Time) bool {
	return func(t time.Time) bool {
		return t.Weekday() == day && t.Hour() == hour && t.Minute() == minute
	}
}

// Monthly fires on the given day of the month.
func Monthly(dayOfMonth, hour, minute int) func(time.Time) bool {
	return func(t time.Time) bool {
		return t.Day() == dayOfMonth && t.Hour() == hour && t.Minute() == minute
	}
}

// Never keeps a job registered but idle. The quarterly reference refresh uses
// it, so it exists as a named manual run rather than a surprise crawl.
func Never() func(time.Time) bool {
	return func(time.Time) bool { return false }
}
