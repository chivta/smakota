// Package off imports Open Food Facts products.
package off

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/arvlas/calbot/internal/domain"
	"github.com/arvlas/calbot/internal/ingest"
	"golang.org/x/time/rate"
)

const (
	deltaEndpoint = "https://world.openfoodfacts.org/api/v2/search"
	deltaPageSize = 100
	offRate       = rate.Limit(2)
)

// Adapter supports both the daily API delta and the full JSONL dump.
type Adapter struct {
	http    *ingest.HTTPClient
	dumpURL string
}

// New constructs an Open Food Facts adapter.
func New(httpClient *ingest.HTTPClient, dumpURL string) *Adapter {
	return &Adapter{http: httpClient, dumpURL: dumpURL}
}

// Name returns the source identifier stored with catalogue rows.
func (a *Adapter) Name() string { return "off" }

// Open Food Facts is ODbL, so its rows may be redistributed with attribution.
func (a *Adapter) Provenance() ingest.Provenance {
	return ingest.Provenance{
		LicenseClass: domain.LicenseRedistributable,
		Confidence:   domain.ConfidenceOpenDB,
	}
}

// Fetch chooses the full or delta mode from the persisted cursor prefix.
func (a *Adapter) Fetch(ctx context.Context, cursor string, emit func(ingest.Raw) error) (string, error) {
	a.http.ResetCache()
	if strings.HasPrefix(cursor, "full:") {
		return a.fetchFull(ctx, cursor, emit)
	}
	return a.fetchDelta(ctx, cursor, emit)
}

type product struct {
	Code          string                     `json:"code"`
	NameUK        string                     `json:"product_name_uk"`
	Name          string                     `json:"product_name"`
	Brands        string                     `json:"brands"`
	Quantity      string                     `json:"quantity"`
	CountriesTags []string                   `json:"countries_tags"`
	LastModified  json.Number                `json:"last_modified_t"`
	Nutriments    map[string]json.RawMessage `json:"nutriments"`
}

type searchResponse struct {
	Products  []json.RawMessage `json:"products"`
	Page      int               `json:"page"`
	PageCount int               `json:"page_count"`
	Count     int               `json:"count"`
}

func (a *Adapter) fetchDelta(ctx context.Context, cursor string, emit func(ingest.Raw) error) (string, error) {
	since, page, err := parseDeltaCursor(cursor)
	if err != nil {
		return "", err
	}
	runStarted := time.Now().UTC().Unix()
	for {
		query := url.Values{}
		query.Set("countries_tags_en", "ukraine")
		query.Set("fields", "code,product_name_uk,product_name,brands,quantity,countries_tags,last_modified_t,nutriments")
		query.Set("page", strconv.Itoa(page))
		query.Set("page_size", strconv.Itoa(deltaPageSize))
		query.Set("sort_by", "last_modified_t")
		endpoint := deltaEndpoint + "?" + query.Encode()
		response, requestErr := a.http.Get(ctx, a.Name(), endpoint, offRate, true)
		if requestErr != nil {
			return "", requestErr
		}
		var result searchResponse
		decoder := json.NewDecoder(response.Body)
		decoder.UseNumber()
		decodeErr := decoder.Decode(&result)
		response.Body.Close()
		if decodeErr != nil {
			return "", fmt.Errorf("decode OFF delta page %d: %w", page, decodeErr)
		}

		foundOlder := false
		for _, payload := range result.Products {
			raw, modified, productErr := rawProduct(payload, endpoint)
			if productErr != nil {
				return "", productErr
			}
			if modified > 0 && modified <= since {
				foundOlder = true
				continue
			}
			raw.Cursor = fmt.Sprintf("delta:%d:%d", since, page)
			if emitErr := emit(raw); emitErr != nil {
				return "", emitErr
			}
		}
		if foundOlder || len(result.Products) == 0 || (result.PageCount > 0 && page >= result.PageCount) || (result.Count > 0 && page*deltaPageSize >= result.Count) {
			break
		}
		page++
	}
	return fmt.Sprintf("delta:%d:1", runStarted), nil
}

