// Package fora imports lookup-only products from the Фора online catalogue API.
package fora

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/arvlas/calbot/internal/domain"
	"github.com/arvlas/calbot/internal/ingest"
	"github.com/rs/zerolog/log"
	"golang.org/x/time/rate"
)

const (
	endpoint = "https://api.catalog.ecom.fora.ua/api/2.0/exec/EcomCatalogGlobal"
	// The API rejects requests without a site Origin and Referer.
	siteOrigin  = "https://fora.ua"
	siteReferer = "https://fora.ua/"
	method      = "GetSimpleCatalogItems"
	siteURL     = "https://fora.ua/"
	foraRPS     = rate.Limit(2)
	pageSize    = 60

	// cursorDone is saved when a walk finishes so the next run starts over.
	cursorDone      = "0"
	cursorSeparator = "|"
)

// Parameter keys that carry nutrition per 100 g.
const (
	keyCalorie = "calorie"
	// Own-production items (pizza, grilled chicken, burgers) carry energy under
	// a different key and in a different format from packaged goods.
	keyKalorii       = "kalorii"
	keyProteins      = "proteins"
	keyFats          = "fats"
	keyCarbohydrates = "carbohydrates"
	keyTrademark     = "trademark"
)

// Adapter walks each configured store's catalogue by offset and unions them.
type Adapter struct {
	http      *ingest.HTTPClient
	filialIDs []int
}

// New constructs a Фора adapter. filialIDs is an ordered fallback list: each
// store stocks a subset of the range, nutrition does not vary by store.
func New(httpClient *ingest.HTTPClient, filialIDs []int) *Adapter {
	return &Adapter{http: httpClient, filialIDs: filialIDs}
}

// Name returns the source identifier stored with catalogue rows.
func (a *Adapter) Name() string { return "fora" }

// fora.ua publishes no terms page, so its rows are treated as unreproducible.
func (a *Adapter) Provenance() ingest.Provenance {
	return ingest.Provenance{
		LicenseClass: domain.LicenseLookupOnly,
		Confidence:   domain.ConfidenceScraped,
	}
}

type parameter struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type item struct {
	ID         int64       `json:"id"`
	Name       string      `json:"name"`
	Parameters []parameter `json:"parameters"`
}

type catalogResponse struct {
	ItemsCount int               `json:"itemsCount"`
	Items      []json.RawMessage `json:"items"`
}

type request struct {
	Method string      `json:"method"`
	Data   requestData `json:"data"`
}

type requestData struct {
	FilialID int `json:"filialId"`
	From     int `json:"from"`
	To       int `json:"to"`
}

// errEmit wraps an error from emit so a store failure is not confused with it.
type errEmit struct{ err error }

func (e errEmit) Error() string { return e.err.Error() }
func (e errEmit) Unwrap() error { return e.err }

// Fetch stores the store and offset in its cursor. Products without a
// nutrition parameter are skipped, not rejected. A store that errors or comes
// back empty is logged and the next one is tried.
func (a *Adapter) Fetch(ctx context.Context, cursor string, emit func(ingest.Raw) error) (string, error) {
	position, err := parseCursor(cursor)
	if err != nil {
		return "", err
	}

	// A store missing from the config means the list changed; start over.
	start := slices.Index(a.filialIDs, position.filial)
	if start < 0 {
		start, position = 0, cursorPosition{}
	}

	seen := make(map[int64]struct{})
	for _, filialID := range a.filialIDs[start:] {
		offset := 0
		if filialID == position.filial {
			offset = position.offset
		}
		fetchErr := a.fetchStore(ctx, filialID, offset, seen, emit)
		var emitErr errEmit
		switch {
		case fetchErr == nil:
		case ctx.Err() != nil:
			return "", ctx.Err()
		case errors.As(fetchErr, &emitErr):
			return "", emitErr.err
		default:
			log.Warn().Err(fetchErr).Int("filial_id", filialID).Msg("fora store skipped")
		}
	}
	return cursorDone, nil
}

