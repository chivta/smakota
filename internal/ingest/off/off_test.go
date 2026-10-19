package off

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arvlas/calbot/internal/ingest"
)

const testHeader = "code\tproduct_name\tbrands\tquantity\tcountries_tags\tenergy-kcal_100g\tproteins_100g\tfat_100g\tcarbohydrates_100g\tother"

func testColumns(t *testing.T) columns {
	t.Helper()
	cols, err := parseHeader([]byte(testHeader))
	if err != nil {
		t.Fatalf("parse header: %v", err)
	}
	return cols
}

func TestParseHeaderNamesMissingColumns(t *testing.T) {
	_, err := parseHeader([]byte("code\tproduct_name"))
	if err == nil || !strings.Contains(err.Error(), "fat_100g") {
		t.Fatalf("err = %v, want it to name the missing columns", err)
	}
}

func TestRawFromRow(t *testing.T) {
	cols := testColumns(t)
	tests := []struct {
		name     string
		line     string
		keep     bool
		wantName string
		wantKcal string
	}{
		{"complete row", "4820000000001\tМолоко\tЯготинське\t900 ml\ten:ukraine\t60\t3\t3.2\t4.7\tx", true, "Молоко", "60"},
		{"entities are decoded", "1\tM&amp;M white\tFit &amp; Co\t\t\t500\t5\t20\t70", true, "M&M white", "500"},
		{"zero energy is kept", "2\tWater\t\t\t\t0\t0\t0\t0", true, "Water", "0"},
		{"no name", "3\t\t\t\t\t100\t1\t1\t1", false, "", ""},
		{"blank name", "4\t   \t\t\t\t100\t1\t1\t1", false, "", ""},
		{"no energy", "5\tBread\t\t\t\t\t8\t2\t50", false, "", ""},
		{"no barcode", "\tBread\t\t\t\t200\t8\t2\t50", false, "", ""},
		{"short row", "6\tBread", false, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields := splitTabs(nil, []byte(tt.line))
			raw, keep, err := rawFromRow(fields, cols)
			if err != nil {
				t.Fatalf("rawFromRow: %v", err)
			}
			if keep != tt.keep {
				t.Fatalf("keep = %v, want %v", keep, tt.keep)
			}
			if !keep {
				return
			}
			if raw.Product.Name != tt.wantName || raw.Product.Kcal != tt.wantKcal {
				t.Errorf("name/kcal = %q/%q, want %q/%q", raw.Product.Name, raw.Product.Kcal, tt.wantName, tt.wantKcal)
			}
			if raw.ExternalID != raw.Product.Barcode || raw.ExternalID == "" {
				t.Errorf("external ID %q, barcode %q: want the barcode as the ID", raw.ExternalID, raw.Product.Barcode)
			}
		})
	}
}

func TestRawFromHitNameFallback(t *testing.T) {
	tests := []struct {
		name string
		hit  string
		want string
	}{
		{"null product_name uses uk", `{"code":"1","product_name":null,"product_name_uk":"Хліб"}`, "Хліб"},
		{"uk wins over product_name", `{"code":"1","product_name":"Bread","product_name_uk":"Хліб"}`, "Хліб"},
		{"product_name when no uk", `{"code":"1","product_name":"Bread"}`, "Bread"},
		{"blank falls through to ru", `{"code":"1","product_name":" ","product_name_ru":"Хлеб"}`, "Хлеб"},
		{"en last", `{"code":"1","product_name_en":"Bread"}`, "Bread"},
		{"no name at all", `{"code":"1"}`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := rawFromHit([]byte(tt.hit))
			if err != nil {
				t.Fatalf("rawFromHit: %v", err)
			}
			if raw.Product.Name != tt.want {
				t.Errorf("name = %q, want %q", raw.Product.Name, tt.want)
			}
		})
	}
}

func TestRawFromHitNutriments(t *testing.T) {
	raw, err := rawFromHit([]byte(`{"code":"9","brands":["A","B"],"nutriments":{"energy-kcal_100g":109,"proteins_100g":"3.5","fat_100g":null}}`))
	if err != nil {
		t.Fatalf("rawFromHit: %v", err)
	}
	p := raw.Product
	if p.Kcal != "109" || p.Protein != "3.5" || p.Fat != "" || p.Brand != "A, B" {
		t.Errorf("got %+v", p)
	}
}

func TestJobsKeepSeparateCursors(t *testing.T) {
	delta, full := NewDelta(nil), NewFull(nil, "")
	if delta.Name() != full.Name() {
		t.Errorf("names %q and %q differ: one product would become two rows", delta.Name(), full.Name())
	}
	if delta.CursorKey() == full.CursorKey() {
		t.Errorf("both jobs use cursor key %q", delta.CursorKey())
	}
}

// The dump is served from a local test server; no network is involved.
func TestFetchFullSkipsDoneLinesAndCheckpoints(t *testing.T) {
	var csv strings.Builder
	csv.WriteString(testHeader + "\n")
	for i := 2; i <= checkpointEvery+2; i++ {
		switch i {
		case 2, 3, checkpointEvery + 2:
			csv.WriteString(strings.Repeat("x", i%7+1) + "\tItem\t\t\t\t100\t10\t4\t12\t\n")
		default:
			csv.WriteString("\t\t\t\t\t\t\t\t\t\n")
		}
	}
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	zw.Write([]byte(csv.String()))
	zw.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(compressed.Bytes())
	}))
	defer server.Close()

	adapter := NewFull(ingest.NewHTTPClient(&http.Client{}, "test"), server.URL)
	var rows []ingest.Raw
	var checkpoints []string
	next, err := adapter.Fetch(context.Background(), "full:2", func(raw ingest.Raw) error {
		if raw.Checkpoint {
			checkpoints = append(checkpoints, raw.Cursor)
			return nil
		}
		rows = append(rows, raw)
		return nil
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(rows) != 2 || rows[0].Cursor != "full:3" {
		t.Errorf("emitted %d rows starting at %v, want lines 3 and %d: line 2 was already done", len(rows), rows, checkpointEvery+2)
	}
	if len(checkpoints) != 1 || checkpoints[0] != "full:100000" {
		t.Errorf("checkpoints = %v, want one at line 100000", checkpoints)
	}
	if next != "full:0" {
		t.Errorf("next cursor = %q, want a restart marker", next)
	}
}
