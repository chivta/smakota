package off

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"strconv"
	"strings"

	"github.com/arvlas/calbot/internal/ingest"
)

const (
	// checkpointEvery bounds the work lost to a kill while the dump is mostly
	// rows without a product, which emit nothing.
	checkpointEvery = 100_000

	// maxLineBytes covers the longest row in the dump with room to spare.
	maxLineBytes = 16 * 1024 * 1024
	scanBufBytes = 1024 * 1024

	tab = '\t'
)

// Dump column names.
const (
	colCode      = "code"
	colName      = "product_name"
	colBrands    = "brands"
	colQuantity  = "quantity"
	colCountries = "countries_tags"
	colKcal      = "energy-kcal_100g"
	colProtein   = "proteins_100g"
	colFat       = "fat_100g"
	colCarbs     = "carbohydrates_100g"
)

// columns holds the position of each needed column in the header.
type columns struct {
	code, name, brands, quantity, countries, kcal, protein, fat, carbs int
}

func parseHeader(header []byte) (columns, error) {
	positions := map[string]int{}
	for i, name := range bytes.Split(header, []byte{tab}) {
		positions[string(name)] = i
	}
	var missing []string
	find := func(name string) int {
		position, ok := positions[name]
		if !ok {
			missing = append(missing, name)
		}
		return position
	}
	cols := columns{
		code: find(colCode), name: find(colName), brands: find(colBrands),
		quantity: find(colQuantity), countries: find(colCountries),
		kcal: find(colKcal), protein: find(colProtein), fat: find(colFat),
		carbs: find(colCarbs),
	}
	if len(missing) > 0 {
		return columns{}, fmt.Errorf("OFF dump header lacks columns %s", strings.Join(missing, ", "))
	}
	return cols, nil
}

// rowPayload is what staging keeps of a dump row; the other 200 columns are
// not worth a jsonb value per product.
type rowPayload struct {
	Code      string `json:"code"`
	Name      string `json:"product_name"`
	Brands    string `json:"brands"`
	Quantity  string `json:"quantity"`
	Countries string `json:"countries_tags"`
	Kcal      string `json:"energy-kcal_100g"`
	Protein   string `json:"proteins_100g"`
	Fat       string `json:"fat_100g"`
	Carbs     string `json:"carbohydrates_100g"`
}

// rawFromRow maps one dump row. It reports false for a row without a barcode,
// a product name or an energy value: such a row has nothing to catalogue, and
// is not a rejection.
func rawFromRow(fields [][]byte, cols columns) (ingest.Raw, bool, error) {
	field := func(i int) string {
		if i >= len(fields) {
			return ""
		}
		return strings.TrimSpace(string(fields[i]))
	}
	row := rowPayload{
		Code:      field(cols.code),
		Name:      html.UnescapeString(field(cols.name)),
		Brands:    html.UnescapeString(field(cols.brands)),
		Quantity:  field(cols.quantity),
		Countries: field(cols.countries),
		Kcal:      field(cols.kcal),
		Protein:   field(cols.protein),
		Fat:       field(cols.fat),
		Carbs:     field(cols.carbs),
	}
	if row.Code == "" || row.Name == "" || row.Kcal == "" {
		return ingest.Raw{}, false, nil
	}
	payload, err := json.Marshal(row)
	if err != nil {
		return ingest.Raw{}, false, fmt.Errorf("marshal OFF row %s: %w", row.Code, err)
	}
	return ingest.Raw{
		ExternalID: row.Code,
		Payload:    payload,
		SourceURL:  productURLPrefix + url.PathEscape(row.Code),
		Product: ingest.RawProduct{
			Name:     row.Name,
			Brand:    row.Brands,
			Barcode:  row.Code,
			Quantity: row.Quantity,
			Kcal:     row.Kcal,
			Protein:  row.Protein,
			Fat:      row.Fat,
			Carbs:    row.Carbs,
		},
	}, true, nil
}

// splitTabs splits line into dst, reusing its storage.
func splitTabs(dst [][]byte, line []byte) [][]byte {
	dst = dst[:0]
	for {
		i := bytes.IndexByte(line, tab)
		if i < 0 {
			return append(dst, line)
		}
		dst = append(dst, line[:i])
		line = line[i+1:]
	}
}

// fetchFull streams and gunzips the dump one line at a time. Progress is the
// number of lines done; a resumed run still downloads from the start, because
// a gzip stream cannot be entered midway, but skips the lines already staged.
func (a *Adapter) fetchFull(ctx context.Context, cursor string, emit func(ingest.Raw) error) (string, error) {
	doneLines, err := parseFullCursor(cursor)
	if err != nil {
		return "", err
	}
	response, err := a.http.Get(ctx, sourceName, a.dumpURL, offRate, false)
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
	scanner.Buffer(make([]byte, scanBufBytes), maxLineBytes)
	if !scanner.Scan() {
		return "", fmt.Errorf("read OFF dump header: %w", scanner.Err())
	}
	cols, err := parseHeader(scanner.Bytes())
	if err != nil {
		return "", err
	}

	line := 1
	var fields [][]byte
	for scanner.Scan() {
		line++
		if line <= doneLines {
			continue
		}
		fields = splitTabs(fields, scanner.Bytes())
		raw, keep, err := rawFromRow(fields, cols)
		if err != nil {
			return "", err
		}
		if keep {
			raw.Cursor = fullCursorPrefix + strconv.Itoa(line)
			err = emit(raw)
		} else if line%checkpointEvery == 0 {
			err = emit(ingest.Raw{Checkpoint: true, Cursor: fullCursorPrefix + strconv.Itoa(line)})
		}
		if err != nil {
			return "", err
		}
	}
	if err = scanner.Err(); err != nil {
		return "", fmt.Errorf("stream OFF dump line %d: %w", line, err)
	}
	return fullCursorPrefix + "0", nil
}

func parseFullCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	line, err := strconv.Atoi(strings.TrimPrefix(cursor, fullCursorPrefix))
	if err != nil || line < 0 {
		return 0, fmt.Errorf("invalid OFF full cursor %q", cursor)
	}
	return line, nil
}