func (a *Adapter) fetchStore(ctx context.Context, filialID, offset int, seen map[int64]struct{}, emit func(ingest.Raw) error) error {
	for {
		page, err := a.fetchPage(ctx, filialID, offset)
		if err != nil {
			return err
		}
		if len(page.Items) == 0 {
			if offset == 0 {
				return fmt.Errorf("fora store %d returned no items", filialID)
			}
			return nil
		}

		for _, payload := range page.Items {
			var p item
			err = json.Unmarshal(payload, &p)
			if err != nil {
				return fmt.Errorf("decode Fora product in store %d: %w", filialID, err)
			}
			if _, dup := seen[p.ID]; dup {
				continue
			}
			nutrition, ok := nutritionOf(p.Parameters)
			if !ok {
				continue
			}
			seen[p.ID] = struct{}{}
			// The page start is the cursor: a flush can land mid-page, and a
			// resume re-reads the whole page.
			err = emit(ingest.Raw{
				ExternalID: strconv.FormatInt(p.ID, 10),
				Payload:    payload,
				SourceURL:  siteURL,
				Cursor:     formatCursor(cursorPosition{filial: filialID, offset: offset}),
				Product: ingest.RawProduct{
					Name:    p.Name,
					Brand:   parameterValue(p.Parameters, keyTrademark),
					Kcal:    nutrition.Kcal,
					Protein: nutrition.Protein,
					Fat:     nutrition.Fat,
					Carbs:   nutrition.Carbs,
				},
			})
			if err != nil {
				return errEmit{err}
			}
		}

		offset += pageSize
		if offset >= page.ItemsCount {
			return nil
		}
	}
}

func (a *Adapter) fetchPage(ctx context.Context, filialID, offset int) (catalogResponse, error) {
	body, err := json.Marshal(request{Method: method, Data: requestData{
		FilialID: filialID,
		From:     offset + 1,
		To:       offset + pageSize,
	}})
	if err != nil {
		return catalogResponse{}, fmt.Errorf("encode Fora request: %w", err)
	}
	header := http.Header{
		"Content-Type": {"application/json"},
		"Origin":       {siteOrigin},
		"Referer":      {siteReferer},
	}
	response, err := a.http.PostJSON(ctx, a.Name(), endpoint, body, header, foraRPS)
	if err != nil {
		return catalogResponse{}, err
	}
	defer response.Body.Close()
	var page catalogResponse
	err = json.NewDecoder(response.Body).Decode(&page)
	if err != nil {
		return catalogResponse{}, fmt.Errorf("decode Fora store %d offset %d: %w", filialID, offset, err)
	}
	return page, nil
}

type nutrition struct {
	Kcal, Protein, Fat, Carbs string
}

// nutritionOf maps parameters to nutrition strings for the shared normalizer.
// ok is false when the product carries none of the four, as non-food does.
func nutritionOf(params []parameter) (n nutrition, ok bool) {
	n = nutrition{
		Kcal:    energyOf(params),
		Protein: parameterValue(params, keyProteins),
		Fat:     parameterValue(params, keyFats),
		Carbs:   parameterValue(params, keyCarbohydrates),
	}
	return n, n != nutrition{}
}

// kcalFromLabelled pulls the kcal figure out of the own-production format,
// "1143кДж (kJ) / 272 ккал (kcal)", where the kJ value comes first and both
// halves are labelled. Packaged goods instead use "157/660", which the shared
// normalizer already reads as a kcal/kJ pair, so that form is passed through
// untouched.
var kcalLabelled = regexp.MustCompile(`([0-9]+(?:[.,][0-9]+)?)\s*ккал`)

// energyOf prefers the packaged-goods key and falls back to the
// own-production one.
func energyOf(params []parameter) string {
	if v := parameterValue(params, keyCalorie); v != "" {
		return v
	}
	labelled := parameterValue(params, keyKalorii)
	if labelled == "" {
		return ""
	}
	if m := kcalLabelled.FindStringSubmatch(labelled); m != nil {
		return m[1]
	}
	// An unrecognised shape is handed over as-is so the row is rejected with a
	// reason rather than silently dropped.
	return labelled
}

func parameterValue(params []parameter, key string) string {
	for _, p := range params {
		if p.Key == key {
			return strings.TrimSpace(p.Value)
		}
	}
	return ""
}

type cursorPosition struct {
	filial int
	offset int
}

func formatCursor(p cursorPosition) string {
	return strconv.Itoa(p.filial) + cursorSeparator + strconv.Itoa(p.offset)
}

// parseCursor accepts the empty string and cursorDone as "start over".
func parseCursor(cursor string) (cursorPosition, error) {
	if cursor == "" || cursor == cursorDone {
		return cursorPosition{}, nil
	}
	store, offsetText, found := strings.Cut(cursor, cursorSeparator)
	filial, filialErr := strconv.Atoi(store)
	offset, offsetErr := strconv.Atoi(offsetText)
	if !found || filialErr != nil || offsetErr != nil || filial < 1 || offset < 0 {
		return cursorPosition{}, fmt.Errorf("invalid Fora cursor %q", cursor)
	}
	return cursorPosition{filial: filial, offset: offset}, nil
}
