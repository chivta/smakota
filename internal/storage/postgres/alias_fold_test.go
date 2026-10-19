package postgres

import (
	"testing"

	"github.com/arvlas/calbot/internal/domain"
	"github.com/arvlas/calbot/internal/resolver"
)

// The write path must store the same norm the search path derives from a
// query for the same text, or the alias can never match.
func TestFoldAliasesAgreesWithSearchNormalization(t *testing.T) {
	names := []string{
		"Шашлык Из Филе Грудки Куриной",
		"м'ясо",
		"Молокія Молоко 2,5%",
		"пісок",
	}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			got := foldAliases([]domain.Food{{ID: 1, Name: name}}, resolver.Normalize)
			if len(got.norms) != 1 {
				t.Fatalf("norms = %v, want one", got.norms)
			}
			want := resolver.Normalize(name)
			if got.norms[0] != want {
				t.Fatalf("stored norm %q, search norm %q", got.norms[0], want)
			}
		})
	}
}

func TestFoldAliasesCollapsesCollisionsToTheLast(t *testing.T) {
	food := domain.Food{ID: 7, Name: "Молоко", Aliases: []domain.Alias{
		{Alias: "Молоко!", Lang: "ru"},
		{Alias: "сир", Lang: "uk"},
		{Alias: "  ", Lang: "uk"},
	}}

	got := foldAliases([]domain.Food{food}, resolver.Normalize)
	if len(got.texts) != 2 || got.texts[0] != "Молоко!" || got.texts[1] != "сир" {
		t.Fatalf("texts = %v, want [Молоко! сир]", got.texts)
	}
}
