package pagewalk

import "testing"

func TestPaginateInputBound(t *testing.T) {
	for _, tc := range []struct {
		maxPages  int
		pages     int
		callerSet bool
	}{
		{0, defaultMaxPages, false},
		{25, 25, true},
		{maxMaxPages, maxMaxPages, true},
		{maxMaxPages + 1, maxMaxPages, false},
	} {
		pages, callerSet := PaginateInput{MaxPages: tc.maxPages}.Bound()
		if pages != tc.pages || callerSet != tc.callerSet {
			t.Errorf("Bound(max_pages=%d) = %d, %v; want %d, %v", tc.maxPages, pages, callerSet, tc.pages, tc.callerSet)
		}
	}
}

func TestPageBounds(t *testing.T) {
	if def, ceiling := PageBounds(); def != defaultMaxPages || ceiling != maxMaxPages {
		t.Errorf("PageBounds() = %d, %d", def, ceiling)
	}
}
