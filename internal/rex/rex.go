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
	// Host is the label of the Rex host the pane lives on (`rex hosts`), ""
	// for this Mac's server.
	Host         string    `json:"host,omitempty"`
	Session      string    `json:"session"`
	SessionLabel string    `json:"session_label"`
	SessionIndex int       `json:"session_index"` // 1-based: this Mac's sessions, then each host's, in list order
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
	// in the session the Rex app shows, on whichever host. Only this Mac's
	// app's sizing counts; it joins every host under the same label.
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
	// Logf, when set, hears about hosts that cannot be watched.
	Logf func(format string, args ...any)

	mu       sync.Mutex
	resolved string
	hostOf   map[string]string // block ID -> host label, from the last Watch emit
}

// on prefixes args with -S for a pane on another host.
func on(host string, args ...string) []string {
	if host == "" {
		return args
	}
	return append([]string{"-S", host}, args...)
}

// host is the host a block was last seen on ("" for this Mac).
func (c *Client) host(block string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hostOf[block]
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

// script is the snapshot script for this Mac's server.
func (c *Client) script(watch time.Duration) []byte {
	c.mu.Lock()
	pref := c.App
	c.mu.Unlock()
	return c.scriptFor(pref, watch)
}

// scriptFor is the snapshot script counting only the app client pref
// (label or ID; "" = the app on the local socket) as the one showing panes.
func (c *Client) scriptFor(pref string, watch time.Duration) []byte {
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

// Hosts lists the labels of the other Rex hosts this Mac's rex knows, the
// ones the Rex app's sidebar also shows.
func (c *Client) Hosts() ([]string, error) {
	out, err := c.run(nil, "hosts", "--json")
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return nil, nil
	}
	var list struct {
		Hosts []struct {
			Host struct {
				Label string `json:"label"`
			} `json:"host"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("parse host list: %w", err)
	}
	var labels []string
	for _, h := range list.Hosts {
		if h.Host.Label != "" {
			labels = append(labels, h.Host.Label)
		}
	}
	return labels, nil
}

// appLabel is the label of this Mac's app client. The app joins every host
// under the same label, which is how a host's snapshot tells this app's
// sizing from other clients'. It is "" when unknown.
func (c *Client) appLabel() string {
	id, err := c.AppClient()
	if err != nil {
		return ""
	}
	out, err := c.run(nil, "client", "ls", "--json")
	if err != nil {
		return ""
	}
	var list struct {
		Clients []struct {
			ID   string `json:"client_id"`
			Info struct {
				Label string `json:"label"`
			} `json:"info"`
		} `json:"clients"`
	}
	if json.Unmarshal(out, &list) != nil {
		return ""
	}
	for _, cl := range list.Clients {
		if cl.ID == id {
			return cl.Info.Label
		}
	}
	return ""
}

// Watch calls emit with the panes of this Mac's Rex and of every host in
// `rex hosts` whenever they change, until ctx ends or the connection to this
// Mac's Rex drops. Each server gets its own watcher (see watchOne). The host
// list is read again every `every`, so a host added or removed later is
// followed. A host that cannot be reached contributes no panes and is retried
// every `every`; it never stops the watch. Emits are serialized.
func (c *Client) Watch(ctx context.Context, every time.Duration, refresh <-chan struct{}, emit func([]Pane)) error {
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() { cancel(); wg.Wait() }() // no rex process outlives the watch
	m := &merger{c: c, emit: emit, byHost: map[string][]Pane{}}
	wg.Add(1)
	go func() { defer wg.Done(); c.followHosts(ctx, every, m) }()
	c.mu.Lock()
	pref := c.App
	c.mu.Unlock()
	return c.watchOne(ctx, "", pref, every, refresh, func(p []Pane) { m.publish("", p, true) })
}

// followHosts keeps one watcher per host in `rex hosts`, re-reading the list
// every `every`, until ctx ends.
func (c *Client) followHosts(ctx context.Context, every time.Duration, m *merger) {
	type watcher struct {
		stop context.CancelFunc
		done chan struct{}
	}
	running := map[string]watcher{}
	defer func() {
		for _, w := range running {
			w.stop()
			<-w.done
		}
	}()
	failed := ""
	for {
		hosts, err := c.Hosts()
		switch {
		case err != nil:
			if err.Error() != failed && c.Logf != nil {
				c.Logf("rex hosts: %v; keeping the hosts already watched", err)
			}
			failed = err.Error()
		default:
			failed = ""
			m.setOrder(hosts) // before a new host's first snapshot arrives
			want := map[string]bool{}
			label := ""
			for _, h := range hosts {
				want[h] = true
				if _, ok := running[h]; ok {
					continue
				}
				if label == "" {
					label = c.appLabel()
				}
				hctx, stop := context.WithCancel(ctx)
				w := watcher{stop, make(chan struct{})}
				running[h] = w
				go func() { defer close(w.done); c.watchHost(hctx, h, label, every, m) }()
			}
			for h, w := range running {
				if !want[h] {
					w.stop()
					<-w.done
					delete(running, h)
					m.drop(h)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

// watchHost keeps watching one host, retrying every `every` while it does
// not answer. label is this Mac's app's label there ("" when unknown).
func (c *Client) watchHost(ctx context.Context, host, label string, every time.Duration, m *merger) {
	reported := ""
	for ctx.Err() == nil {
		err := c.watchOne(ctx, host, label, every, nil, func(p []Pane) {
			reported = ""
			if label == "" {
				for i := range p {
					p[i].ShownAt = time.Time{} // any client's sizing; must not decide what this Mac shows
				}
			}
			m.publish(host, p, true)
		})
		if ctx.Err() != nil {
			return
		}
		m.publish(host, nil, false) // its panes are gone until it answers again
		if err != nil && err.Error() != reported && c.Logf != nil {
			c.Logf("rex host %s: %v", host, err)
			reported = err.Error()
		}
		select {
		case <-ctx.Done():
		case <-time.After(every):
		}
	}
}

// merger combines the servers' snapshots into one list, this Mac's first,
// with session numbers continuing from server to server.
type merger struct {
	c      *Client
	emit   func([]Pane)
	mu     sync.Mutex
	order  []string // host labels after this Mac
	byHost map[string][]Pane
}

func (m *merger) setOrder(hosts []string) {
	m.mu.Lock()
	m.order = hosts
	m.mu.Unlock()
}

// publish records a server's panes and emits the merged list. A server that
// went away (ok false) keeps its blocks' host routes, so an action already
// queued for one of them still goes to that host, not to this Mac.
func (m *merger) publish(host string, panes []Pane, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.byHost[host] = panes
	m.c.mu.Lock()
	if m.c.hostOf == nil {
		m.c.hostOf = map[string]string{}
	}
	if ok {
		seen := map[string]bool{}
		for _, p := range panes {
			seen[p.Block] = true
			m.c.hostOf[p.Block] = host
		}
		for b, h := range m.c.hostOf {
			if h == host && !seen[b] {
				delete(m.c.hostOf, b) // closed
			}
		}
	}
	m.c.mu.Unlock()
	var all []Pane
	offset := 0
	for _, h := range append([]string{""}, m.order...) {
		top := 0
		for _, p := range m.byHost[h] {
			p.Host = h
			top = max(top, p.SessionIndex)
			p.SessionIndex += offset
			all = append(all, p)
		}
		offset += top
	}
	m.emit(all)
}

// drop forgets a host that left `rex hosts`.
func (m *merger) drop(host string) {
	m.c.mu.Lock()
	for b, h := range m.c.hostOf {
		if h == host {
			delete(m.c.hostOf, b)
		}
	}
	m.c.mu.Unlock()
	m.mu.Lock()
	delete(m.byHost, host)
	m.mu.Unlock()
}

// watchOne calls emit with one server's snapshot whenever it changes, until
// ctx ends or the connection drops. One rex process stays connected and
// rebuilds the snapshot inside the server after events, and every `every` in
// case an event was missed, so nothing is spawned or attached while Rex is
// idle. With a Runner that cannot stream, it polls every `every`, sooner on
// refresh.
func (c *Client) watchOne(ctx context.Context, host, pref string, every time.Duration, refresh <-chan struct{}, emit func([]Pane)) error {
	s, ok := c.Runner.(Streamer)
	if !ok {
		for {
			out, err := c.run(c.scriptFor(pref, 0), on(host, "do", "-")...)
			if err != nil {
				return err
			}
			panes, err := parsePanes(out)
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
	err := s.Stream(ctx, c.scriptFor(pref, every), func(line []byte) {
		if len(line) == 0 || (line[0] != '[' && line[0] != '{') {
			return // rex's "listening" banner
		}
		panes, err := parsePanes(line)
		if err != nil {
			bad = err
			return
		}
		emit(panes)
	}, on(host, "do", "-", "--all")...)
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

// Show brings a pane into view in the app: focus the block on the server,
// then have the app select its session and window. The order matters: the
// app does not follow a focus change in a session it already shows, so
// focusing after the select left the old pane on screen. A pane on another
// host is focused on that host; this Mac's app shows any host's session.
func (c *Client) Show(p Pane) error {
	if _, err := c.run(nil, on(p.Host, "focus", "-s", p.Session, p.Block)...); err != nil {
		return err
	}
	return c.Perform("session.select", map[string]string{"session_id": p.Session, "window_id": p.Window})
}

// Zoom sets a block's zoom.
func (c *Client) Zoom(block string, zoom bool) error {
	flag := "--off"
	if zoom {
		flag = "--on"
	}
	_, err := c.run(nil, on(c.host(block), "zoom", block, flag)...)
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
	_, err := c.run(nil, on(c.host(block), append([]string{"send-key", "-b", block}, keys...)...)...)
	return err
}

// SendText types text into a block.
func (c *Client) SendText(block, text string) error {
	_, err := c.run(nil, on(c.host(block), "send", "-b", block, text)...)
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
