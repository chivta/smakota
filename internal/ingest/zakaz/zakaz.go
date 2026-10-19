// Package zakaz imports lookup-only products from the Zakaz.ua store API.
package zakaz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/arvlas/calbot/internal/domain"
	"github.com/arvlas/calbot/internal/ingest"
	"golang.org/x/time/rate"
)

const (
	baseURL = "https://stores-api.zakaz.ua/stores/"
	// language is sent because titles come back in English without it.
	language    = "uk"
	zakazRPS    = rate.Limit(2)
	httpTimeout = 45 * time.Second

	// cursorDone is saved when a walk finishes so the next run starts over.
	cursorDone      = "0"
	cursorSeparator = "|"
)

// Adapter walks each configured store's leaf categories page by page, one
// store per chain, and emits each ean once per run.
type Adapter struct {
	http     *ingest.HTTPClient
	storeIDs []string
}

// New constructs a Zakaz.ua adapter. httpClient must come from NewHTTPClient.
func New(httpClient *ingest.HTTPClient, storeIDs []string) *Adapter {
	return &Adapter{http: httpClient, storeIDs: storeIDs}
}

// NewHTTPClient returns an ingest client that sends Accept-Language on every
// request; ingest.HTTPClient has no per-request header hook.
func NewHTTPClient(userAgent string) *ingest.HTTPClient {
	client := &http.Client{
		Transport: languageTransport{next: http.DefaultTransport},
		Timeout:   httpTimeout,
	}
	return ingest.NewHTTPClient(client, userAgent)
}

type languageTransport struct {
	next http.RoundTripper
}

func (t languageTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.Header.Set("Accept-Language", language)
	return t.next.RoundTrip(request)
}

// Name returns the source identifier stored with catalogue rows.
func (a *Adapter) Name() string { return "zakaz" }

// zakaz.ua's terms forbid reuse without consent, so its rows stay lookup-only.
func (a *Adapter) Provenance() ingest.Provenance {
	return ingest.Provenance{
		LicenseClass: domain.LicenseLookupOnly,
		Confidence:   domain.ConfidenceScraped,
	}
}

type category struct {
	ID           string     `json:"id"`
	IsCollection bool       `json:"is_collection"`
	Children     []category `json:"children"`
}

type product struct {
	Title    string  `json:"title"`
	EAN      string  `json:"ean"`
	Weight   float64 `json:"weight"`
	Producer struct {
		Trademark string `json:"trademark"`
	} `json:"producer"`
	Nutrition map[string]string `json:"nutrition_facts"`
}

// Fetch stores store, category and page in its cursor. Products without a
// nutrition panel are skipped, not rejected.
func (a *Adapter) Fetch(ctx context.Context, cursor string, emit func(ingest.Raw) error) (string, error) {
	a.http.ResetCache()
	position, err := parseCursor(cursor)
	if err != nil {
		return "", err
	}

	// The same product, and the chain-prefixed counter items, recur across
	// categories and stores; a resumed run only dedupes from its resume point,
	// staging's unique key covers the rest.
	seen := map[string]struct{}{}
	for _, storeID := range a.storeIDs {
		if position.store != "" && storeID != position.store {
			continue
		}
		categoryIDs, categoriesErr := a.leafCategories(ctx, storeID)
		if categoriesErr != nil {
			return "", categoriesErr
		}
		// A category missing from the tree means the store changed it; start
		// the store over rather than skipping it.
		start, page := 0, 1
		if i := slices.Index(categoryIDs, position.category); i >= 0 {
			start, page = i, position.page
		}
		position = cursorPosition{}
		for _, categoryID := range categoryIDs[start:] {
			err = a.fetchCategory(ctx, storeID, categoryID, page, seen, emit)
			if err != nil {
				return "", err
			}
			page = 1
		}
	}
	return cursorDone, nil
}

