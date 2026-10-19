// Package off imports Open Food Facts products: the whole catalogue from the
// bulk CSV, and the Ukrainian subset from the search API.
package off

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/arvlas/calbot/internal/domain"
	"github.com/arvlas/calbot/internal/ingest"
	"golang.org/x/time/rate"
)

const (
	// sourceName is shared by both modes, so a product found by each is one
	// catalogue row.
	sourceName = "off"

	searchEndpoint = "https://search.openfoodfacts.org/search"
	searchQuery    = `countries_tags:"en:ukraine"`
	searchFields   = "code,product_name,product_name_uk,product_name_ru,product_name_en,brands,quantity,nutriments"
	searchPageSize = 100
	offRate        = rate.Limit(2)

	productURLPrefix = "https://world.openfoodfacts.org/product/"

	deltaCursorPrefix = "delta:"
	fullCursorPrefix  = "full:"
)

type mode int

const (
	modeDelta mode = iota
	modeFull
)

// Adapter runs one OFF job. The job picks the mode, never the stored cursor.
type Adapter struct {
	http    *ingest.HTTPClient
	mode    mode
	dumpURL string
}

// NewDelta constructs the job that walks the Ukrainian products in the search
// API.
func NewDelta(httpClient *ingest.HTTPClient) *Adapter {
	return &Adapter{http: httpClient, mode: modeDelta}
}

// NewFull constructs the job that streams the bulk CSV at dumpURL. The client
// must not carry an overall timeout, because the download takes longer than any.
func NewFull(httpClient *ingest.HTTPClient, dumpURL string) *Adapter {
	return &Adapter{http: httpClient, mode: modeFull, dumpURL: dumpURL}
}

// Name returns the source identifier stored with catalogue rows.
func (a *Adapter) Name() string { return sourceName }

// CursorKey keeps the two modes' progress apart: the delta's page number must
// not be read as a dump line.
func (a *Adapter) CursorKey() string {
	if a.mode == modeFull {
		return sourceName + "-full"
	}
	return sourceName + "-delta"
}

// Open Food Facts is ODbL, so its rows may be redistributed with attribution.
func (a *Adapter) Provenance() ingest.Provenance {
	return ingest.Provenance{
		LicenseClass: domain.LicenseRedistributable,
		Confidence:   domain.ConfidenceOpenDB,
	}
}

func (a *Adapter) Fetch(ctx context.Context, cursor string, emit func(ingest.Raw) error) (string, error) {
	a.http.ResetCache()
	if a.mode == modeFull {
		return a.fetchFull(ctx, cursor, emit)
	}
	return a.fetchDelta(ctx, cursor, emit)
}

type searchHit struct {
	Code       string                     `json:"code"`
	Name       *string                    `json:"product_name"`
	NameUK     *string                    `json:"product_name_uk"`
	NameRU     *string                    `json:"product_name_ru"`
	NameEN     *string                    `json:"product_name_en"`
	Brands     []string                   `json:"brands"`
	Quantity   string                     `json:"quantity"`
	Nutriments map[string]json.RawMessage `json:"nutriments"`
}

type searchResponse struct {
	Hits      []json.RawMessage `json:"hits"`
	PageCount int               `json:"page_count"`
}

// fetchDelta reads every Ukrainian product, newest first. The search index
// carries no change feed, so a run re-reads all pages; unchanged rows cost
// nothing downstream because staging skips an identical content hash.
func (a *Adapter) fetchDelta(ctx context.Context, cursor string, emit func(ingest.Raw) error) (string, error) {
	page, err := parseDeltaCursor(cursor)
	if err != nil {
		return "", err
	}
	for {
		query := url.Values{}
		query.Set("q", searchQuery)
		query.Set("fields", searchFields)
		query.Set("sort_by", "-last_modified_t")
		query.Set("page_size", strconv.Itoa(searchPageSize))
		query.Set("page", strconv.Itoa(page))
		endpoint := searchEndpoint + "?" + query.Encode()

		response, err := a.http.Get(ctx, sourceName, endpoint, offRate, true)
		if err != nil {
			return "", err
		}
		var result searchResponse
		decodeErr := json.NewDecoder(response.Body).Decode(&result)
		response.Body.Close()
		if decodeErr != nil {
			return "", fmt.Errorf("decode OFF search page %d: %w", page, decodeErr)
		}

		for _, payload := range result.Hits {
			raw, err := rawFromHit(payload)
			if err != nil {
				return "", err
			}
			if err = emit(raw); err != nil {
				return "", err
			}
		}
		if len(result.Hits) == 0 || page >= result.PageCount {
			break
		}
		page++
		err = emit(ingest.Raw{Checkpoint: true, Cursor: deltaCursorPrefix + strconv.Itoa(page)})
		if err != nil {
			return "", err
		}
	}
	return deltaCursorPrefix + "1", nil
}

func rawFromHit(payload []byte) (ingest.Raw, error) {
	var hit searchHit
	err := json.Unmarshal(payload, &hit)
	if err != nil {
		return ingest.Raw{}, fmt.Errorf("decode OFF search hit: %w", err)
	}
	return ingest.Raw{
		ExternalID: hit.Code,
		Payload:    append([]byte(nil), payload...),
		SourceURL:  productURLPrefix + url.PathEscape(hit.Code),
		Product: ingest.RawProduct{
			Name:       hit.name(),
			Brand:      strings.Join(hit.Brands, ", "),
			Barcode:    hit.Code,
			Quantity:   hit.Quantity,
			Kcal:       rawText(hit.Nutriments["energy-kcal_100g"]),
			Kilojoules: rawText(hit.Nutriments["energy-kj_100g"]),
			Protein:    rawText(hit.Nutriments["proteins_100g"]),
			Fat:        rawText(hit.Nutriments["fat_100g"]),
			Carbs:      rawText(hit.Nutriments["carbohydrates_100g"]),
		},
	}, nil
}

// name prefers Ukrainian, then the product's own name, then the other
// languages the catalogue's users read. product_name is often null.
func (h searchHit) name() string {
	for _, candidate := range []*string{h.NameUK, h.Name, h.NameRU, h.NameEN} {
		if candidate != nil && strings.TrimSpace(*candidate) != "" {
			return *candidate
		}
	}
	return ""
}

func rawText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	return strings.TrimSpace(string(raw))
}

func parseDeltaCursor(cursor string) (int, error) {
	if cursor == "" {
		return 1, nil
	}
	page, err := strconv.Atoi(strings.TrimPrefix(cursor, deltaCursorPrefix))
	if err != nil || page < 1 {
		return 0, fmt.Errorf("invalid OFF delta cursor %q", cursor)
	}
	return page, nil
}
