package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/trisslazaj/pick6/internal/adp"
	"github.com/trisslazaj/pick6/internal/cache"
	"github.com/trisslazaj/pick6/internal/engine"
	"github.com/trisslazaj/pick6/internal/ui"
)

func runTiers(args []string) error {
	fs := flagSet("tiers")
	sportName := sportFlag(fs)
	only := fs.String("pos", "", "show one position only (qb, rb, wr, te, k, def — gkp, def, mid, fwd under -sport fpl)")
	depth := fs.Int("depth", 0, "max players to show per position (0 = all)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	sp, err := resolveSport(*sportName)
	if err != nil {
		return err
	}

	dir, err := cache.Dir()
	if err != nil {
		return err
	}
	// Its own reader rather than loadBoard's, because this command wants the adp
	// rows and not the engine's players — so it needs the same sport key told to
	// it twice. A second reader of one filename is how `tiers -sport fpl` ends up
	// printing the nfl board.
	path := filepath.Join(dir, sp.board)
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("no board yet — run %s first (%w)", sp.fetchCmd(), err)
	}
	var list []*adp.Player
	if err := json.Unmarshal(b, &list); err != nil {
		return err
	}

	byPos := map[string][]*adp.Player{}
	for _, p := range list {
		byPos[p.Pos] = append(byPos[p.Pos], p)
	}

	order := engine.RosterPositions(sp.roster)
	if *only != "" {
		order = []string{strings.ToUpper(*only)}
	}

	for _, pos := range order {
		group := byPos[pos]
		if len(group) == 0 {
			continue
		}
		sort.Slice(group, func(i, j int) bool {
			if group[i].Tier != group[j].Tier {
				// Untiered players sort last, not first.
				if group[i].Tier == 0 || group[j].Tier == 0 {
					return group[j].Tier == 0
				}
				return group[i].Tier < group[j].Tier
			}
			return group[i].ADP < group[j].ADP
		})
		// The tier counts come from the WHOLE position, not from the slice being
		// printed. `-depth 15` used to truncate first, so a tier with six men in
		// it printed "2 left — tier ending" in cliff amber because two of them
		// happened to fall inside the cut. A cheat sheet that invents a cliff is
		// worse than one that stops early. (Predates fpl; found looking at it.)
		counts := map[int]int{}
		for _, p := range group {
			counts[p.Tier]++
		}
		if *depth > 0 && len(group) > *depth {
			group = group[:*depth]
		}
		printPosition(sp, pos, group, counts)
	}
	return nil
}

func printPosition(sp sport, pos string, group []*adp.Player, counts map[int]int) {
	// The early-draft hold renders faint, same as the board. Read off the sport's
	// roster rather than the literal it used to be: fpl holds nothing back, and
	// its 184 defenders printed greyed-out under a rule about team defenses.
	suppressed := sp.roster.Hold == nil && (pos == "K" || pos == "DEF")
	style := ui.Pos(pos, suppressed)
	if sp.roster.Hold != nil {
		style = ui.PosFPL(pos, suppressed)
	}

	fmt.Println()
	fmt.Println(style.Bold(true).Render(strings.ToLower(pos)) + " " +
		ui.Dim.Render(fmt.Sprintf("— %d players", len(group))))

	lastTier := -1
	for _, p := range group {
		if p.Tier != lastTier {
			lastTier = p.Tier
			fmt.Println("  " + tierHeader(p, counts[p.Tier], style))
		}
		fmt.Printf("      %s %s  %s  %s\n",
			style.Render(fmt.Sprintf("%-24s", trunc(strings.ToLower(p.Name), 24))),
			ui.Dim.Render(p.Team),
			ui.Dim.Render(fmt.Sprintf("%s "+sp.priceFmt(), sp.priceNoun(), p.ADP)),
			spreadNote(p))
	}
}

// tierHeader shows the tier, how many are left in it, and where the tier came
// from. Cliff colouring previews what the live board will do: amber at two left,
// red at one.
func tierHeader(p *adp.Player, n int, style lipgloss.Style) string {
	if p.Tier == 0 {
		// Two different groups land on tier 0 and the header has to tell them
		// apart. K and def are untiered by DECISION — they carry a value anchored
		// to the skill curve, and a borrowed value cannot draw a real tier
		// boundary, so tier 0 is what keeps cliff logic off them. The untiered
		// skill players are untiered by ABSENCE: nothing prices them at all. One
		// sentence for both was wrong about six receivers, who are anchored to
		// nothing.
		why := "no value from any source"
		if p.Value > 0 {
			why = "never tiered; value is anchored to adp"
		}
		return ui.Dim.Render(fmt.Sprintf("no tier — %d players (%s)", n, why))
	}
	label := style.Render(fmt.Sprintf("tier %d", p.Tier))
	count := fmt.Sprintf("%d left", n)
	switch {
	case n == 1:
		count = ui.Cliff.Render("cliff — last one")
	case n <= 2:
		count = ui.Run.Render(count + " — tier ending")
	default:
		count = ui.Dim.Render(count)
	}
	return label + "  " + count + "  " + ui.Dim.Render("via "+string(p.TierSrc))
}

func spreadNote(p *adp.Player) string {
	if p.FormatSpread < 10 {
		return ""
	}
	return ui.Run.Render(fmt.Sprintf("±%.0f by scoring", p.FormatSpread))
}
