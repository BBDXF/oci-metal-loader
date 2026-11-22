package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"omloader/internal/config"
)

// runConfigGet implements `omloader config get [-c omloader.yaml] KEY` for shell scripts: it prints the resolved
// value of a dotted key (e.g. boot.default_image). Scalars print as-is, lists one element per line; an unknown key
// or a mapping is an error (exit 1).
func runConfigGet(args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("config get", flag.ContinueOnError)
	fs.SetOutput(errw)
	cfg := fs.String("c", "", "global omloader.yaml")
	pos, err := parse(fs, args)
	if err != nil {
		return 1
	}
	if len(pos) != 1 {
		fmt.Fprintln(errw, "usage: omloader config get [-c omloader.yaml] KEY   (e.g. boot.default_image)")
		return 1
	}
	r, err := config.Load(*cfg, "")
	if err != nil {
		fmt.Fprintln(errw, "error:", err)
		return 1
	}
	raw, _ := json.Marshal(r.Config)
	var v any
	_ = json.Unmarshal(raw, &v)
	for _, k := range strings.Split(pos[0], ".") {
		m, ok := v.(map[string]any)
		if !ok {
			v, ok = nil, false
		} else {
			v, ok = m[k]
		}
		if !ok {
			fmt.Fprintf(errw, "error: unknown key %q\n", pos[0])
			return 1
		}
	}
	switch t := v.(type) {
	case nil:
	case string:
		fmt.Fprintln(out, t)
	case []any:
		for _, e := range t {
			fmt.Fprintln(out, fmt.Sprint(e))
		}
	case map[string]any:
		fmt.Fprintf(errw, "error: %q is a mapping, not a value\n", pos[0])
		return 1
	default:
		fmt.Fprintln(out, fmt.Sprint(t))
	}
	return 0
}
