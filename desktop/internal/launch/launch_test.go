package launch

import (
	"bytes"
	"errors"
	"log/slog"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func env(vars map[string]string, installed ...string) Env {
	have := map[string]bool{}
	for _, i := range installed {
		have[i] = true
	}
	return Env{
		Getenv: func(k string) string { return vars[k] },
		LookPath: func(n string) (string, error) {
			if have[n] {
				return "/usr/bin/" + n, nil
			}
			return "", errors.New("not found")
		},
	}
}

func TestSplitCommand(t *testing.T) {
	cases := map[string][]string{
		`vim`:                    {"vim"},
		`code --wait`:            {"code", "--wait"},
		`  nvim   -u  NONE `:     {"nvim", "-u", "NONE"},
		`"/opt/My Editor/ed" -n`: {"/opt/My Editor/ed", "-n"},
		`sh -c 'a b'`:            {"sh", "-c", "a b"},
		`a\ b c`:                 {"a b", "c"},
		`x ""`:                   {"x", ""},
		`it's`:                   nil, // unterminated
	}
	for in, want := range cases {
		got, err := SplitCommand(in)
		if want == nil {
			if err == nil {
				t.Errorf("%q: expected error", in)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %q (%v) want %q", in, got, err, want)
		}
	}
}

func TestEditorResolutionOrder(t *testing.T) {
	path := "/p/default.yaml"
	e := env(map[string]string{"VISUAL": "code --wait", "EDITOR": "vim"}, "code", "vim", "xdg-open")
	// an override that is not installed is skipped, falling through to $VISUAL
	if got, _ := EditorCmd(e, "gedit", path); got[0] != "code" {
		t.Fatalf("%v", got)
	}
	// override > VISUAL > EDITOR
	e2 := env(map[string]string{"VISUAL": "code --wait", "EDITOR": "vim"}, "code", "vim", "gedit", "xdg-open")
	if got, _ := EditorCmd(e2, "gedit", path); !reflect.DeepEqual(got, []string{"gedit", path}) {
		t.Fatalf("override: %v", got)
	}
	if got, _ := EditorCmd(e2, "", path); !reflect.DeepEqual(got, []string{"code", "--wait", path}) {
		t.Fatalf("VISUAL: %v", got)
	}
	e3 := env(map[string]string{"EDITOR": "kate"}, "kate", "xdg-open")
	if got, _ := EditorCmd(e3, "", path); !reflect.DeepEqual(got, []string{"kate", path}) {
		t.Fatalf("EDITOR: %v", got)
	}
	// an uninstalled candidate is skipped, not fatal
	e4 := env(map[string]string{"VISUAL": "ghost", "EDITOR": "kate"}, "kate")
	if got, _ := EditorCmd(e4, "", path); got[0] != "kate" {
		t.Fatalf("skip: %v", got)
	}
	// nothing set → desktop default
	if got, _ := EditorCmd(env(nil, "xdg-open"), "", path); !reflect.DeepEqual(got, []string{"xdg-open", path}) {
		t.Fatalf("xdg-open: %v", got)
	}
	// nothing at all → helpful error
	if _, err := EditorCmd(env(map[string]string{"EDITOR": "ghost"}), "", path); err == nil {
		t.Fatal("expected error")
	}
}

func TestTerminalEditorsAreWrapped(t *testing.T) {
	path := "/p/a b.yaml" // a space must stay one argument
	e := env(map[string]string{"EDITOR": "nvim"}, "nvim", "gnome-terminal", "xterm")
	got, err := EditorCmd(e, "", path)
	if err != nil || !reflect.DeepEqual(got, []string{"gnome-terminal", "--", "nvim", path}) {
		t.Fatalf("%v %v", got, err)
	}
	// $TERMINAL wins, with its own exec flag
	e = env(map[string]string{"EDITOR": "nano", "TERMINAL": "alacritty"}, "nano", "alacritty", "gnome-terminal")
	if got, _ := EditorCmd(e, "", path); !reflect.DeepEqual(got, []string{"alacritty", "-e", "nano", path}) {
		t.Fatalf("%v", got)
	}
	// kitty/foot take the command directly
	e = env(map[string]string{"EDITOR": "vim", "TERMINAL": "foot"}, "vim", "foot")
	if got, _ := EditorCmd(e, "", path); !reflect.DeepEqual(got, []string{"foot", "vim", path}) {
		t.Fatalf("%v", got)
	}
	// no terminal available → clear error, never a half-started command
	if _, err := EditorCmd(env(map[string]string{"EDITOR": "vim"}, "vim"), "", path); err == nil {
		t.Fatal("expected error without a terminal")
	}
	// GUI editors are not wrapped; emacs only with -nw
	e = env(map[string]string{"EDITOR": "emacs"}, "emacs", "xterm")
	if got, _ := EditorCmd(e, "", path); got[0] != "emacs" {
		t.Fatalf("%v", got)
	}
	e = env(map[string]string{"EDITOR": "emacs -nw"}, "emacs", "xterm")
	if got, _ := EditorCmd(e, "", path); got[0] != "xterm" {
		t.Fatalf("%v", got)
	}
}

func TestFilePathsAreNeverInterpreted(t *testing.T) {
	path := "/p/$(rm -rf ~) `x`; a'b.yaml"
	got, _ := EditorCmd(env(map[string]string{"EDITOR": "kate"}, "kate"), "", path)
	if got[len(got)-1] != path || len(got) != 2 {
		t.Fatalf("%q", got)
	}
}

func TestShowCmdsAndFallback(t *testing.T) {
	path := "/home/u/My Profiles/a.yaml"
	cmds := ShowCmds(env(nil, "dbus-send", "xdg-open"), path)
	if len(cmds) != 2 || cmds[0][0] != "dbus-send" || cmds[1][0] != "xdg-open" || cmds[1][1] != "/home/u/My Profiles" {
		t.Fatalf("%v", cmds)
	}
	if got := cmds[0][len(cmds[0])-2]; got != "array:string:file:///home/u/My%20Profiles/a.yaml" {
		t.Fatalf("uri: %s", got)
	}
	// dbus fails → xdg-open of the folder is tried
	var ran [][]string
	run := func(argv []string, wait bool) error {
		ran = append(ran, argv)
		if argv[0] == "dbus-send" {
			return errors.New("no file manager on the bus")
		}
		return nil
	}
	if err := Show(env(nil, "dbus-send", "xdg-open"), path, run); err != nil || len(ran) != 2 {
		t.Fatal(err, ran)
	}
	if err := Show(env(nil), path, run); err == nil {
		t.Fatal("expected error when no helper exists")
	}
	if _, err := OpenDirCmd(env(nil), "/x"); err == nil {
		t.Fatal("expected error without xdg-open")
	}
}

func TestEditUsesRunner(t *testing.T) {
	var got []string
	err := Edit(env(map[string]string{"EDITOR": "kate"}, "kate"), "", "/p/x.yaml", func(a []string, wait bool) error {
		got = a
		if wait {
			t.Error("editors must be started detached")
		}
		return nil
	})
	if err != nil || !reflect.DeepEqual(got, []string{"kate", "/p/x.yaml"}) {
		t.Fatal(err, got)
	}
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestDetachedFailureIsLogged(t *testing.T) {
	var logs syncBuf
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(old)

	if err := Exec([]string{"sh", "-c", "echo no-handler-here >&2; exit 3"}, false); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(logs.String(), "no-handler-here") {
		if time.Now().After(deadline) {
			t.Fatalf("failure of a detached command was not logged: %q", logs.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(logs.String(), "exit status 3") {
		t.Fatalf("exit status missing: %q", logs.String())
	}
}

func TestCappedBufferBounds(t *testing.T) {
	c := &cappedBuffer{max: 4}
	if n, _ := c.Write([]byte("abcdefgh")); n != 8 {
		t.Fatal("Write must report all bytes consumed")
	}
	c.Write([]byte("zz"))
	if c.String() != "abcd" {
		t.Fatalf("%q", c.String())
	}
}

func TestExecWaitSuccessFailureAndTimeout(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	if err := Exec([]string{"sh", "-c", "exit 0"}, true); err != nil {
		t.Errorf("success: %v", err)
	}
	if err := Exec([]string{"sh", "-c", "exit 3"}, true); err == nil {
		t.Error("a non-zero exit must be reported")
	}
	if err := Exec([]string{"definitely-not-a-command-xyz"}, true); err == nil {
		t.Error("a missing command must be reported")
	}
	start := time.Now()
	err := Exec([]string{"sh", "-c", "sleep 30"}, true)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("timeout: %v", err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("the wait was not bounded: %v", d)
	}
}
