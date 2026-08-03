package chrome

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestHelpTwoColumnLayout(t *testing.T) {
	two := helpBody(100)
	one := helpBodyOneCol(100)
	ht, ho := lipgloss.Height(two), lipgloss.Height(one)
	if ht >= ho {
		t.Fatalf("two-col height %d should be < one-col %d", ht, ho)
	}
	// Fits typical ~28-row shells with strip chrome remaining.
	if ht > 26 {
		t.Fatalf("two-col too tall: %d", ht)
	}
	if !strings.Contains(two, "Tabs") || !strings.Contains(two, "Panes") {
		t.Fatal("missing sections")
	}
	// Narrow window uses single column (still opens).
	narrow := helpBody(40)
	if lipgloss.Height(narrow) < 10 {
		t.Fatal("narrow help empty")
	}
}
