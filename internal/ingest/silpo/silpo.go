// Package silpo imports lookup-only products from the Silpo commerce API.
package silpo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/arvlas/calbot/internal/domain"
	"github.com/arvlas/calbot/internal/ingest"
	"github.com/rs/zerolog/log"
	"golang.org/x/time/rate"
)

const (
	baseURL  = "https://sf-ecom-api.silpo.ua/v1/uk/branches/"
	pageSize = 100
	silpoRPS = rate.Limit(2)

	// cursorDone is saved when a walk finishes so the next run starts over.
	cursorDone      = "0"
	cursorSeparator = ":"
)

// foodTopLevelSlugs are the top-level categories worth walking. A leaf is
// food when its top-level ancestor is listed here; everything else (cosmetics,
// household, pharmacy, alcohol, pet food, promo collections that only repeat
// other categories) never costs a request. Лавка Традицій and Власні марки are
// mixed, so their non-food leaves are skipped later for lacking nutrition.
var foodTopLevelSlugs = map[string]bool{
	"frukty-ovochi-4788": true,
	"m-iaso-4411":        true,
	"ryba-4430":          true,
	"kovbasni-vyroby-i-m-iasni-delikatesy-4731": true,
	"syry-1468":                        true,
	"khlib-ta-vypichka-5121":           true,
	"gotovi-stravy-i-kulinariia-4761":  true,
	"molochni-produkty-ta-iaitsia-234": true,
	"vlasni-marky-5202":                true,
	"lavka-tradytsii-4487":             true,
	"zdorove-kharchuvannia-4864":       true,
	"bakaliia-i-konservy-4870":         true,
	"sousy-i-spetsii-4938":             true,
	"solodoshchi-498":                  true,
	"sneky-ta-chypsy-5016":             true,
	"kava-chai-359":                    true,
	"napoi-52":                         true,
	"zamorozhena-produktsiia-264":      true,
}

// errNoNutrition marks a product without a usable energy value.
var errNoNutrition = errors.New("no nutrition")

// errEmit wraps an error from emit so a store failure is not mistaken for a
// skippable fetch failure.
type errEmit struct{ err error }

func (e errEmit) Error() string { return e.err.Error() }
func (e errEmit) Unwrap() error { return e.err }

// walkStats counts what one run skipped.
type walkStats struct {
	seen             map[string]struct{}
	duplicates       int
	noNutrition      int
	failedProducts   int
	failedCategories int
}

// Adapter pages product search and fetches each product's detail record.
type Adapter struct {
	http     *ingest.HTTPClient
	branchID string
}

// New constructs a Silpo adapter for one configured branch.
func New(httpClient *ingest.HTTPClient, branchID string) *Adapter {
	return &Adapter{http: httpClient, branchID: branchID}
}

// Name returns the source identifier stored with catalogue rows.
func (a *Adapter) Name() string { return "silpo" }

// Сільпо's terms forbid reproduction, so its rows are rendered one at a time
// and never exported.
func (a *Adapter) Provenance() ingest.Provenance {
	return ingest.Provenance{
		LicenseClass: domain.LicenseLookupOnly,
		Confidence:   domain.ConfidenceScraped,
	}
}

type searchResponse struct {
	Items    []searchItem `json:"items"`
	Products []searchItem `json:"products"`
	Total    int          `json:"total"`
	Count    int          `json:"count"`
}

type searchItem struct {
	ExternalProductID json.RawMessage `json:"externalProductId"`
}

type category struct {
	ID       string `json:"id"`
	Slug     string `json:"slug"`
	ParentID string `json:"parentId"`
}

type categoriesResponse struct {
	Items []category `json:"items"`
}

// Fetch walks the leaf categories because the products endpoint rejects a
// request without a filter. The cursor holds the category ID and the offset
// inside it. Detail lookups always use externalProductId and never the UUID id
// returned beside it.
func (a *Adapter) Fetch(ctx context.Context, cursor string, emit func(ingest.Raw) error) (string, error) {
	a.http.ResetCache()
	categorySlug, offset, err := parseCursor(cursor)
	if err != nil {
		return "", err
	}
	categories, err := a.categories(ctx)
	if err != nil {
		return "", err
	}
	categorySlugs := foodLeafCategories(categories)
	start := 0
	if categorySlug != "" {
		start = slices.Index(categorySlugs, categorySlug)
		if start < 0 {
			start, offset = 0, 0
		}
	}

	stats := &walkStats{seen: make(map[string]struct{})}
	for _, slug := range categorySlugs[start:] {
		err = a.fetchCategory(ctx, slug, offset, stats, emit)
		var emitErr errEmit
		switch {
		case err == nil:
		case ctx.Err() != nil:
			return "", ctx.Err()
		case errors.As(err, &emitErr):
			return "", emitErr.err
		default:
			stats.failedCategories++
			log.Warn().Err(err).Str("category", slug).Msg("silpo category skipped")
		}
		offset = 0
	}
	log.Info().
		Int("categories", len(categorySlugs)).
		Int("duplicates", stats.duplicates).
		Int("no_nutrition", stats.noNutrition).
		Int("failed_products", stats.failedProducts).
		Int("failed_categories", stats.failedCategories).
		Msg("silpo walk finished")
	return cursorDone, nil
}

