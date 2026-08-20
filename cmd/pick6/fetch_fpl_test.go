package main

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/trisslazaj/pick6/internal/fpl"
)

// printSidelined is the only report `fetch -sport fpl` grew, and it runs once a
// season on a machine nobody is watching closely — so it gets a test rather
// than a first outing on draft morning. Obviously fake names.
func TestPrintSidelined(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	zero := 0
	out := []fpl.Element{
		{ID: 2, WebName: "Bravo", DraftRank: 90, Status: "i", ChanceNext: &zero,
			News: "Leg injury - Expected back 28 Nov"},
		{ID: 1, WebName: "Alpha", DraftRank: 12, Status: "i", ChanceNext: &zero,
			News: "Achilles injury - Unknown return date"},
	}
	got := capture(t, func() { printSidelined(out, now, true) })
	// Deepest first would bury the man who matters; rank order is the point.
	if i, j := strings.Index(got, "alpha"), strings.Index(got, "bravo"); i < 0 || j < 0 || i > j {
		t.Errorf("rows are not in rank order:\n%s", got)
	}
	for _, want := range []string{"rank 12", "no return date", "back 28 nov"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if s := capture(t, func() { printSidelined(nil, now, true) }); s != "" {
		t.Errorf("an empty list printed %q", s)
	}
}

// capture runs f with stdout redirected and returns what it wrote.
func capture(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	f()
	w.Close()
	os.Stdout = old
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
