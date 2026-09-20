// Package silpo imports lookup-only products from the Silpo commerce API.
package silpo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/arvlas/calbot/internal/ingest"
	"golang.org/x/time/rate"
)

const (
	baseURL  = "https://sf-ecom-api.silpo.ua/v1/uk/branches/"
	pageSize = 100
	silpoRPS = rate.Limit(2)
)

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

type searchResponse struct {
	Items    []searchItem `json:"items"`
	Products []searchItem `json:"products"`
	Total    int          `json:"total"`
	Count    int          `json:"count"`
}

type searchItem struct {
	ExternalProductID json.RawMessage `json:"externalProductId"`
}

// Fetch stores the numeric search offset in its cursor. Detail lookups always
// use externalProductId and never the UUID id returned beside it.
func (a *Adapter) Fetch(ctx context.Context, cursor string, emit func(ingest.Raw) error) (string, error) {
	a.http.ResetCache()
	offset, err := parseCursor(cursor)
	if err != nil {
		return "", err
	}
	for {
		searchURL := a.productsURL(offset)
		response, requestErr := a.http.Get(ctx, a.Name(), searchURL, silpoRPS, true)
		if requestErr != nil {
			return "", requestErr
		}
		var result searchResponse
		decodeErr := json.NewDecoder(response.Body).Decode(&result)
		response.Body.Close()
		if decodeErr != nil {
			return "", fmt.Errorf("decode Silpo search offset %d: %w", offset, decodeErr)
		}
		items := result.Items
		if len(items) == 0 {
			items = result.Products
		}
		if len(items) == 0 {
			return "0", nil
		}

		for _, item := range items {
			externalID := rawScalar(item.ExternalProductID)
			if externalID == "" {
				return "", fmt.Errorf("Silpo search item is missing externalProductId")
			}
			raw, detailErr := a.fetchDetail(ctx, externalID)
			if detailErr != nil {
				return "", detailErr
			}
			raw.Cursor = strconv.Itoa(offset)
			if emitErr := emit(raw); emitErr != nil {
				return "", emitErr
			}
		}

		offset += len(items)
		total := result.Total
		if total == 0 {
			total = result.Count
		}
		if len(items) < pageSize || (total > 0 && offset >= total) {
			return "0", nil
		}
	}
}

func (a *Adapter) productsURL(offset int) string {
	query := url.Values{}
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
	nutrients, err := nutrientAttributes(product)
	if err != nil {
		return ingest.Raw{}, fmt.Errorf("Silpo product %s: %w", externalID, err)
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
			Kcal:     firstMapString(nutrients, "calorie", "calories", "energy"),
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
				key := firstString(attribute, "key", "code", "slug", "name")
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

func parseCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	offset, err := strconv.Atoi(cursor)
	if err != nil || offset < 0 {
		return 0, fmt.Errorf("invalid Silpo cursor %q", cursor)
	}
	return offset, nil
}
