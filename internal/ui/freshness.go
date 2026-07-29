package ui

import (
	"fmt"
	"strconv"
	"time"

	"github.com/trisslazaj/pick6/internal/engine"
)

// Freshness is what the board knows about its own age.
//
// The board is a photograph, not a window: adp is a trailing average and every
// injury flag froze at fetch time. This is the only thing on screen that can
// say how long ago that was.
//
// It is PASSED IN, flattened, rather than read here — internal/ui does not
// import internal/adp and should not start. That package is the whole fetch
// side (http clients for ffc, fantasycalc and mfl, tier derivation, name
// matching); pulling it into the renderer to read three fields would make it
// the largest edge in the ui's dependency graph and would put a cache-dir read
// inside a render. The ui already takes a flattened engine.Player for exactly
// this reason. cmd/pick6's loadFreshness does the conversion from adp.Meta.
type Freshness struct {
	FetchedAt time.Time
	Drafts    int       // drafts behind the primary adp
	WindowEnd time.Time // last day of ffc's adp sample; zero when unreported
}

// Known reports whether a meta.json was there at all. A board fetched before
// that file existed still draws fine; it just cannot say how old it is, and
// "adp 0h old" about a week-old board is worse than silence because the reader
// has no way to catch it.
func (f Freshness) Known() bool { return !f.FetchedAt.IsZero() }

// note is the footer's freshness clause, in a long form and a short one. Both
// are "" when nothing is known.
//
// The count goes first when it has to go: the age is the actionable half — it
// decides whether you refetch — and the sample size is provenance you read once
// a season.
func (f Freshness) note() (long, short string) {
	if !f.Known() {
		return "", ""
	}
	short = "adp " + shortAge(time.Since(f.FetchedAt)) + " old"
	if f.Drafts > 0 {
		return short + " · " + commas(f.Drafts) + " drafts", short
	}
	return short, short
}

// Stale reports whether the live board should say out loud that it is old, and
// what to say.
//
// Warn, never block. A stale board still beats no board at a draft party — the
// same principle the poll loop already follows when the wifi drops — and the
// only thing this can do about it is tell you to run the refetch you were going
// to run anyway.
func (f Freshness) Stale() (string, bool) {
	if !f.Known() {
		return "", false // nothing known: nothing to warn about, and no "0h old"
	}
	if age := time.Since(f.FetchedAt); age >= engine.StaleADPHours*time.Hour {
		return fmt.Sprintf(
			"stale — adp and injury flags are %s old. run `pick6 fetch`", shortAge(age)), true
	}
	// A fetch can be minutes old while the data behind it is not: every source is
	// disk-cached, so a fetch that hits cache all the way through moves only
	// FetchedAt. ffc's own window end is what actually went stale.
	if !f.WindowEnd.IsZero() {
		if d := time.Since(f.WindowEnd); d > engine.ADPWindowStaleDays*24*time.Hour {
			return fmt.Sprintf(
				"stale — ffc's adp window ended %s ago. run `pick6 fetch`", shortAge(d)), true
		}
	}
	return "", false
}

// shortAge renders a duration the way a draft room says it: hours until the
// board is properly old, then days. A negative age — a clock that moved
// backwards, or a meta.json written by a machine running ahead — reads 0h
// rather than "-3h old", which would look like a bug in the tool rather than in
// the clock.
func shortAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if h := int(d.Hours()); h < 72 {
		return strconv.Itoa(h) + "h"
	}
	return strconv.Itoa(int(d.Hours())/24) + "d"
}

// commas groups a count for reading: 1110 drafts is a number you parse, 1,110
// is one you glance at. Hand-rolled because the stdlib has no thousands
// formatter and this project does not add a dependency for four lines.
func commas(n int) string {
	s := strconv.Itoa(n)
	if n < 0 {
		return s // never happens for a draft count; a comma after "-" would look broken
	}
	var out []byte
	for i := 0; i < len(s); i++ {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	return string(out)
}
