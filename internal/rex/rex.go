// Package rex drives Rex through its `rex` command-line tool.
//
// The CLI is Rex's stable interface, so rex-micro never speaks the server's
// socket protocol itself. An embedded Lua script builds a snapshot of every
// session's panes and agent state inside the server: once per `rex do` call,
// or, for the daemon, from one long-lived `rex do` that rebuilds it on events.
package rex

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed snapshot.lua
var snapshotLua []byte

// Runner executes the rex CLI. Tests replace it.
type Runner interface {
	Run(ctx context.Context, stdin []byte, args ...string) ([]byte, error)
}

// Command builds a rex invocation that never starts a Rex server: rex-micro
// only reads and drives a Rex that is already running. Every rex call goes
// through it.
func Command(ctx context.Context, path string, args ...string) *exec.Cmd {
	if path == "" {
		path = "rex"
	}
	return exec.CommandContext(ctx, path, append([]string{"--autostart=false"}, args...)...)
}

// ExecRunner runs a real rex binary through Command.
type ExecRunner struct{ Path string }

func (r ExecRunner) Run(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	cmd := Command(ctx, r.Path, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.Bytes(), fmt.Errorf("rex %s: %s", strings.Join(args, " "), msg)
	}
	return out.Bytes(), nil
}

// Stream runs rex and calls line with each line it prints, until it exits.
func (r ExecRunner) Stream(ctx context.Context, stdin []byte, line func([]byte), args ...string) error {
	cmd := Command(ctx, r.Path, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	errb := &tail{}
	cmd.Stderr = errb
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64*1024), 16<<20)
	for sc.Scan() {
		line(sc.Bytes())
	}
	_, _ = io.Copy(io.Discard, out) // a line past the buffer: keep rex unblocked
	if err := cmd.Wait(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("rex %s: %s", strings.Join(args, " "), msg)
	}
	return nil
}

// tail keeps the last few KB written to it: a watcher runs for days.
type tail struct{ b []byte }

func (t *tail) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > 4096 {
		t.b = t.b[len(t.b)-4096:]
	}
	return len(p), nil
}

func (t *tail) String() string { return string(t.b) }

// Pane is one block and, when an agent runs in it, its state.
type Pane struct {
	Session      string    `json:"session"`
	SessionLabel string    `json:"session_label"`
	SessionIndex int       `json:"session_index"` // 1-based, session.list order
	Window       string    `json:"window"`
	Block        string    `json:"block"`
	Label        string    `json:"label"`
	App          string    `json:"app"`   // e.g. claude-code, codex; "" when no agent
	State        string    `json:"state"` // working, blocked, done, error, idle
	Since        time.Time `json:"since"`
	Focused      bool      `json:"focused"` // focused block of its session's active window
	// Program is the foreground program of a focused terminal, e.g. zsh,
	// lazygit, claude. Only focused panes report it.
	Program string `json:"program,omitempty"`
	// ShownAt is when an app last sized this focused pane; the newest one is
	// in the session the Rex app shows.
	ShownAt time.Time `json:"shown_at"`
}

// Current returns the pane the Rex app most likely shows: of the focused
// panes, the one an app sized most recently. It is nil when none is known.
//
// Only terminals report when the app last sized them, so a session whose
// focused block is another kind (onboarding, a web view) cannot be told apart
// and the last terminal shown still counts as current. Rex exposes no
// "session on screen" for an app client to fix this from.
func Current(panes []Pane) *Pane {
	var best *Pane
	for i := range panes {
		p := &panes[i]
		if p.Focused && !p.ShownAt.IsZero() && (best == nil || p.ShownAt.After(best.ShownAt)) {
			best = p
		}
	}
	return best
}

// Agent reports whether an agent program runs in the pane.
func (p Pane) Agent() bool { return p.App != "" }

// Client is a handle on the Rex server and one Rex app.
type Client struct {
	Runner Runner
	// App is the app client label or ID that receives client actions. Empty
	// selects the first macOS app connected over the local socket.
	App     string
	Timeout time.Duration

	mu       sync.Mutex
	resolved string
}

func (c *Client) run(stdin []byte, args ...string) ([]byte, error) {
	t := c.Timeout
	if t == 0 {
		t = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), t)
	defer cancel()
	return c.Runner.Run(ctx, stdin, args...)
}

func (c *Client) script(watch time.Duration) []byte {
	c.mu.Lock()
	pref := c.App
	c.mu.Unlock()
	head := fmt.Sprintf("local app_pref = %q\nlocal watch = %g\n", pref, watch.Seconds())
	return append([]byte(head), snapshotLua...)
}

func parsePanes(out []byte) ([]Pane, error) {
	out = bytes.TrimSpace(out)
	// Lua encodes an empty list as {}.
	if string(out) == "{}" {
		return nil, nil
	}
	var panes []Pane
	if err := json.Unmarshal(out, &panes); err != nil {
		return nil, fmt.Errorf("parse snapshot: %w", err)
	}
	return panes, nil
}

// Snapshot lists every pane in every session with its agent state.
func (c *Client) Snapshot() ([]Pane, error) {
	out, err := c.run(c.script(0), "do", "-")
	if err != nil {
		return nil, err
	}
	return parsePanes(out)
}