func (a *Adapter) fetchCategory(ctx context.Context, storeID, categoryID string, page int, seen map[string]struct{}, emit func(ingest.Raw) error) error {
	for {
		productsURL := fmt.Sprintf("%s%s/categories/%s/products/?page=%d", baseURL, url.PathEscape(storeID), url.PathEscape(categoryID), page)
		response, err := a.http.Get(ctx, a.Name(), productsURL, zakazRPS, true)
		if err != nil {
			return err
		}
		var result struct {
			Results []json.RawMessage `json:"results"`
		}
		decodeErr := json.NewDecoder(response.Body).Decode(&result)
		response.Body.Close()
		if decodeErr != nil {
			return fmt.Errorf("decode Zakaz category %s page %d: %w", categoryID, page, decodeErr)
		}
		if len(result.Results) == 0 {
			return nil
		}

		for _, item := range result.Results {
			raw, ok, err := toRaw(storeID, categoryID, page, item)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			if _, duplicate := seen[raw.ExternalID]; duplicate {
				continue
			}
			seen[raw.ExternalID] = struct{}{}
			err = emit(raw)
			if err != nil {
				return err
			}
		}
		page++
	}
}

// toRaw maps one API product to a staged row. ok is false for products without
// a nutrition panel or an ean, which are skipped, not rejected.
func toRaw(storeID, categoryID string, page int, item json.RawMessage) (raw ingest.Raw, ok bool, err error) {
	var p product
	err = json.Unmarshal(item, &p)
	if err != nil {
		return ingest.Raw{}, false, fmt.Errorf("decode Zakaz product in category %s: %w", categoryID, err)
	}
	if p.Nutrition["ingredient_energy"] == "" || p.EAN == "" {
		return ingest.Raw{}, false, nil
	}
	raw = ingest.Raw{
		ExternalID: p.EAN,
		Payload:    item,
		SourceURL:  fmt.Sprintf("%s%s/products/search/?q=%s", baseURL, url.PathEscape(storeID), url.QueryEscape(p.EAN)),
		Cursor:     formatCursor(cursorPosition{store: storeID, category: categoryID, page: page}),
		Product: ingest.RawProduct{
			Name:    p.Title,
			Brand:   p.Producer.Trademark,
			Barcode: p.EAN,
			Kcal:    p.Nutrition["ingredient_energy"],
			Protein: p.Nutrition["ingredient_protein"],
			Fat:     p.Nutrition["ingredient_fat"],
			Carbs:   p.Nutrition["ingredient_carbohydrates"],
		},
	}
	if p.Weight > 0 {
		raw.Product.Quantity = strconv.FormatFloat(p.Weight, 'f', -1, 64) + " g"
	}
	return raw, true, nil
}

// leafCategories flattens the category tree to its leaves, skipping promo
// collections that only repeat products listed elsewhere.
func (a *Adapter) leafCategories(ctx context.Context, storeID string) ([]string, error) {
	response, err := a.http.Get(ctx, a.Name(), baseURL+url.PathEscape(storeID)+"/categories/", zakazRPS, true)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var tree []category
	err = json.NewDecoder(response.Body).Decode(&tree)
	if err != nil {
		return nil, fmt.Errorf("decode Zakaz store %s categories: %w", storeID, err)
	}
	return flattenLeaves(tree), nil
}

// flattenLeaves lists the leaf ids of a category tree, skipping collections.
func flattenLeaves(tree []category) []string {
	var ids []string
	var walk func([]category)
	walk = func(nodes []category) {
		for _, node := range nodes {
			switch {
			case node.IsCollection:
			case len(node.Children) == 0:
				ids = append(ids, node.ID)
			default:
				walk(node.Children)
			}
		}
	}
	walk(tree)
	return ids
}

type cursorPosition struct {
	store    string
	category string
	page     int
}

func formatCursor(p cursorPosition) string {
	return strings.Join([]string{p.store, p.category, strconv.Itoa(p.page)}, cursorSeparator)
}

// parseCursor accepts the empty string and cursorDone as "start over".
func parseCursor(cursor string) (cursorPosition, error) {
	if cursor == "" || cursor == cursorDone {
		return cursorPosition{}, nil
	}
	parts := strings.Split(cursor, cursorSeparator)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" {
		return cursorPosition{}, fmt.Errorf("invalid Zakaz cursor %q", cursor)
	}
	page, err := strconv.Atoi(parts[2])
	if err != nil || page < 1 {
		return cursorPosition{}, fmt.Errorf("invalid Zakaz cursor %q", cursor)
	}
	return cursorPosition{store: parts[0], category: parts[1], page: page}, nil
}
