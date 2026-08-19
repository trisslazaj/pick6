#!/usr/bin/env python3
"""Turn a priority-matrix draft board into pick6's row-per-player csv.

    python3 docs/fpl-priority-board.py "Draft Ranking.csv" ~/.config/pick6/rankings/fpl/board.csv
    pick6 fetch -sport fpl -rankings ~/.config/pick6/rankings/fpl/board.csv


The sheet lays players out in COLUMNS by priority and groups ROWS by position,
with a position label in column A. pick6 wants one row per player. Column index
becomes the tier; the two trailing note columns become sentiment rather than a
tier, because "still deciding" is an opinion and not a rank.
"""
import csv, re, sys, unicodedata

SRC, OUT = sys.argv[1], sys.argv[2]
rows = list(csv.reader(open(SRC)))
head = rows[0]

# Columns that are notes, not tiers: everything from the first labelled
# non-priority header onward.
note_from = min([i for i, c in enumerate(head)
                 if c.strip() and not c.strip().upper().startswith("PRIO")] or [len(head)])
note_label = {}
for i, c in enumerate(head):
    if i >= note_from and c.strip():
        note_label[i] = c.strip()

POS = {"gk": "GKP", "gkp": "GKP", "keeper": "GKP",
       "def": "DEF", "mid": "MID", "fw": "FWD", "fwd": "FWD"}

def clean(name):
    name = re.sub(r"\(.*?\)", "", name)          # "Gabriel (8)" -> "Gabriel"
    return " ".join(name.split()).strip(" ,")

out, pos, cur_note = [], "GKP", ""   # the sheet starts on keepers with no label
for r in rows[1:]:
    a = r[0].strip() if r else ""
    if a.lower() in POS:                          # a position group starts here
        pos = POS[a.lower()]
        continue
    if a and (len(a.split()) > 3 or a.lower().startswith("updated") or "prio" in a.lower()):
        continue                                  # legend / notes at the bottom
    for i, cell in enumerate(r):
        n = clean(cell)
        if not n:
            continue
        if i in note_label or i >= note_from:
            cur_note = note_label.get(i, cur_note)
            out.append((n, pos, "", cur_note))    # no tier: an opinion, not a rank
        else:
            out.append((n, pos, str(i + 1), ""))

with open(OUT, "w", newline="") as f:
    w = csv.writer(f)
    w.writerow(["name", "position", "team", "tier", "sentiment"])
    for n, p, t, note in out:
        s = "avoid" if note.lower().startswith("injur") else ("pass" if note else "")
        w.writerow([n, p, "", t, s])
print(f"{len(out)} players out of {SRC}")
