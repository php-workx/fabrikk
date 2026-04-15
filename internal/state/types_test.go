package state_test

import (
	"testing"

	"github.com/php-workx/fabrikk/internal/state"
)

func TestBoundariesNormalizedPreservesEmptyAndMarkerLiteral(t *testing.T) {
	normalized := state.Boundaries{
		Never: []string{
			"  ",
			state.EmptyBoundaryItem,
			"docs/",
			" docs/ ",
		},
	}.Normalized()

	if got, want := len(normalized.Never), 3; got != want {
		t.Fatalf("len(Never) = %d, want %d: %q", got, want, normalized.Never)
	}
	if !state.IsEmptyBoundaryItem(normalized.Never[0]) {
		t.Fatalf("first item should be the internal empty boundary marker: %q", normalized.Never[0])
	}
	if state.IsEmptyBoundaryItem(normalized.Never[1]) {
		t.Fatalf("literal marker input should be escaped, not treated as empty: %q", normalized.Never[1])
	}
	if got := state.BoundaryItemValue(normalized.Never[1]); got != state.EmptyBoundaryItem {
		t.Fatalf("BoundaryItemValue(marker literal) = %q, want %q", got, state.EmptyBoundaryItem)
	}
	if got := state.BoundaryItemValue(normalized.Never[2]); got != "docs/" {
		t.Fatalf("BoundaryItemValue(normal path) = %q, want docs/", got)
	}
}
