package resolver

import "testing"

func TestNormalize(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "Ukrainian diary entry", in: "Куряче Філе", want: "куряче філе"},
		{name: "Russian diary entry", in: "Яготинский Молоко 1%", want: "яготінскій молоко 1"},
		{name: "quantity remains part of key", in: "варена курка 150г", want: "варена курка 150г"},
		{name: "punctuation and apostrophes", in: "  Мʼясо,  м'ясо! ", want: "мясо мясо"},
		{name: "letter folds and accent", in: "Ґречка Є́дина Элитная", want: "гречка едіна елітная"},
		{name: "precomposed accent", in: "Café", want: "cafe"},
		{name: "Ukrainian letters are preserved", in: "їжа й йогурт", want: "їжа й йогурт"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Normalize(test.in)
			if got != test.want {
				t.Fatalf("Normalize(%q) = %q, want %q", test.in, got, test.want)
			}
		})
	}
}
