package ingest

import (
	"context"
	"fmt"
	"testing"

	"github.com/arvlas/calbot/internal/domain"
)

// scriptedAdapter emits the rows it is given, in order.
type scriptedAdapter struct {
	rows []Raw
}

func (scriptedAdapter) Name() string { return "scripted" }

func (scriptedAdapter) Provenance() Provenance {
	return Provenance{LicenseClass: domain.LicenseRedistributable, Confidence: domain.ConfidenceOpenDB}
}

func (a scriptedAdapter) Fetch(_ context.Context, _ string, emit func(Raw) error) (string, error) {
	for _, raw := range a.rows {
		err := emit(raw)
		if err != nil {
			return "", err
		}
	}
	return "", nil
}

func goodRow(id int) Raw {
	return Raw{
		ExternalID: fmt.Sprint(id),
		Product:    RawProduct{Name: "Тест", Kcal: "100", Protein: "10", Fat: "5", Carbs: "5"},
	}
}

func TestRunStagesInBatches(t *testing.T) {
	var rows []Raw
	for i := 1; i <= 2*stageBatchSize+7; i++ {
		rows = append(rows, goodRow(i))
	}
	store := newFakeStore()
	err := NewRunner(store, 50).Run(context.Background(), scriptedAdapter{rows: rows})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	want := []int{stageBatchSize, stageBatchSize, 7}
	if fmt.Sprint(store.stageCalls) != fmt.Sprint(want) {
		t.Errorf("Stage calls = %v, want %v: one call per batch, remainder flushed at the end", store.stageCalls, want)
	}
}

func TestRejectCapPerReason(t *testing.T) {
	const cap = 3
	tests := []struct {
		name        string
		rows        []Raw
		wantStored  int
		wantDropped int
	}{
		{
			name:        "one reason is capped",
			rows:        rejectedRows("missing name", 10),
			wantStored:  cap,
			wantDropped: 7,
		},
		{
			name:        "under the cap everything is stored",
			rows:        rejectedRows("missing name", 2),
			wantStored:  2,
			wantDropped: 0,
		},
		{
			name:        "each reason has its own budget",
			rows:        append(rejectedRows("missing name", 5), rejectedRows("bad kcal", 5)...),
			wantStored:  2 * cap,
			wantDropped: 4,
		},
		{
			name:        "values inside a reason do not make it a new reason",
			rows:        rejectedRows("energy", 6),
			wantStored:  cap,
			wantDropped: 3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore()
			runner := NewRunner(store, cap)
			counts := Counts{}
			limiter := newRejectLimiter(cap)
			for _, raw := range tt.rows {
				err := runner.reject(context.Background(), limiter, "src", raw, fmt.Errorf("%s", raw.Payload), &counts)
				if err != nil {
					t.Fatalf("reject: %v", err)
				}
			}
			if len(store.rejected) != tt.wantStored {
				t.Errorf("stored %d, want %d", len(store.rejected), tt.wantStored)
			}
			if counts.RejectedDropped != tt.wantDropped {
				t.Errorf("dropped %d, want %d", counts.RejectedDropped, tt.wantDropped)
			}
			if counts.Rejected != len(tt.rows) {
				t.Errorf("counted %d rejections, want all %d", counts.Rejected, len(tt.rows))
			}
		})
	}
}

// rejectedRows makes rows whose rejection text is reason, with a distinct
// number inside it for the "energy" case.
func rejectedRows(reason string, n int) []Raw {
	rows := make([]Raw, n)
	for i := range rows {
		text := reason
		if reason == "energy" {
			text = fmt.Sprintf("Atwater energy mismatch: stated %d.5 kcal", 100+i)
		}
		rows[i] = Raw{ExternalID: fmt.Sprint(i), Payload: []byte(text)}
	}
	return rows
}

func TestReasonKeyFoldsValues(t *testing.T) {
	tests := []struct{ a, b string }{
		{"Atwater energy mismatch: stated 120.50 kcal, calculated 90.00 kcal", "Atwater energy mismatch: stated 87 kcal, calculated 3,5 kcal"},
		{`unsupported value "a b"`, `unsupported value "zz"`},
	}
	for _, tt := range tests {
		if reasonKey(tt.a) != reasonKey(tt.b) {
			t.Errorf("%q and %q should be one reason", tt.a, tt.b)
		}
	}
	if reasonKey("missing name") == reasonKey("missing external ID") {
		t.Error("different reasons folded together")
	}
}
