package proe

import (
	"testing"

	"github.com/arvlas/calbot/internal/ingest"
)

func TestExtractProduct(t *testing.T) {
	tests := []struct {
		name string
		page string
		want ingest.RawProduct
	}{
		{
			name: "sentence with barcode span",
			page: `<html><head><script>var x = "Калорійність - 1 ккал";</script></head><body>
<h1 class="page-title">Чіпси «Lay's»<span class="barcode barcode-generator"> 5900259069924</span></h1>
<p>Вага - 133 г, Калорійність - 527 ккал, Білки - 6.2 г, Жири - 31.4 г, Вуглеводи - 52.6 г. Склад продукту: картопля</p>
<table><tr><td>Білки</td><td>6.2 г</td><td>8.246 г</td></tr></table></body></html>`,
			want: ingest.RawProduct{Name: "Чіпси «Lay's»", Barcode: "5900259069924", Kcal: "527 ккал", Protein: "6.2 г", Fat: "31.4 г", Carbs: "52.6 г"},
		},
		{
			name: "comma decimals split across tags",
			page: `<h1>Сир</h1><p>Калорійність - <b>350,5 ккал</b>, Білки - 20 г, Жири - 28 г, Вуглеводи - 0,5 г</p>`,
			want: ingest.RawProduct{Name: "Сир", Kcal: "350,5 ккал", Protein: "20 г", Fat: "28 г", Carbs: "0,5 г"},
		},
		{
			name: "no nutrition sentence",
			page: `<h1>Порожньо</h1><table><tr><td>Калорійність</td><td>527 ккал</td></tr></table>`,
			want: ingest.RawProduct{Name: "Порожньо"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractProduct([]byte(tt.page))
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
