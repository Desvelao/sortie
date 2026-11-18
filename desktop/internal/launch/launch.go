// Package launch opens files and folders with external programs: the user's
// text editor (also terminal editors such as vim) and the desktop file
// manager. Commands are built as argument lists and never go through a shell,
// so file names cannot inject anything. It has no GUI dependency; the command
// builders are pure so they can be tested without starting anything.
package launch

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Env abstracts the environment so command building is testable.
type Env struct {
	Getenv   func(string) string
	LookPath func(string) (string, error)
}

// DefaultEnv uses the real environment and PATH.
func DefaultEnv() Env { return Env{Getenv: os.Getenv, LookPath: exec.LookPath} }

// terminalEditors run inside a terminal and need a terminal emulator.
var terminalEditors = map[string]bool{
	"vi": true, "vim": true, "nvim": true, "view": true, "nvi": true, "vimx": true,
	"nano": true, "pico": true, "micro": true, "joe": true, "ne": true, "jed": true,
	"hx": true, "helix": true, "kak": true, "ed": true, "mcedit": true, "tilde": true, "kakoune": true,
}

// terminals lists emulators to try (after $TERMINAL and x-terminal-emulator)
// with the arguments that precede the command to run.
var terminals = []struct {
	name   string
	prefix []string
}{
	{"gnome-terminal", []string{"--"}}, {"kgx", []string{"--"}}, {"ptyxis", []string{"--"}},
	{"konsole", []string{"-e"}}, {"xfce4-terminal", []string{"-x"}}, {"kitty", nil},
	{"alacritty", []string{"-e"}}, {"foot", nil}, {"wezterm", []string{"start", "--"}},
	{"tilix", []string{"-e"}}, {"terminator", []string{"-x"}}, {"lxterminal", []string{"-e"}},
	{"mate-terminal", []string{"-x"}}, {"urxvt", []string{"-e"}}, {"st", []string{"-e"}}, {"xterm", []string{"-e"}},
}

// execPrefix returns the "run this command" arguments of a terminal emulator.
func execPrefix(base string) []string {
	if base == "x-terminal-emulator" {
		return []string{"-e"}
	}
	for _, t := range terminals {
		if t.name == base {
			return t.prefix
		}
	}
	return []string{"-e"}
}

