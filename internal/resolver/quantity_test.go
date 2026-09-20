package resolver

import (
	"reflect"
	"testing"
)

func TestParseQuantity(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		want       Quantity
		wantPhrase string
	}{
		{name: "attached grams", in: "варена курка 150г", want: Quantity{Grams: 150}, wantPhrase: "варена курка"},
		{name: "spaced grams", in: "курка 150 г", want: Quantity{Grams: 150}, wantPhrase: "курка"},
		{name: "named portion", in: "2 склянки молока", want: Quantity{Count: 2, Portion: "склянки"}, wantPhrase: "молока"},
		{name: "half kilo", in: "півкіло картоплі", want: Quantity{Grams: 500}, wantPhrase: "картоплі"},
		{name: "pieces", in: "яйця 3 шт", want: Quantity{Count: 3, Portion: "шт"}, wantPhrase: "яйця"},
		{name: "grams word", in: "100 грам рису", want: Quantity{Grams: 100}, wantPhrase: "рису"},
		{name: "decimal kilograms", in: "0.5 кг яблук", want: Quantity{Grams: 500}, wantPhrase: "яблук"},
		{name: "diary product without quantity", in: "Яготинский Молоко 1%", want: Quantity{}, wantPhrase: "Яготинский Молоко 1%"},
		{name: "diary food without quantity", in: "Куряче Філе", want: Quantity{}, wantPhrase: "Куряче Філе"},
		{name: "parenthesized grams", in: "Nestle Lion (42г)", want: Quantity{Grams: 42}, wantPhrase: "Nestle Lion"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, gotPhrase := ParseQuantity(test.in)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("ParseQuantity(%q) quantity = %#v, want %#v", test.in, got, test.want)
			}
			if gotPhrase != test.wantPhrase {
				t.Fatalf("ParseQuantity(%q) phrase = %q, want %q", test.in, gotPhrase, test.wantPhrase)
			}
		})
	}
}
