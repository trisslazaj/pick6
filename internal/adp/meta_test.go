package adp

import (
	"testing"
	"time"
)

// TestMetaRoundTrip pins the two things the staleness guard actually depends on:
// the timestamps survive json (a time.Time silently becoming its zero value
// would make every board look freshly fetched, i.e. the guard would warn
// exactly never), and a missing file is an error the caller can swallow rather
// than a panic on a board written before meta.json existed.
func TestMetaRoundTrip(t *testing.T) {
	dir := t.TempDir()

	if _, err := LoadMeta(dir); err == nil {
		t.Error("loading a missing meta.json should error, not return a zero meta silently")
	}

	fetched := time.Now().Add(-26 * time.Hour).Round(time.Second)
	want := Meta{
		FetchedAt:    fetched,
		Format:       "half-ppr",
		Season:       2026,
		Players:      201,
		TotalDrafts:  1110,
		ADPWindowEnd: "2026-07-28",
		TiersFile:    "data/fake-tiers.csv",
		TiersMod:     fetched.Add(-72 * time.Hour),
		SleeperMod:   fetched.Add(-time.Hour),
	}
	if err := WriteMeta(dir, want); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := LoadMeta(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !got.FetchedAt.Equal(want.FetchedAt) || !got.TiersMod.Equal(want.TiersMod) || !got.SleeperMod.Equal(want.SleeperMod) {
		t.Errorf("timestamps did not survive the round trip: %+v", got)
	}
	if got.Format != want.Format || got.TotalDrafts != want.TotalDrafts || got.ADPWindowEnd != want.ADPWindowEnd {
		t.Errorf("fields did not survive the round trip: %+v", got)
	}
	if age := got.Age(); age < 25*time.Hour || age > 27*time.Hour {
		t.Errorf("age = %v, want ~26h", age)
	}
	end, ok := got.WindowEnd()
	if !ok || end.Format("2006-01-02") != "2026-07-28" {
		t.Errorf("window end = %v (ok %v), want 2026-07-28", end, ok)
	}

	// A source that reported no window is not an error — the caller just has
	// nothing to say about it, and must not print a parsed zero date.
	if _, ok := (Meta{}).WindowEnd(); ok {
		t.Error("an empty window end must report ok=false")
	}
}

// ModTime is asked about files that are routinely absent (no rankings file at
// all), so "missing" has to be the zero time and not a crash.
func TestModTimeMissingFile(t *testing.T) {
	if got := ModTime(""); !got.IsZero() {
		t.Errorf("ModTime(\"\") = %v, want zero", got)
	}
	if got := ModTime(t.TempDir() + "/not-here.csv"); !got.IsZero() {
		t.Errorf("ModTime(missing) = %v, want zero", got)
	}
}
