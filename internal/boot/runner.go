// Package boot plans and executes the M1 VM boot flow (dm-snapshot stack + kexec).
package boot

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Runner executes external commands. All side effects on devices go through it.
type Runner interface {
	Run(name string, args ...string) (string, error)
}

func quote(name string, args []string) string {
	parts := []string{name}
	for _, a := range args {
		if a == "" || strings.ContainsAny(a, " \t'\"\\$;&|<>*?{}") {
			a = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}

// StdinRunner is a Runner that can feed a command's standard input (used for `mksquashfs - out -tar`).
type StdinRunner interface {
	Runner
	RunStdin(stdin io.Reader, name string, args ...string) (string, error)
}

// ExecRunner runs real commands; executed commands are appended to Log if set.
type ExecRunner struct {
	LogFile string
	// Guest mode: tools found in BinDir run through the bundled dynamic loader with LibDir.
	BinDir, LibDir, Loader string
	Env                    []string
}

func (r *ExecRunner) Run(name string, args ...string) (string, error) {
	return r.RunStdin(nil, name, args...)
}

func (r *ExecRunner) RunStdin(stdin io.Reader, name string, args ...string) (string, error) {
	if r.LogFile != "" {
		f, err := os.OpenFile(r.LogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return "", err
		}
		fmt.Fprintln(f, quote(name, args))
		f.Close()
	}
	cmd := exec.Command(name, args...)
	if r.BinDir != "" {
		if p := r.BinDir + "/" + name; fileExists(p) {
			cmd = exec.Command(r.Loader, append([]string{"--library-path", r.LibDir, p}, args...)...)
		}
	}
	cmd.Stdin = stdin
	if len(r.Env) > 0 {
		cmd.Env = append(os.Environ(), r.Env...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s: %w: %s", quote(name, args), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// DryRunner records commands, prints them to Out (if non-nil) and returns
// plausible synthetic output so planning can continue. Used for --dry-run and tests.
type DryRunner struct {
	Out  io.Writer
	Cmds []string
	// MntDir, if set, is returned by mktemp (lets tests provide a real image tree).
	MntDir string
	// Respond may override the synthetic output of a command line.
	Respond func(line string) (string, bool)

	// Stdin holds what was piped to RunStdin commands, keyed by command line.
	Stdin map[string][]byte

	loops int
}

// RunStdin drains stdin (so producers finish) and records the command.
func (d *DryRunner) RunStdin(stdin io.Reader, name string, args ...string) (string, error) {
	if stdin != nil {
		if d.Stdin == nil {
			d.Stdin = map[string][]byte{}
		}
		b, _ := io.ReadAll(stdin)
		d.Stdin[quote(name, args)] = b
	}
	return d.Run(name, args...)
}

func (d *DryRunner) Run(name string, args ...string) (string, error) {
	line := quote(name, args)
	d.Cmds = append(d.Cmds, line)
	if d.Out != nil {
		fmt.Fprintln(d.Out, line)
	}
	if d.Respond != nil {
		if o, ok := d.Respond(line); ok {
			return o, nil
		}
	}
	switch name {
	case "losetup":
		d.loops++
		return fmt.Sprintf("/dev/loop%d\n", d.loops-1), nil
	case "mktemp":
		if d.MntDir != "" {
			return d.MntDir + "\n", nil
		}
		return "/tmp/omloader-mnt.XXXXXX\n", nil
	case "kpartx":
		if len(args) >= 3 && args[0] == "-a" {
			b := args[len(args)-1]
			b = b[strings.LastIndex(b, "/")+1:]
			sep := ""
			if b != "" && b[len(b)-1] >= '0' && b[len(b)-1] <= '9' {
				sep = "p"
			}
			return fmt.Sprintf("add map %s%s1 (253:9): 0 1 linear %s 2048\nadd map %s%s2 (253:10): 0 1 linear %s 4096\n", b, sep, b, b, sep, b), nil
		}
	case "blkid":
		if len(args) > 1 && args[1] == "export" {
			return "DEVNAME=" + args[len(args)-1] + "\nUUID=DRY-RUN-UUID\nTYPE=ext4\n", nil
		}
		if len(args) > 0 && args[0] == "-U" {
			return "/dev/dry-by-uuid\n", nil
		}
		return "ext4\n", nil
	}
	return "", nil
}