func (a *Adapter) fetchCategory(ctx context.Context, categoryID string, offset int, stats *walkStats, emit func(ingest.Raw) error) error {
	for {
		response, err := a.http.Get(ctx, a.Name(), a.productsURL(categoryID, offset), silpoRPS, true)
		if err != nil {
			return err
		}
		var result searchResponse
		decodeErr := json.NewDecoder(response.Body).Decode(&result)
		response.Body.Close()
		if decodeErr != nil {
			return fmt.Errorf("decode Silpo category %s offset %d: %w", categoryID, offset, decodeErr)
		}
		items := result.Items
		if len(items) == 0 {
			items = result.Products
		}
		if len(items) == 0 {
			return nil
		}

		for i, item := range items {
			externalID := rawScalar(item.ExternalProductID)
			if externalID == "" {
				log.Warn().Str("category", categoryID).Int("offset", offset+i).Msg("silpo search item missing externalProductId")
				stats.failedProducts++
				continue
			}
			if _, dup := stats.seen[externalID]; dup {
				stats.duplicates++
				continue
			}
			stats.seen[externalID] = struct{}{}
			raw, detailErr := a.fetchDetail(ctx, externalID)
			switch {
			case detailErr == nil:
			case ctx.Err() != nil:
				return ctx.Err()
			case errors.Is(detailErr, errNoNutrition):
				stats.noNutrition++
				continue
			default:
				stats.failedProducts++
				log.Warn().Err(detailErr).Str("external_id", externalID).Msg("silpo product skipped")
				continue
			}
			raw.Cursor = formatCursor(categoryID, offset+i+1)
			if emitErr := emit(raw); emitErr != nil {
				return errEmit{emitErr}
			}
		}

		offset += len(items)
		total := result.Total
		if total == 0 {
			total = result.Count
		}
		if len(items) < pageSize || (total > 0 && offset >= total) {
			return nil
		}
	}
}

func (a *Adapter) categories(ctx context.Context) ([]category, error) {
	response, err := a.http.Get(ctx, a.Name(), baseURL+url.PathEscape(a.branchID)+"/categories", silpoRPS, true)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read Silpo categories: %w", err)
	}
	var categories []category
	if json.Unmarshal(body, &categories) != nil {
		var wrapped categoriesResponse
		err = json.Unmarshal(body, &wrapped)
		if err != nil {
			return nil, fmt.Errorf("decode Silpo categories: %w", err)
		}
		categories = wrapped.Items
	}
	return categories, nil
}

// foodLeafCategories returns the slugs of leaf categories whose top-level
// ancestor is a food category. The products filter already includes children,
// so walking parents too would fetch every product several times.
func foodLeafCategories(categories []category) []string {
	byID := make(map[string]category, len(categories))
	parents := make(map[string]bool, len(categories))
	for _, c := range categories {
		byID[c.ID] = c
		parents[c.ParentID] = true
	}
	var slugs []string
	for _, c := range categories {
		if c.Slug == "" || parents[c.ID] {
			continue
		}
		top := c
		// The step bound guards against a parent cycle in the data.
		for steps := 0; top.ParentID != "" && steps < len(categories); steps++ {
			parent, ok := byID[top.ParentID]
			if !ok {
				break
			}
			top = parent
		}
		if foodTopLevelSlugs[top.Slug] {
			slugs = append(slugs, c.Slug)
		}
	}
	return slugs
}

func (a *Adapter) productsURL(categoryID string, offset int) string {
	query := url.Values{}
	query.Set("category", categoryID)
	query.Set("limit", strconv.Itoa(pageSize))
	query.Set("offset", strconv.Itoa(offset))
	query.Set("deliveryType", "DeliveryHome")
	query.Set("includeChildCategories", "true")
	query.Set("sortBy", "popularity")
	query.Set("sortDirection", "desc")
	query.Set("inStock", "false")
	return baseURL + url.PathEscape(a.branchID) + "/products?" + query.Encode()
}

