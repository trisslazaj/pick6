# pick6 — nothing here is required to use the tool. `go build ./...` and
# `go test ./...` are the real build; this file exists so the paper has one
# obvious way to be produced.

.PHONY: paper paper-open paper-clean test build

build:
	go build ./...

test:
	go test ./...

# docs/pick6-engine.tex — the engine written up. Needs a TeX Live with latexmk;
# stock packages only, no shell-escape. Two passes are required for the
# cross-references, which latexmk handles on its own.
paper: docs/pick6-engine.pdf

docs/pick6-engine.pdf: docs/pick6-engine.tex
	cd docs && latexmk -pdf -interaction=nonstopmode -halt-on-error pick6-engine.tex

# Zed has no pdf viewer, so opening the file in the editor shows you nothing
# useful. Hand it to whatever the OS uses instead.
paper-open: paper
	@open docs/pick6-engine.pdf 2>/dev/null \
		|| xdg-open docs/pick6-engine.pdf 2>/dev/null \
		|| echo "no viewer found — the pdf is at docs/pick6-engine.pdf"

paper-clean:
	cd docs && latexmk -C pick6-engine.tex
