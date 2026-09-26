package mailer

import (
	"math"
	"testing"
)

func TestWindowRange(t *testing.T) {
	tests := []struct {
		name       string
		total      int
		limit      int
		offset     int
		start, end int
		ok         bool
	}{
		{"empty mailbox", 0, 20, 0, 0, 0, false},
		{"one message, wider window", 1, 20, 0, 1, 1, true},
		{"exactly the whole mailbox", 5, 5, 0, 1, 5, true},
		{"wider than the mailbox", 5, 50, 0, 1, 5, true},
		{"inside the mailbox", 5, 2, 1, 3, 4, true},
		{"offset at the end", 5, 2, 5, 0, 0, false},
		{"offset past the end", 5, 2, 9, 0, 0, false},
		{"zero limit", 5, 0, 0, 0, 0, false},
		{"negative limit", 5, -1, 0, 0, 0, false},
		{"negative offset", 5, 2, -3, 0, 0, false},
		{"huge limit does not overflow", 5, math.MaxInt, 0, 1, 5, true},
		{"huge limit, non-zero offset", 5, math.MaxInt, 1, 1, 4, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			start, end, ok := WindowRange(tc.total, tc.limit, tc.offset)
			if start != tc.start || end != tc.end || ok != tc.ok {
				t.Errorf("WindowRange(%d, %d, %d) = (%d, %d, %v), want (%d, %d, %v)",
					tc.total, tc.limit, tc.offset, start, end, ok, tc.start, tc.end, tc.ok)
			}
		})
	}
}

func TestDefaultFolder(t *testing.T) {
	if got := DefaultFolder(""); got != "INBOX" {
		t.Errorf(`DefaultFolder("") = %q, want "INBOX"`, got)
	}
	if got := DefaultFolder("Archive"); got != "Archive" {
		t.Errorf(`DefaultFolder("Archive") = %q, want "Archive"`, got)
	}
}
