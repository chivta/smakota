// Package proe imports lookup-only products from proe.info product pages.
package proe

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/arvlas/calbot/internal/domain"
	"github.com/arvlas/calbot/internal/ingest"
	"golang.org/x/net/html"
	"golang.org/x/time/rate"
)

const (
	proeRPS    = rate.Limit(2)
	foodPrefix = "/uk/food/"

	// cursorDone is saved when a walk finishes so the next run starts over.
	cursorDone      = "0"
	cursorSeparator = ":"
)

// The visible text reads "Калорійність - 527 ккал, Білки - 6.2 г, ...". The
// table below it has no dashes, so these match only the per-100 g sentence.
var (
	kcalPattern    = regexp.MustCompile(`Калорійність\s*-\s*(\d+(?:[.,]\d+)?\s*ккал)`)
	proteinPattern = regexp.MustCompile(`Білки\s*-\s*(\d+(?:[.,]\d+)?\s*г)`)
	fatPattern     = regexp.MustCompile(`Жири\s*-\s*(\d+(?:[.,]\d+)?\s*г)`)
	carbsPattern   = regexp.MustCompile(`Вуглеводи\s*-\s*(\d+(?:[.,]\d+)?\s*г)`)
)

// Adapter enumerates product slugs from the paginated sitemap and scrapes each
// page.
type Adapter struct {
	http    *ingest.HTTPClient
	baseURL string
}

// New constructs a proe.info adapter.
func New(httpClient *ingest.HTTPClient, baseURL string) *Adapter {
	return &Adapter{http: httpClient, baseURL: strings.TrimRight(baseURL, "/")}
}

// Name returns the source identifier stored with catalogue rows.
func (a *Adapter) Name() string { return "proe" }

// proe.info publishes no licence, so treat its rows as unreproducible.
func (a *Adapter) Provenance() ingest.Provenance {
	return ingest.Provenance{
		LicenseClass: domain.LicenseLookupOnly,
		Confidence:   domain.ConfidenceScraped,
	}
}

type sitemapIndex struct {
	Sitemaps []struct {
		Loc string `xml:"loc"`
	} `xml:"sitemap"`
}

type sitemapPage struct {
	URLs []struct {
		Loc string `xml:"loc"`
	} `xml:"url"`
}

// Fetch stores the sitemap page and the index within it in its cursor.
func (a *Adapter) Fetch(ctx context.Context, cursor string, emit func(ingest.Raw) error) (string, error) {
	a.http.ResetCache()
	startPage, startIndex, err := parseCursor(cursor)
	if err != nil {
		return "", err
	}

	var index sitemapIndex
	err = a.getXML(ctx, a.baseURL+"/sitemap.xml", &index)
	if err != nil {
		return "", err
	}

	for page := startPage; page <= len(index.Sitemaps); page++ {
		var sitemap sitemapPage
		err = a.getXML(ctx, a.baseURL+"/sitemap.xml?page="+strconv.Itoa(page), &sitemap)
		if err != nil {
			return "", err
		}
		slugs := foodSlugs(sitemap)

		// Most sitemap pages carry no food at all, and each is several hundred
		// kilobytes, so record the page before walking it. Without this a
		// killed run re-downloads every food-free page on the next attempt.
		err = emit(ingest.Raw{Cursor: formatCursor(page, 0), Checkpoint: true})
		if err != nil {
			return "", err
		}

		for i := startIndex; i < len(slugs); i++ {
			raw, pageErr := a.fetchProduct(ctx, slugs[i])
			if pageErr != nil {
				return "", pageErr
			}
			raw.Cursor = formatCursor(page, i+1)
			err = emit(raw)
			if err != nil {
				return "", err
			}
		}
		startIndex = 0
	}
	return cursorDone, nil
}

func foodSlugs(sitemap sitemapPage) []string {
	var slugs []string
	for _, entry := range sitemap.URLs {
		parsed, err := url.Parse(strings.TrimSpace(entry.Loc))
		if err != nil || !strings.HasPrefix(parsed.Path, foodPrefix) {
			continue
		}
		slug := strings.TrimPrefix(parsed.Path, foodPrefix)
		if slug != "" && !strings.Contains(slug, "/") {
			slugs = append(slugs, slug)
		}
	}
	return slugs
}

