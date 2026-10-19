// Package mcdonalds imports prepared dishes from the nutrition PDF McDonald's
// Ukraine publishes for its whole menu.
package mcdonalds

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/arvlas/calbot/internal/domain"
	"github.com/arvlas/calbot/internal/ingest"
	"github.com/ledongthuc/pdf"
	"github.com/rs/zerolog/log"
	"golang.org/x/time/rate"
)

const (
	// mcdonaldsRPS is irrelevant for one request but the client requires a limit.
	mcdonaldsRPS = rate.Limit(1)
	// maxPDFBytes bounds the download; the real file is about 364 KB.
	maxPDFBytes = 20 << 20

	portionName = "порція"
	gramsPer100 = 100.0
)

var (
	// Values are per serving. Decimals use a comma or a dot, and the dash is an
	// en dash, so the pattern accepts a hyphen too in case the PDF is re-typeset.
	servingPattern = regexp.MustCompile(`Маса порції:\s*([\d.,]+)\s*г`)
	volumePattern  = regexp.MustCompile(`Об['’]єм:`)
	proteinPattern = regexp.MustCompile(`білки\s*[–-]\s*([\d.,]+)\s*г`)
	fatPattern     = regexp.MustCompile(`;\s*жири\s*[–-]\s*([\d.,]+)\s*г`)
	carbsPattern   = regexp.MustCompile(`вуглеводи\s*[–-]\s*([\d.,]+)\s*г`)
	kcalPattern    = regexp.MustCompile(`([\d.,]+)\s*кКал`)
	// pageFurniture is the header every page repeats. It can fall between the
	// two copies of an item heading, so it is dropped before headings are matched.
	pageFurniture = regexp.MustCompile(`^(Сторінка \d+ з \d+|ДІЄ З .*|with Breakfast)$`)
)

// Adapter reads one PDF and emits one row per menu item.
type Adapter struct {
	http *ingest.HTTPClient
	url  string
}

// New constructs a McDonald's Ukraine adapter.
func New(httpClient *ingest.HTTPClient, pdfURL string) *Adapter {
	return &Adapter{http: httpClient, url: pdfURL}
}

// Name returns the source identifier stored with catalogue rows.
func (a *Adapter) Name() string { return "mcdonalds" }

// First-party nutrition outranks a scraped retailer catalogue, and no licence
// for the PDF was found, so rows stay lookup-only.
func (a *Adapter) Provenance() ingest.Provenance {
	return ingest.Provenance{
		LicenseClass: domain.LicenseLookupOnly,
		Confidence:   domain.ConfidenceOpenDB,
	}
}

// item is one menu entry with its values still per serving.
type item struct {
	Name     string  `json:"name"`
	ServingG float64 `json:"serving_g"`
	Kcal     float64 `json:"kcal"`
	Protein  float64 `json:"protein"`
	Fat      float64 `json:"fat"`
	Carbs    float64 `json:"carbs"`
}

// Fetch downloads and parses the PDF. The cursor is unused.
func (a *Adapter) Fetch(ctx context.Context, _ string, emit func(ingest.Raw) error) (string, error) {
	response, err := a.http.Get(ctx, a.Name(), a.url, mcdonaldsRPS, false)
	if err != nil {
		return "", err
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxPDFBytes))
	response.Body.Close()
	if readErr != nil {
		return "", fmt.Errorf("read McDonald's PDF: %w", readErr)
	}
	text, err := extractText(body)
	if err != nil {
		return "", err
	}

	items, skipped := parseItems(text)
	log.Info().Int("items", len(items)).Int("skipped", len(skipped)).
		Strs("skipped_items", skipped).Msg("McDonald's menu parsed; skipped items have no serving mass")
	for _, it := range items {
		err = emit(rawFor(it, a.url))
		if err != nil {
			return "", err
		}
	}
	return "", nil
}

func extractText(data []byte) (string, error) {
	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("open McDonald's PDF: %w", err)
	}
	var text strings.Builder
	for pageNumber := 1; pageNumber <= reader.NumPage(); pageNumber++ {
		pageText, pageErr := reader.Page(pageNumber).GetPlainText(nil)
		if pageErr != nil {
			return "", fmt.Errorf("read McDonald's PDF page %d: %w", pageNumber, pageErr)
		}
		text.WriteString(pageText)
		text.WriteString("\n")
	}
	return text.String(), nil
}

// parseItems splits the text at item headings, which the PDF prints twice in a
// row. Items priced per volume or lacking a serving mass cannot be converted to
// per 100 g and are returned by name in skipped.
func parseItems(text string) (items []item, skipped []string) {
	type block struct {
		name string
		body strings.Builder
	}
	var blocks []*block
	var previous string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || pageFurniture.MatchString(line) {
			continue
		}
		if line == previous {
			blocks = append(blocks, &block{name: line})
		} else if len(blocks) > 0 {
			current := blocks[len(blocks)-1]
			current.body.WriteString(line)
			current.body.WriteString(" ")
		}
		previous = line
	}

	for _, b := range blocks {
		body := b.body.String()
		if !strings.Contains(body, "кКал") {
			continue
		}
		it, ok := parseItem(b.name, body)
		if !ok {
			skipped = append(skipped, b.name)
			continue
		}
		items = append(items, it)
	}
	return items, skipped
}

func parseItem(name, body string) (item, bool) {
	if volumePattern.MatchString(body) {
		return item{}, false
	}
	serving, ok := number(servingPattern, body)
	if !ok || serving <= 0 {
		return item{}, false
	}
	kcal, kcalOK := number(kcalPattern, body)
	protein, proteinOK := number(proteinPattern, body)
	fat, fatOK := number(fatPattern, body)
	carbs, carbsOK := number(carbsPattern, body)
	if !kcalOK || !proteinOK || !fatOK || !carbsOK {
		return item{}, false
	}
	return item{Name: name, ServingG: serving, Kcal: kcal, Protein: protein, Fat: fat, Carbs: carbs}, true
}

func number(pattern *regexp.Regexp, text string) (float64, bool) {
	match := pattern.FindStringSubmatch(text)
	if match == nil {
		return 0, false
	}
	value, err := strconv.ParseFloat(strings.ReplaceAll(match[1], ",", "."), 64)
	return value, err == nil
}

// rawFor scales the per-serving values to per 100 g and keeps the serving as
// the default portion.
func rawFor(it item, sourceURL string) ingest.Raw {
	scale := gramsPer100 / it.ServingG
	payload, _ := json.Marshal(it) // a struct of strings and floats cannot fail
	return ingest.Raw{
		ExternalID: strings.ToLower(it.Name),
		Payload:    payload,
		SourceURL:  sourceURL,
		Product: ingest.RawProduct{
			Name:    it.Name,
			Kcal:    per100(it.Kcal * scale),
			Protein: per100(it.Protein * scale),
			Fat:     per100(it.Fat * scale),
			Carbs:   per100(it.Carbs * scale),
		},
		Kind:     domain.KindDish,
		State:    domain.StateCooked,
		Portions: []domain.Portion{{Name: portionName, Grams: it.ServingG, IsDefault: true}},
	}
}

func per100(value float64) string {
	return strconv.FormatFloat(value, 'f', 2, 64)
}
