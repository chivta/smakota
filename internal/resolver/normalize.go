package resolver

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Normalize produces the folded key stored in food_aliases.norm.
func Normalize(value string) string {
	var normalized strings.Builder
	normalized.Grow(len(value))

	spacePending := false
	for _, source := range strings.ToLower(value) {
		if unicode.Is(unicode.Mn, source) {
			continue
		}
		r := foldAccent(source)
		switch r {
		case 'и', 'і', 'ы':
			r = 'і'
		case 'е', 'є', 'э', 'ё':
			r = 'е'
		case 'г', 'ґ':
			r = 'г'
		case '\'', '’', 'ʼ', '`':
			continue
		}

		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if spacePending && normalized.Len() > 0 {
				normalized.WriteByte(' ')
			}
			normalized.WriteRune(r)
			spacePending = false
			continue
		}

		spacePending = normalized.Len() > 0
	}

	return normalized.String()
}

func foldAccent(r rune) rune {
	if r == 'й' || r == 'ї' {
		return r
	}
	for _, decomposed := range norm.NFD.String(string(r)) {
		if !unicode.Is(unicode.Mn, decomposed) {
			return decomposed
		}
	}
	return r
}
