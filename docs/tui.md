# Interactive terminal UI (`omloader tui`)

The loader boots into a keyboard-only terminal UI. The whole CLI keeps working unchanged; the TUI calls the same library code
(`internal/snapmgr`, `snapshot`, `boot`, `fetch`/`simplestreams`/`cache`, `config`) and never shells out to `omloader`.

```
omloader tui [-c omloader.yaml] [--images-root DIR] [--snap-root DIR] [--data-root DIR] [--ascii]
```

## Screens and keys

Tabs: `1` Images, `2` Pull, `3` Storage, `4` Network, `5` Settings (also Tab / Shift+Tab). Always: `?` help, `s` shell,
`P` reboot / power off, `q` or `Esc` on the first screen = leave to the shell, `Ctrl+L` redraw. **Ctrl+C cancels the running
operation and never quits the TUI.** Navigation: arrows or `j`/`k`, PgUp/PgDn, Home/End, Enter, Esc. The bottom bar shows the keys of
the current screen. UI text: English only, all strings live in `internal/tui/strings.go` (`Strings` / `L`) for later translation.

| Screen | What it does |
|---|---|
| Images | name, type (`vm` / `lxd-container` / `oci (exp.)`), base size, base sha state (`ok`/`MISMATCH`/`-`), snapshot count, current snapshot, last use; `r` refresh, Enter opens the image |
| Image detail | snapshot **tree** (current marker, `[frozen]`/`[leaf]`, `tmp-` ephemeral, tags, on-disk size) plus a pane with parent/created/tags/note. Actions: `b`/Enter **Boot** (current / selected / new named / base ephemeral), `c` create (name, note, tag), `r` rollback (`--discard` asks a warning), `d` delete (recursive + current need explicit confirmation), `t` tag (`-tag` removes), `n` note, `p` prune/GC (dry-run first, then confirm), `f` flatten, `e` export, `v` verify (optional deep) |
| Pull | configured sources (policy shown), `t` = `source test` with timings, Enter loads the catalog (`images list` backend), `/` filter (os / release / type), Enter on an image: name, confirm, background pull with streamed output, byte progress and speed (partial files in the cache), sha256 verification line. Offline / policy settings are honoured (`policy.allow_online`, `cache.offline`, `allowed_sources`) |
| Storage | snapshot store: partition or store file (host fs, size, allocated, ext4 clean/dirty, attached), free space, space guard (`snapshot.retention.min_free_percent`); `c` create / grow store-file wizard (reuses `snapmgr` store code) |
| Network | like `omloader net status` (`r` refresh, `t` also probes the sources) |
| Settings | resolved config with the source layer of every key (read-only, like `config show --resolved`) |

### Boot handoff

Boot confirmation shows the exact `omloader boot ...` command, what it will do, and `d` streams a **dry-run preview**. Booting a
snapshot that has children needs `--force`; the TUI shows an explicit warning dialog first. After the confirmation the TUI restores
the terminal and runs `runBoot` in the foreground, i.e. **exactly the code path of `omloader boot`**: VM = dm-snapshot + kexec
(never returns), container = mounts the root and writes `/run/omloader-switch`, then `omloader tui` exits 0 and
`switch-root.sh` (the code formerly inline in `autoboot.sh`) runs `systemctl switch-root`. If the boot fails the error is printed and the TUI comes back after Enter.
A lock (`/run/omloader/boot.lock`) stops a second console from booting at the same time.

## Launch design (decision)

* `autoboot.sh` (omloader-autoboot.service, before the gettys) decides the console UI once and writes `/run/omloader/ui`
  (`tui` or `shell`) and, when a boot config file was found, its path to `/run/omloader/uicfg`:
  `omloader.ui=tui|shell` kernel flag > `boot.ui` of `<VTOYDATA|OMLOADER>/omloader/boot.yaml` (or `omloader.yaml`) > default `tui`.
  `omloader.image=`, `omloader.hook=1`, `omloader.selftest=1` always force `shell` (the e2e suites are unaffected).
* The existing autologin getties stay (tty1 and ttyS0). The login shell's `/etc/profile.d/omloader-banner.sh` sources
  `tui-launch.sh` on tty1 / ttyS* / hvc0 when `/run/omloader/ui` says `tui`: it runs `omloader tui` in the foreground of that
  console, then does the container switch-root if requested, otherwise falls through to the shell banner. **Leaving the shell
  (`exit`) makes getty log in again, which shows the TUI again.** Both consoles run an independent TUI at the same time.
* With `boot.default_image` the countdown is unchanged; Enter/any key cancels it and the TUI opens (instead of a bare shell).
* Exit codes of `omloader tui`: 0 quit / boot prepared, 10 shell requested.
* Marker: `OMLOADER_TUI_READY uptime=N` is printed to the console right before the first frame (boot timing; e2e-live).

## Terminals

Raw ANSI renderer (no terminfo/curses): cursor addressing, SGR reverse/bold and a few colours; works on `TERM=linux` (tty1),
`vt100`/`vt220`/`xterm` serial consoles. Minimum 80x24 (serial consoles report no window size: 80x24 is assumed, or `COLUMNS`/`LINES`,
SIGWINCH is honoured when the tty reports a size). UTF-8 box drawing / tree glyphs only when the locale is UTF-8 and `TERM` is not
`vt*`; otherwise (and always with `--ascii`) plain ASCII. Colours are off for `vt100`/`dumb`; selection is reverse video. Navigation
keys can be rebound with `OMLOADER_TUI_KEYS="down=n,m;up=w"` (actions: up down pgup pgdn home end select back next prev help cancel redraw).

## Code layout

* `internal/tui`: `Model` (state + `Update(Key)` + `View() *Canvas`, no IO), `Service` interface (everything the model needs),
  `Op` (background operations with streamed output, cancel), `Terminal` (raw mode, key reader, diff renderer), `strings.go`.
* `cmd/omloader/tui.go`: the real `Service` (library calls; `streamRunner` echoes commands and stops between commands on cancel) and the boot handoff.
* Tests: `go test ./internal/tui` (navigation, tree, flows with a fake `Service`, ASCII fallback, 80x24 layout, key parsing, keymap, a pty test with
  a mini VT emulator), remote `make e2e-tui` (`test/e2e-tui.sh`, `test/tui-drive.py`: QEMU serial on a pty).

## Dependencies / licenses

No third-party libraries were added (custom renderer, standard library + `syscall` ioctls); the binary grew by ~0.35 MB
(12.9 MB -> 13.3 MB static, CGO_ENABLED=0). Existing dependency: `gopkg.in/yaml.v3` (MIT / Apache-2.0).

## Limits

* Cancelling flatten/export/store operations takes effect between the external commands (the Runner API has no context); pull and probes cancel immediately.
* Pull progress is the size of partial files in the cache (no per-mirror line; the mirror that served the file is only visible through retry/fallback warnings).
* The two consoles do not synchronise their views (refresh with `r`); only booting is locked.
* VM boot from the TUI is the same `omloader boot` path (kexec) but the e2e only boots a container from the TUI; the VM path stays covered by `make e2e`/`e2e-pull`.
* Grow releases (unmounts) the store file; reboot afterwards.
* A real serial terminal larger than 80x24 is used as 80x24.