// SplitCommand splits a command line into arguments honouring single quotes,
// double quotes and backslash escapes. It never expands variables or runs
// anything.
func SplitCommand(s string) ([]string, error) {
	var args []string
	var cur strings.Builder
	in, quote := false, rune(0)
	esc := false
	for _, r := range s {
		switch {
		case esc:
			cur.WriteRune(r)
			esc = false
		case r == '\\' && quote != '\'':
			esc, in = true, true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, in = r, true
		case r == ' ' || r == '\t':
			if in {
				args = append(args, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteRune(r)
			in = true
		}
	}
	if quote != 0 {
		return nil, errors.New("unterminated quote in command")
	}
	if esc {
		cur.WriteRune('\\')
	}
	if in {
		args = append(args, cur.String())
	}
	return args, nil
}

// isTerminalEditor reports whether argv runs in a terminal. Emacs is a
// terminal editor only when asked to be (-nw).
func isTerminalEditor(argv []string) bool {
	base := filepath.Base(argv[0])
	if base == "emacs" || base == "emacsclient" {
		for _, a := range argv[1:] {
			if a == "-nw" || a == "-t" || a == "--no-window-system" || a == "--tty" {
				return true
			}
		}
		return false
	}
	return terminalEditors[base]
}

// EditorCmd returns the command that opens path in an editor: override (the
// app's "editor" setting), then $VISUAL, then $EDITOR, then the desktop's
// default via xdg-open. Candidates whose program is not installed are
// skipped. Terminal editors are wrapped in a terminal emulator.
func EditorCmd(env Env, override, path string) ([]string, error) {
	var tried []string
	for _, cand := range []string{override, env.Getenv("VISUAL"), env.Getenv("EDITOR")} {
		if strings.TrimSpace(cand) == "" {
			continue
		}
		argv, err := SplitCommand(cand)
		if err != nil || len(argv) == 0 {
			tried = append(tried, fmt.Sprintf("%q (%v)", cand, err))
			continue
		}
		if _, err := env.LookPath(argv[0]); err != nil {
			tried = append(tried, fmt.Sprintf("%q (not found)", argv[0]))
			continue
		}
		argv = append(argv, path)
		if isTerminalEditor(argv) {
			return wrapInTerminal(env, argv)
		}
		return argv, nil
	}
	if _, err := env.LookPath("xdg-open"); err == nil {
		return []string{"xdg-open", path}, nil
	}
	msg := "no editor found: set $VISUAL/$EDITOR or the editor setting"
	if len(tried) > 0 {
		msg += " (tried " + strings.Join(tried, ", ") + ")"
	}
	return nil, errors.New(msg)
}

func wrapInTerminal(env Env, inner []string) ([]string, error) {
	var cands []string
	if t := strings.TrimSpace(env.Getenv("TERMINAL")); t != "" {
		cands = append(cands, t)
	}
	cands = append(cands, "x-terminal-emulator")
	for _, t := range terminals {
		cands = append(cands, t.name)
	}
	for _, c := range cands {
		argv, err := SplitCommand(c)
		if err != nil || len(argv) == 0 {
			continue
		}
		if _, err := env.LookPath(argv[0]); err != nil {
			continue
		}
		out := append([]string(nil), argv...)
		out = append(out, execPrefix(filepath.Base(argv[0]))...)
		return append(out, inner...), nil
	}
	return nil, fmt.Errorf("%s is a terminal editor but no terminal emulator was found (set $TERMINAL)", filepath.Base(inner[0]))
}

// ShowCmds returns the commands to try, in order, to show path in the file
// manager with the file selected: the freedesktop FileManager1 D-Bus call,
// then opening the containing folder with xdg-open.
func ShowCmds(env Env, path string) [][]string {
	var out [][]string
	if _, err := env.LookPath("dbus-send"); err == nil {
		u := url.URL{Scheme: "file", Path: path}
		out = append(out, []string{"dbus-send", "--session", "--print-reply", "--dest=org.freedesktop.FileManager1",
			"--type=method_call", "/org/freedesktop/FileManager1", "org.freedesktop.FileManager1.ShowItems",
			"array:string:" + u.String(), "string:"})
	}
	if _, err := env.LookPath("xdg-open"); err == nil {
		out = append(out, []string{"xdg-open", filepath.Dir(path)})
	}
	return out
}

// OpenDirCmd returns the command that opens a folder in the file manager.
func OpenDirCmd(env Env, dir string) ([]string, error) {
	if _, err := env.LookPath("xdg-open"); err != nil {
		return nil, errors.New("xdg-open is not installed")
	}
	return []string{"xdg-open", dir}, nil
}

// Runner starts a command. wait=true waits for it and reports failure (used
// for short helpers such as dbus-send); otherwise it is started detached.
type Runner func(argv []string, wait bool) error

// Edit opens path in the user's editor.
func Edit(env Env, override, path string, run Runner) error {
	argv, err := EditorCmd(env, override, path)
	if err != nil {
		return err
	}
	return run(argv, false)
}

// Show reveals path in the file manager, trying each strategy until one works.
func Show(env Env, path string, run Runner) error {
	cmds := ShowCmds(env, path)
	if len(cmds) == 0 {
		return errors.New("neither dbus-send nor xdg-open is installed")
	}
	var last error
	for _, argv := range cmds {
		if last = run(argv, argv[0] == "dbus-send"); last == nil {
			return nil
		}
	}
	return last
}

// Open opens dir in the file manager.
func Open(env Env, dir string, run Runner) error {
	argv, err := OpenDirCmd(env, dir)
	if err != nil {
		return err
	}
	return run(argv, false)
}

// Exec is the real Runner: detached start (new session, reaped in the
// background) or a bounded wait.
func Exec(argv []string, wait bool) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	if wait {
		done := make(chan error, 1)
		if err := cmd.Start(); err != nil {
			return err
		}
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			return err
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
			return errors.New(argv[0] + " timed out")
		}
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	stderr := &cappedBuffer{max: maxStderr}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		// A detached command cannot report back to the caller; at least log
		// why it failed (no handler for the type, no DISPLAY, ...).
		if err := cmd.Wait(); err != nil {
			slog.Warn("external command failed", "command", argv[0], "error", err, "stderr", strings.TrimSpace(stderr.String()))
		}
	}()
	return nil
}

// maxStderr bounds how much stderr of a detached command is kept for logging.
const maxStderr = 2048

// cappedBuffer keeps at most max bytes and silently discards the rest, so a
// chatty long-running program cannot grow memory. Safe for the single writer
// goroutine exec uses plus the reader after Wait returns.
type cappedBuffer struct {
	max int
	buf []byte
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if room := c.max - len(c.buf); room > 0 {
		if len(p) < room {
			room = len(p)
		}
		c.buf = append(c.buf, p[:room]...)
	}
	return len(p), nil
}

func (c *cappedBuffer) String() string { return string(c.buf) }
