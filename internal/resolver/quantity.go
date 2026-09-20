package resolver

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	halfKiloPattern  = regexp.MustCompile(`(?i)пів[[:space:]-]*кіло(грам(а|и|ів)?)?`)
	massPattern      = regexp.MustCompile(`(?i)([0-9]+([.,][0-9]+)?)[[:space:]]*(кілограм(а|и|ів)?|кг|kg|грам(а|и|ів)?|гр|г|g)`)
	countPattern     = regexp.MustCompile(`(?i)([0-9]+([.,][0-9]+)?)[[:space:]]*(склян(ка|ки|ок)|шт(ука|уки|ук)?|порці(я|ї|й)|таріл(ка|ки|ок)|шмат(ок|ки|ків))`)
	emptyPairPattern = regexp.MustCompile(`\([[:space:]]*\)|\[[[:space:]]*\]|\{[[:space:]]*\}`)
	spacePattern     = regexp.MustCompile(`[[:space:]]+`)
)

// Quantity is either a mass in grams or a count of a named portion.
type Quantity struct {
	Grams   float64 `json:"grams,omitempty"`
	Count   float64 `json:"count,omitempty"`
	Portion string  `json:"portion,omitempty"`
}

// ParseQuantity removes the first supported quantity from a food phrase.
func ParseQuantity(phrase string) (Quantity, string) {
	location := halfKiloPattern.FindStringIndex(phrase)
	if location != nil {
		return Quantity{Grams: 500}, removeQuantity(phrase, location[0], location[1])
	}

	matches := massPattern.FindStringSubmatchIndex(phrase)
	if matches != nil {
		amount, ok := parseAmount(phrase[matches[2]:matches[3]])
		if ok && amount > 0 {
			unit := strings.ToLower(phrase[matches[6]:matches[7]])
			if unit == "кг" || unit == "kg" || strings.HasPrefix(unit, "кілограм") {
				amount *= 1000
			}
			return Quantity{Grams: amount}, removeQuantity(phrase, matches[0], matches[1])
		}
	}

	matches = countPattern.FindStringSubmatchIndex(phrase)
	if matches != nil {
		amount, ok := parseAmount(phrase[matches[2]:matches[3]])
		if ok && amount > 0 {
			portion := strings.ToLower(phrase[matches[6]:matches[7]])
			return Quantity{Count: amount, Portion: portion}, removeQuantity(phrase, matches[0], matches[1])
		}
	}

	return Quantity{}, strings.TrimSpace(spacePattern.ReplaceAllString(phrase, " "))
}

func parseAmount(value string) (float64, bool) {
	amount, err := strconv.ParseFloat(strings.Replace(value, ",", ".", 1), 64)
	return amount, err == nil
}

func removeQuantity(phrase string, start, end int) string {
	remaining := phrase[:start] + phrase[end:]
	remaining = emptyPairPattern.ReplaceAllString(remaining, " ")
	remaining = spacePattern.ReplaceAllString(remaining, " ")
	return strings.Trim(strings.TrimSpace(remaining), ",.;:-")
}
