# TUI screenshots (simulated)

`make screenshots` (from the repo root) renders the real TUI (`internal/tui`, fake sample data from
`internal/tui/shots_test.go`) as ANSI and converts it to PNG into `out/screenshots-sim/`.

Requirements: Go, `python3`, `rsvg-convert` (librsvg) and the font **JetBrainsMono Nerd Font Mono** (any monospace
font with that family name; otherwise rsvg falls back and the cell alignment may shift). Optional: `optipng`/`pngquant`.
The images in `docs/img/` are copies of the 80x25 shots (`tui-*.png`); they are not photos of real hardware.