// Streamer runs a long-lived rex command and hands over each output line.
// ExecRunner implements it; a Runner without it is polled instead.
type Streamer interface {
	Stream(ctx context.Context, stdin []byte, line func([]byte), args ...string) error
}

// Watch calls emit with a snapshot whenever one changes, until ctx ends or
// the connection to Rex drops. One rex process stays connected and rebuilds
// the snapshot inside the server after events, and every `every` in case an
// event was missed, so nothing is spawned or attached while Rex is idle. With
// a Runner that cannot stream, Watch polls every `every`, sooner on refresh.
func (c *Client) Watch(ctx context.Context, every time.Duration, refresh <-chan struct{}, emit func([]Pane)) error {
	s, ok := c.Runner.(Streamer)
	if !ok {
		for {
			panes, err := c.Snapshot()
			if err != nil {
				return err
			}
			emit(panes)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(every):
			case <-refresh:
			}
		}
	}
	var bad error
	err := s.Stream(ctx, c.script(every), func(line []byte) {
		if len(line) == 0 || (line[0] != '[' && line[0] != '{') {
			return // rex's "listening" banner
		}
		panes, err := parsePanes(line)
		if err != nil {
			bad = err
			return
		}
		emit(panes)
	}, "do", "-", "--all")
	if ctx.Err() != nil {
		return nil
	}
	if err == nil {
		err = errors.New("rex stopped watching")
	}
	if bad != nil {
		err = fmt.Errorf("%w (%v)", err, bad)
	}
	return err
}

// AppClient resolves the app client that receives client actions. The rex
// call runs outside the lock, so SetApp and ForgetApp never wait on it.
func (c *Client) AppClient() (string, error) {
	c.mu.Lock()
	resolved, app := c.resolved, c.App
	c.mu.Unlock()
	if resolved != "" {
		return resolved, nil
	}
	if app != "" {
		return app, nil // configured explicitly; nothing to cache or go stale
	}
	out, err := c.run(nil, "client", "ls", "--json")
	if err != nil {
		return "", err
	}
	var list struct {
		Clients []struct {
			ID   string `json:"client_id"`
			Info struct {
				Kind string `json:"kind"`
			} `json:"info"`
			Principal struct {
				Transport string `json:"transport"`
			} `json:"principal"`
			ConnectedAt time.Time `json:"connected_at"`
		} `json:"clients"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return "", fmt.Errorf("parse client list: %w", err)
	}
	var best string
	var bestAt time.Time
	for _, cl := range list.Clients {
		if cl.Info.Kind == "app" && cl.Principal.Transport == "unix" && (best == "" || cl.ConnectedAt.After(bestAt)) {
			best, bestAt = cl.ID, cl.ConnectedAt
		}
	}
	if best == "" {
		return "", errors.New("no Rex app is connected on this Mac; open Rex")
	}
	c.mu.Lock()
	if c.App == app { // not reconfigured meanwhile
		c.resolved = best
	}
	c.mu.Unlock()
	return best, nil
}

// SetApp changes the configured app client and forgets the resolved one.
func (c *Client) SetApp(app string) { c.mu.Lock(); c.App, c.resolved = app, ""; c.mu.Unlock() }

// ForgetApp drops the cached app client, e.g. after Rex restarts.
func (c *Client) ForgetApp() { c.mu.Lock(); c.resolved = ""; c.mu.Unlock() }

// Perform runs an action as the Rex app: a client action (pane.zoom), a Rex
// config action defined with rex.action, or a block action.
func (c *Client) Perform(action string, args map[string]string) error {
	app, err := c.AppClient()
	if err != nil {
		return err
	}
	argv := []string{"-C", app, "do", action}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		argv = append(argv, k+"="+args[k])
	}
	_, err = c.run(nil, argv...)
	if err != nil && strings.Contains(err.Error(), "client") {
		c.ForgetApp()
	}
	return err
}

// Show brings a pane into view in the app: select its session and window,
// then focus the block.
func (c *Client) Show(p Pane) error {
	if err := c.Perform("session.select", map[string]string{"session_id": p.Session, "window_id": p.Window}); err != nil {
		return err
	}
	_, err := c.run(nil, "focus", "-s", p.Session, p.Block)
	return err
}

// Zoom sets a block's zoom.
func (c *Client) Zoom(block string, on bool) error {
	flag := "--off"
	if on {
		flag = "--on"
	}
	_, err := c.run(nil, "zoom", block, flag)
	return err
}

// SendKeys sends key presses to a block, or to the app's focused pane when block is "".
func (c *Client) SendKeys(block string, keys ...string) error {
	if block == "" {
		for _, k := range keys {
			if err := c.Perform("pane.send_key", map[string]string{"key": k}); err != nil {
				return err
			}
		}
		return nil
	}
	_, err := c.run(nil, append([]string{"send-key", "-b", block}, keys...)...)
	return err
}

// SendText types text into a block.
func (c *Client) SendText(block, text string) error {
	_, err := c.run(nil, "send", "-b", block, text)
	return err
}

// CLI runs arbitrary rex arguments.
func (c *Client) CLI(args ...string) error {
	_, err := c.run(nil, args...)
	return err
}

// Version reports the rex CLI version, proving rex is installed and reachable.
func (c *Client) Version() (string, error) {
	out, err := c.run(nil, "version")
	return strings.TrimSpace(string(out)), err
}