func (a *Adapter) fetchFull(ctx context.Context, cursor string, emit func(ingest.Raw) error) (string, error) {
	lineToSkip, err := parseFullCursor(cursor)
	if err != nil {
		return "", err
	}
	response, err := a.http.Get(ctx, a.Name(), a.dumpURL, offRate, false)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	gzipReader, err := gzip.NewReader(response.Body)
	if err != nil {
		return "", fmt.Errorf("open OFF gzip dump: %w", err)
	}
	defer gzipReader.Close()

	scanner := bufio.NewScanner(gzipReader)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		if line < lineToSkip {
			continue
		}
		payload := append([]byte(nil), scanner.Bytes()...)
		var candidate product
		decoder := json.NewDecoder(strings.NewReader(string(payload)))
		decoder.UseNumber()
		if err = decoder.Decode(&candidate); err != nil {
			return "", fmt.Errorf("decode OFF dump line %d: %w", line, err)
		}
		if !isUkrainian(candidate) {
			continue
		}
		raw, _, rawErr := rawProduct(payload, a.dumpURL)
		if rawErr != nil {
			return "", rawErr
		}
		raw.Cursor = fmt.Sprintf("full:%d", line)
		if err = emit(raw); err != nil {
			return "", err
		}
	}
	if err = scanner.Err(); err != nil && err != io.EOF {
		return "", fmt.Errorf("stream OFF dump: %w", err)
	}
	return "full:1", nil
}

func rawProduct(payload []byte, sourceURL string) (ingest.Raw, int64, error) {
	var candidate product
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.UseNumber()
	err := decoder.Decode(&candidate)
	if err != nil {
		return ingest.Raw{}, 0, fmt.Errorf("decode OFF product: %w", err)
	}
	name := candidate.NameUK
	if strings.TrimSpace(name) == "" {
		name = candidate.Name
	}
	modified, _ := candidate.LastModified.Int64()
	productURL := "https://world.openfoodfacts.org/product/" + url.PathEscape(candidate.Code)
	if candidate.Code == "" {
		productURL = sourceURL
	}
	return ingest.Raw{
		ExternalID: candidate.Code,
		Payload:    append([]byte(nil), payload...),
		SourceURL:  productURL,
		Product: ingest.RawProduct{
			Name:       name,
			Brand:      candidate.Brands,
			Barcode:    candidate.Code,
			Quantity:   candidate.Quantity,
			Kcal:       rawText(candidate.Nutriments["energy-kcal_100g"]),
			Kilojoules: rawText(candidate.Nutriments["energy-kj_100g"]),
			Protein:    rawText(candidate.Nutriments["proteins_100g"]),
			Fat:        rawText(candidate.Nutriments["fat_100g"]),
			Carbs:      rawText(candidate.Nutriments["carbohydrates_100g"]),
		},
	}, modified, nil
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

func isUkrainian(candidate product) bool {
	if strings.HasPrefix(candidate.Code, "482") {
		return true
	}
	for _, tag := range candidate.CountriesTags {
		tag = strings.ToLower(tag)
		if tag == "en:ukraine" || tag == "ukraine" || tag == "uk:ukraine" {
			return true
		}
	}
	return false
}

func parseDeltaCursor(cursor string) (int64, int, error) {
	if cursor == "" {
		return time.Now().UTC().Add(-24 * time.Hour).Unix(), 1, nil
	}
	parts := strings.Split(cursor, ":")
	if len(parts) != 3 || parts[0] != "delta" {
		return 0, 0, fmt.Errorf("invalid OFF delta cursor %q", cursor)
	}
	since, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("parse OFF delta timestamp: %w", err)
	}
	page, err := strconv.Atoi(parts[2])
	if err != nil || page < 1 {
		return 0, 0, fmt.Errorf("invalid OFF delta page %q", parts[2])
	}
	return since, page, nil
}

func parseFullCursor(cursor string) (int, error) {
	lineText := strings.TrimPrefix(cursor, "full:")
	line, err := strconv.Atoi(lineText)
	if err != nil || line < 1 {
		return 0, fmt.Errorf("invalid OFF full cursor %q", cursor)
	}
	return line, nil
}