func (a *Adapter) getXML(ctx context.Context, rawURL string, target any) error {
	response, err := a.http.Get(ctx, a.Name(), rawURL, proeRPS, true)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	err = xml.NewDecoder(response.Body).Decode(target)
	if err != nil {
		return fmt.Errorf("decode proe.info %s: %w", rawURL, err)
	}
	return nil
}

func (a *Adapter) fetchProduct(ctx context.Context, slug string) (ingest.Raw, error) {
	pageURL := a.baseURL + foodPrefix + url.PathEscape(slug)
	response, err := a.http.Get(ctx, a.Name(), pageURL, proeRPS, false)
	if err != nil {
		return ingest.Raw{}, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		return ingest.Raw{}, fmt.Errorf("read proe.info product %s: %w", slug, err)
	}
	product, err := extractProduct(payload)
	if err != nil {
		return ingest.Raw{}, fmt.Errorf("proe.info product %s: %w", slug, err)
	}
	return ingest.Raw{ExternalID: slug, Payload: payload, SourceURL: pageURL, Product: product}, nil
}

// extractProduct reads the name, barcode and per-100 g values out of a product
// page. A page without the nutrition sentence yields empty values, which
// normalization rejects with a reason.
func extractProduct(page []byte) (ingest.RawProduct, error) {
	document, err := html.Parse(strings.NewReader(string(page)))
	if err != nil {
		return ingest.RawProduct{}, fmt.Errorf("parse html: %w", err)
	}

	var product ingest.RawProduct
	var text strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			switch node.Data {
			case "script", "style":
				return
			case "h1":
				product.Name, product.Barcode = headingParts(node)
			}
		}
		if node.Type == html.TextNode {
			text.WriteString(node.Data)
			text.WriteByte(' ')
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(document)

	visible := text.String()
	product.Kcal = firstMatch(kcalPattern, visible)
	product.Protein = firstMatch(proteinPattern, visible)
	product.Fat = firstMatch(fatPattern, visible)
	product.Carbs = firstMatch(carbsPattern, visible)
	return product, nil
}

// headingParts splits the title heading into the product name and the barcode
// held in its nested span.
func headingParts(heading *html.Node) (name, barcode string) {
	var nameText, barcodeText strings.Builder
	var walk func(*html.Node, bool)
	walk = func(node *html.Node, inBarcode bool) {
		if node.Type == html.ElementNode && node.Data == "span" {
			inBarcode = inBarcode || hasClass(node, "barcode")
		}
		if node.Type == html.TextNode {
			if inBarcode {
				barcodeText.WriteString(node.Data)
			} else {
				nameText.WriteString(node.Data)
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child, inBarcode)
		}
	}
	walk(heading, false)
	return strings.TrimSpace(nameText.String()), strings.TrimSpace(barcodeText.String())
}

func hasClass(node *html.Node, class string) bool {
	for _, attribute := range node.Attr {
		if attribute.Key == "class" && slices.Contains(strings.Fields(attribute.Val), class) {
			return true
		}
	}
	return false
}

func firstMatch(pattern *regexp.Regexp, text string) string {
	match := pattern.FindStringSubmatch(text)
	if match == nil {
		return ""
	}
	return match[1]
}

func formatCursor(page, index int) string {
	return strconv.Itoa(page) + cursorSeparator + strconv.Itoa(index)
}

// parseCursor accepts the empty string and cursorDone as "start over" and
// returns a 1-based sitemap page.
func parseCursor(cursor string) (page, index int, err error) {
	if cursor == "" || cursor == cursorDone {
		return 1, 0, nil
	}
	pageText, indexText, ok := strings.Cut(cursor, cursorSeparator)
	if !ok {
		return 0, 0, fmt.Errorf("invalid proe.info cursor %q", cursor)
	}
	page, pageErr := strconv.Atoi(pageText)
	index, indexErr := strconv.Atoi(indexText)
	if pageErr != nil || indexErr != nil || page < 1 || index < 0 {
		return 0, 0, fmt.Errorf("invalid proe.info cursor %q", cursor)
	}
	return page, index, nil
}