func (a *Adapter) fetchDetail(ctx context.Context, externalID string) (ingest.Raw, error) {
	detailURL := baseURL + url.PathEscape(a.branchID) + "/products/" + url.PathEscape(externalID)
	response, err := a.http.Get(ctx, a.Name(), detailURL, silpoRPS, true)
	if err != nil {
		return ingest.Raw{}, err
	}
	payload, err := readJSON(response)
	if err != nil {
		return ingest.Raw{}, fmt.Errorf("read Silpo product %s: %w", externalID, err)
	}
	var detail map[string]any
	err = json.Unmarshal(payload, &detail)
	if err != nil {
		return ingest.Raw{}, fmt.Errorf("decode Silpo product %s: %w", externalID, err)
	}
	product := productObject(detail)
	nutrients, nutrientErr := nutrientAttributes(product)
	if nutrientErr != nil {
		return ingest.Raw{}, errNoNutrition
	}
	kcal := firstMapString(nutrients, "calorie", "calories", "energy")
	if kcal == "" {
		return ingest.Raw{}, errNoNutrition
	}
	return ingest.Raw{
		ExternalID: externalID,
		Payload:    payload,
		SourceURL:  detailURL,
		Product: ingest.RawProduct{
			Name:     firstString(product, "title", "name", "productName"),
			Brand:    nestedString(product["brand"]),
			Barcode:  firstString(product, "barcode", "ean"),
			Quantity: firstString(product, "quantity", "weight", "displayRatio"),
			Kcal:     kcal,
			Protein:  firstMapString(nutrients, "proteins", "protein"),
			Fat:      firstMapString(nutrients, "fats", "fat"),
			Carbs:    firstMapString(nutrients, "carbohydrates", "carbs"),
		},
	}, nil
}

func productObject(detail map[string]any) map[string]any {
	for _, key := range []string{"product", "data"} {
		if product, ok := detail[key].(map[string]any); ok {
			return product
		}
	}
	return detail
}

func nutrientAttributes(detail map[string]any) (map[string]string, error) {
	groups, ok := detail["attributeGroups"].([]any)
	if !ok {
		return nil, fmt.Errorf("missing attributeGroups")
	}
	for _, value := range groups {
		group, groupOK := value.(map[string]any)
		if !groupOK || firstString(group, "key", "code", "slug") != "nutrient" {
			continue
		}
		result := make(map[string]string)
		for _, field := range []string{"attributes", "items", "values"} {
			attributes, attributesOK := group[field].([]any)
			if !attributesOK {
				continue
			}
			for _, attributeValue := range attributes {
				attribute, attributeOK := attributeValue.(map[string]any)
				if !attributeOK {
					continue
				}
				// The live shape is {attribute: {key}, value: {title}}.
				key := nestedKey(attribute["attribute"])
				if key == "" {
					key = firstString(attribute, "key", "code", "slug", "name")
				}
				result[key] = firstString(attribute, "value", "text", "displayValue")
			}
		}
		for key, value := range group {
			if text := nestedString(value); text != "" && key != "key" {
				result[key] = text
			}
		}
		return result, nil
	}
	return nil, fmt.Errorf("missing nutrient attribute group")
}

func nestedKey(value any) string {
	if m, ok := value.(map[string]any); ok {
		return firstString(m, "key", "id")
	}
	return ""
}

func firstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := nestedString(values[key]); value != "" {
			return value
		}
	}
	return ""
}

func firstMapString(values map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(values[key]); value != "" {
			return value
		}
	}
	return ""
}

func nestedString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case json.Number:
		return typed.String()
	case map[string]any:
		return firstString(typed, "name", "title", "value", "text")
	default:
		return ""
	}
}

func rawScalar(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	return strings.Trim(string(raw), `"`)
}

func readJSON(response *http.Response) ([]byte, error) {
	defer response.Body.Close()
	return io.ReadAll(response.Body)
}

func formatCursor(categoryID string, offset int) string {
	return categoryID + cursorSeparator + strconv.Itoa(offset)
}

// parseCursor accepts the empty string and cursorDone as "start from the
// first category".
func parseCursor(cursor string) (string, int, error) {
	if cursor == "" || cursor == cursorDone {
		return "", 0, nil
	}
	categoryID, offsetText, ok := strings.Cut(cursor, cursorSeparator)
	if !ok || categoryID == "" {
		return "", 0, fmt.Errorf("invalid Silpo cursor %q", cursor)
	}
	offset, err := strconv.Atoi(offsetText)
	if err != nil || offset < 0 {
		return "", 0, fmt.Errorf("invalid Silpo cursor %q", cursor)
	}
	return categoryID, offset, nil
}
