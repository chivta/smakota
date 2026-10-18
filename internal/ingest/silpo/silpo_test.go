package silpo

import "testing"

func TestCursorRoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		cursor   string
		category string
		offset   int
		wantErr  bool
	}{
		{name: "empty starts over", cursor: ""},
		{name: "done marker starts over", cursor: cursorDone},
		{name: "mid category", cursor: "1edb13c0-69c3-6622-b5d4-53f89724fa3a:200", category: "1edb13c0-69c3-6622-b5d4-53f89724fa3a", offset: 200},
		{name: "no offset", cursor: "abc", wantErr: true},
		{name: "negative offset", cursor: "abc:-1", wantErr: true},
		{name: "bad offset", cursor: "abc:x", wantErr: true},
		{name: "empty category", cursor: ":5", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			category, offset, err := parseCursor(tt.cursor)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if category != tt.category || offset != tt.offset {
				t.Fatalf("got %q/%d, want %q/%d", category, offset, tt.category, tt.offset)
			}
			if tt.category != "" && formatCursor(category, offset) != tt.cursor {
				t.Fatalf("format = %q, want %q", formatCursor(category, offset), tt.cursor)
			}
		})
	}
}
