package ingest

import (
	"fmt"
	"math"

	"github.com/arvlas/calbot/internal/domain"
)

// Validate applies the quarantine checks before a product can be staged.
func Validate(n domain.Nutrition) error {
	if n.Kcal < 0 || n.Protein < 0 || n.Fat < 0 || n.Carbs < 0 {
		return fmt.Errorf("nutrition contains a negative value")
	}
	if n.Kcal > 900 {
		return fmt.Errorf("kcal exceeds 900 per 100 g")
	}
	if n.Protein+n.Fat+n.Carbs > 100 {
		return fmt.Errorf("macros exceed 100 g per 100 g")
	}
	if n.Kcal >= 20 {
		atwater := 4*n.Protein + 9*n.Fat + 4*n.Carbs
		if math.Abs(n.Kcal-atwater) > 0.2*n.Kcal {
			return fmt.Errorf("Atwater energy mismatch: stated %.2f kcal, calculated %.2f kcal", n.Kcal, atwater)
		}
	}
	return nil
}
