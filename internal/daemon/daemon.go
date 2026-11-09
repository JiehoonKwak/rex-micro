// Package daemon runs rex-micro: it owns the pad, follows Rex, lights the
// keys and turns presses into Rex actions. A Unix socket lets the CLI ask for
// status, reload the config, or pause the daemon during `rex-micro test`.
package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/JiehoonKwak/rex-micro/internal/agents"
	"github.com/JiehoonKwak/rex-micro/internal/config"
	"github.com/JiehoonKwak/rex-micro/internal/engine"
	"github.com/JiehoonKwak/rex-micro/internal/hid"
	"github.com/JiehoonKwak/rex-micro/internal/macos"
	"github.com/JiehoonKwak/rex-micro/internal/micro"
	"github.com/JiehoonKwak/rex-micro/internal/protocol"
	"github.com/JiehoonKwak/rex-micro/internal/rex"
)

// SocketPath is where the daemon listens for the CLI.
func SocketPath() string {
	if p := os.Getenv("REX_MICRO_SOCKET"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Application Support", "rex-micro", "daemon.sock")
}

// SlotStatus describes one Agent Key in Status.
type SlotStatus struct {
	Key     string `json:"key"`
	State   string `json:"state"`
	Block   string `json:"block,omitempty"`
	Host    string `json:"host,omitempty"` // "" for this Mac
	Session string `json:"session,omitempty"`
	Label   string `json:"label,omitempty"`
	App     string `json:"app,omitempty"`
}

// Status is what `rex-micro status` prints.
type Status struct {
	PID        int          `json:"pid"`
	Executable string       `json:"executable"`
	Access     string       `json:"input_monitoring"`
	Connected  bool         `json:"connected"`
	Transport  string       `json:"transport,omitempty"`
	Firmware   string       `json:"firmware,omitempty"`
	Battery    float64      `json:"battery,omitempty"`
	Layer      string       `json:"layer"`
	LayerIndex int          `json:"layer_index"`
	Layers     []string     `json:"layers"`
	Slots      []SlotStatus `json:"agent_keys"`
	Selected   string       `json:"selected_agent,omitempty"`
	Paused     bool         `json:"paused"`
	Config     string       `json:"config"`
	ConfigErr  string       `json:"config_error,omitempty"`
	RexErr     string       `json:"rex_error,omitempty"`
	LastErr    string       `json:"last_error,omitempty"`
	// Foreign is when another app last changed the pad's lights (RFC 3339).
	Foreign string `json:"foreign_lighting,omitempty"`
	// Accessibility is granted or denied; shortcut bindings need it.
	Accessibility string `json:"accessibility"`
	FrontApp      string `json:"front_app,omitempty"`
	Program       string `json:"program,omitempty"` // in the pane Rex shows
	// PadLayers is the pad's own layers: off, syncing, sync or "error: ...".
	PadLayers string `json:"pad_layers,omitempty"`
}

type request struct {
	Cmd     string `json:"cmd"`
	fw      *micro.Status
	pad     *padDone
	padLost error // a layer notice the pad never took
	reply   chan any
	done    chan struct{} // closed when a pausing client disconnects
}

type daemon struct {
	logger   *log.Logger
	cfgPath  string
	cfg      *config.Config
	cfgMod   time.Time
	cfgErr   string
	rex      *rex.Client
	pad      *micro.Pad
	eng      *engine.Engine
	assigner agents.Assigner
	slots    [agents.Keys]agents.Slot
	panes    []rex.Pane

	status       Status
	poll         atomic.Int64
	rewatch      atomic.Pointer[context.CancelFunc] // ends the current watch
	rewake       chan struct{}                      // ends a retry wait
	watchDone    chan struct{}
	lastSent     []byte
	pauses       int
	foreignAt    time.Time
	context      atomic.Pointer[[2]string] // app in front and Rex program, for the effect worker
	front        string                    // app in front
	asked        bool                      // Accessibility prompt shown
	frontApp     func() string
	trusted      func() bool
	freshTrusted func() bool // nil: never restart for a grant
	requestTrust func()
	effects      effectQueue
	refresh      chan struct{}
	fronts       chan string
	snapshots    chan snapshot
	requests     chan request

	// The pad's own layers (padlayers.go).
	padJobs   chan padJob
	padTell   chan int
	padSeen   chan int
	padOn     atomic.Bool // the worker polls the pad's layer
	padGen    int
	padSynced bool
	padToldAt time.Time
	padWant   int     // the layer last sent to the pad
	padIO     padPort // the pad, as the worker uses it

	// The keys overlay (overlay.go).
	keys    keysView
	keysOut chan string
}

type snapshot struct {
	panes []rex.Pane
	err   error
}

// Start runs the daemon with explicit dependencies until opts.Stop closes.
func Start(opts Options) error {
	logger, cfgPath := opts.Logger, opts.ConfigPath
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("config %s: %w", cfgPath, err)
	}
	sock := SocketPath()
	if c, err := net.DialTimeout("unix", sock, 300*time.Millisecond); err == nil {
		c.Close()
		return errors.New("rex-micro is already running (see `rex-micro status`)")
	}
	_ = os.MkdirAll(filepath.Dir(sock), 0o700)
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	defer ln.Close()
	defer os.Remove(sock)

	exe := opts.Self
	if opts.FrontApp == nil {
		opts.FrontApp = macos.FrontApp
	}
	if opts.Shortcut == nil {
		opts.Shortcut = macos.Post
	}
	if opts.HoldShortcut == nil {
		opts.HoldShortcut = macos.PostHeld
	}
	requestTrust := func() {} // tests supply Trusted and never see a prompt
	if opts.Trusted == nil {
		opts.Trusted, requestTrust = macos.Trusted, func() { macos.RequestTrust() }
		if opts.FreshTrusted == nil && exe != "" {
			opts.FreshTrusted = func() bool { return exec.Command(exe, "_trusted").Run() == nil }
		}
	}
	d := &daemon{
		logger:       logger,
		cfgPath:      cfgPath,
		cfg:          cfg,
		rex:          &rex.Client{Runner: opts.Runner, App: cfg.Rex.App, Logf: logger.Printf},
		pad:          micro.New(opts.Device),
		refresh:      make(chan struct{}, 1),
		rewake:       make(chan struct{}, 1),
		watchDone:    make(chan struct{}),
		fronts:       make(chan string, 1),
		frontApp:     opts.FrontApp,
		trusted:      opts.Trusted,
		freshTrusted: opts.FreshTrusted,
		requestTrust: requestTrust,
		snapshots:    make(chan snapshot, 1),
		requests:     make(chan request),
		padJobs:      make(chan padJob, 1),
		padTell:      make(chan int, 1),
		padSeen:      make(chan int, 1),
		keysOut:      make(chan string, 1),
		status:       Status{PID: os.Getpid(), Executable: exe, Config: cfgPath},
	}
	d.padIO = d.pad
	if st, err := os.Stat(cfgPath); err == nil {
		d.cfgMod = st.ModTime()
	}
	d.poll.Store(int64(cfg.Rex.Poll.Duration))
	program := func() string {
		if c := d.context.Load(); c != nil {
			return c[1]
		}
		return ""
	}
	d.eng = engine.New(cfg, executor{rex: d.rex, post: opts.Shortcut, hold: opts.HoldShortcut, front: opts.FrontApp, program: program, logger: logger}, d.enqueue, logger.Printf)
	d.eng.OnLayer = d.onLayer
	d.eng.OnKeys = d.onKeys
	helper := &overlayHelper{exe: exe, logf: logger.Printf}
	d.keys = helper
	d.warmKeys()
	d.askTrust(logger)
	go d.runEffects()
	go d.padWorker(opts.Stop)
	go d.keysWorker(opts.Stop)
	watchCtx, stopWatch := context.WithCancel(context.Background())
	go d.watchRex(watchCtx)
	go d.pollFront(opts.Stop)
	go d.serve(ln, logger)

	logger.Printf("rex-micro running (config %s)", cfgPath)
	err = d.loop(opts.Stop, opts.Update, logger)
	// Stop the rex watcher and wait for it, so no rex process outlives this one.
	stopWatch()
	select {
	case <-d.watchDone:
	case <-time.After(3 * time.Second):
		logger.Printf("the rex watcher did not stop in time")
	}
	// Finish what held keys started (a push-to-talk stop) before this process
	// goes away, whether it is stopping or restarting into a new version.
	d.eng.Reset()
	// Skip presses still queued, but run every release: a push-to-talk
	// start must get its stop. Each effect may take up to one shell timeout.
	if !d.effects.finish(shellTimeout + 3*time.Second) {
		logger.Printf("a key action was still running; ran the release actions alongside it")
	}
	d.effects.close()
	d.lightsOff()
	helper.stop()
	d.pad.Close()
	return err
}

func RexPath() string {
	if p := os.Getenv("REX_MICRO_REX"); p != "" {
		return p
	}
	if p, err := exec.LookPath("rex"); err == nil {
		return p
	}
	// launchd starts agents with a minimal PATH; look where Rex installs its CLI.
	home, _ := os.UserHomeDir()
	for _, p := range []string{
		filepath.Join(home, "Library/Application Support/com.superlogical.rex/RexServer/Versions/Current/Rex Server.app/Contents/MacOS/rex"),
		"/opt/homebrew/bin/rex", "/usr/local/bin/rex", filepath.Join(home, ".local/bin/rex"),
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "rex"
}

func (d *daemon) loop(stop <-chan struct{}, update func() (bool, error), logger *log.Logger) error {
	upgrade := time.NewTicker(30 * time.Second)
	defer upgrade.Stop()
	timer := time.NewTimer(time.Hour)
	cfgTick := time.NewTicker(time.Second)
	defer cfgTick.Stop()
	resend := time.NewTicker(15 * time.Second)
	defer resend.Stop()
	trustTick := time.NewTicker(3 * time.Second)
	defer trustTick.Stop()
	for {
		d.armTimer(timer)
		select {
		case <-stop:
			return nil
		case <-upgrade.C:
			if update == nil {
				continue
			}
			if ok, err := update(); err != nil {
				logger.Printf("update check: %v", err)
			} else if ok {
				logger.Printf("a new rex-micro version is installed; restarting")
				return nil
			}
		case ev, ok := <-d.pad.Events():
			if !ok {
				return errors.New("device closed")
			}
			d.onPad(ev, logger)
		case <-timer.C:
			if d.pauses == 0 {
				d.eng.Advance(time.Now())
			}
		case s := <-d.snapshots:
			d.onSnapshot(s)
		case app := <-d.fronts:
			d.front = app
			d.followContext()
		case <-cfgTick.C:
			d.checkConfig(logger)
		case <-resend.C:
			d.render(true) // another app may have repainted the pad
		case <-trustTick.C:
			if d.grantedSinceStart() {
				logger.Printf("Accessibility granted; restarting to use it")
				return nil // launchd's KeepAlive starts a process that has it
			}
		case r := <-d.requests:
			d.onRequest(r, logger)
		case i := <-d.padSeen:
			d.onPadLayer(i)
		}
	}
}

func (d *daemon) armTimer(t *time.Timer) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	next := d.eng.NextDeadline()
	if next.IsZero() {
		t.Reset(time.Hour)
		return
	}
	t.Reset(max(time.Until(next), time.Millisecond))
}

func (d *daemon) onPad(ev micro.Event, logger *log.Logger) {
	switch {
	case ev.Connected:
		d.status.Connected, d.status.Transport = true, ev.Transport
		go func() {
			// The pad can miss a request sent the instant it enumerates; retry.
			var st micro.Status
			var err error
			for try := 0; try < 4; try++ {
				time.Sleep(time.Duration(300+700*try) * time.Millisecond)
				if st, err = d.pad.Status(1500 * time.Millisecond); err == nil {
					break
				}
			}
			if err != nil {
				logger.Printf("pad connected over %s but did not answer device.status: %v", ev.Transport, err)
				return
			}
			logger.Printf("pad connected over %s, firmware %s", ev.Transport, st.Version)
			d.requests <- request{fw: &st}
		}()
		d.lastSent = nil
		d.render(true)
	case ev.Disconnected:
		d.status.Connected, d.status.Transport = false, ""
		d.padSynced = false // checked again when it comes back
		d.updatePadOn()
		d.eng.Reset() // releases will never arrive; finish held keys now
		logger.Printf("pad disconnected")
	case ev.Foreign != "":
		now := time.Now()
		if d.foreignAt.IsZero() || now.Sub(d.foreignAt) > time.Minute {
			logger.Printf("another app sent %s to the pad and is changing its lights; remove ChatGPT from Input Monitoring and restart it, and quit Work Louder Input", ev.Foreign)
		}
		d.foreignAt = now
		d.status.Foreign = now.Format(time.RFC3339)
	case ev.Input != nil:
		if d.pauses > 0 {
			return
		}
		d.eng.Input(*ev.Input, time.Now())
		d.kick()
	}
}

func (d *daemon) kick() {
	select {
	case d.refresh <- struct{}{}:
	default:
	}
}

// askTrust shows the Accessibility prompt once when a binding sends shortcuts.
func (d *daemon) askTrust(logger *log.Logger) {
	if d.asked || !d.cfg.UsesShortcut() || d.trusted() {
		return
	}
	d.asked = true
	logger.Printf("shortcut bindings need Accessibility; allow rex-micro in System Settings > Privacy & Security > Accessibility")
	d.requestTrust()
}

// grantedSinceStart reports an Accessibility grant this process cannot see.
func (d *daemon) grantedSinceStart() bool {
	return d.freshTrusted != nil && d.cfg.UsesShortcut() && !d.trusted() && d.freshTrusted()
}

// render sends the Agent Key and backlight lighting when it changed.
// onLayer follows a layer change: lights, the pad's own layer and the keys
// overlay, which redraws for the new layer while it is up.
func (d *daemon) onLayer(i int) {
	d.render(true)
	d.tellPad(i)
	if d.eng.KeysShown() {
		d.onKeys(true)
	}
}

func (d *daemon) render(force bool) {
	if d.pauses > 0 || !d.status.Connected {
		return
	}
	lights := agents.Lights(d.slots, d.cfg.Lighting)
	for i := range lights {
		// A key this layer rebinds shows the layer, not an agent's state.
		if color, ok := d.eng.Rebound(i); ok {
			lights[i] = d.layerLight(color)
		}
	}
	keys := protocol.AgentKeys(lights)
	back := d.backlight()
	msg := append(append([]byte{}, keys...), back...)
	if !force && bytes.Equal(msg, d.lastSent) {
		return
	}
	if err := d.pad.Send(keys); err != nil {
		d.status.LastErr = err.Error()
		return
	}
	if err := d.pad.Send(back); err != nil {
		d.status.LastErr = err.Error()
		return
	}
	d.lastSent = msg
}

func (d *daemon) backlight() []byte {
	off := protocol.Light{}
	if d.cfg.Lighting.Backlight == "off" {
		return protocol.Ambient(off, off)
	}
	l := d.layerLight(d.eng.LayerConfig().Color)
	return protocol.Ambient(l, l)
}

// layerLight is a steady light in a layer's color, or off without one.
func (d *daemon) layerLight(color string) protocol.Light {
	if color == "" {
		return protocol.Light{}
	}
	c, _ := protocol.ParseColor(color)
	return protocol.Light{Effect: 1, Brightness: 0.6 * d.cfg.Lighting.Brightness, Color: c}
}

func (d *daemon) lightsOff() {
	if !d.status.Connected {
		return
	}
	var dark [agents.Keys]protocol.Light
	_ = d.pad.Send(protocol.AgentKeys(dark))
	_ = d.pad.Send(protocol.Ambient(protocol.Light{}, protocol.Light{}))
}

func (d *daemon) checkConfig(logger *log.Logger) {
	st, err := os.Stat(d.cfgPath)
	var mod time.Time
	if err == nil {
		mod = st.ModTime()
	}
	if mod.Equal(d.cfgMod) {
		return
	}
	d.cfgMod = mod
	d.reload(logger)
}

func (d *daemon) reload(logger *log.Logger) error {
	cfg, err := config.Load(d.cfgPath)
	if err != nil {
		d.cfgErr = err.Error()
		logger.Printf("config not reloaded: %v", err)
		return err
	}
	d.cfgErr = ""
	if cfg.Rex != d.cfg.Rex {
		d.rex.SetApp(cfg.Rex.App)
		d.poll.Store(int64(cfg.Rex.Poll.Duration))
		d.restartRex()
	}
	d.cfg = cfg
	d.eng.SetConfig(cfg)
	d.warmKeys()
	d.askTrust(logger)
	d.slots = d.assigner.Assign(d.panes, cfg.Agents)
	d.eng.SetPanes(d.panes, d.slots)
	d.render(true)
	if d.status.Firmware != "" && d.status.Connected {
		d.syncPad()
	}
	logger.Printf("config reloaded")
	return nil
}

func (d *daemon) onRequest(r request, logger *log.Logger) {
	switch {
	case r.fw == nil && r.Cmd == "status":
		r.reply <- d.snapshotStatus()
	case r.Cmd == "reload":
		if err := d.reload(logger); err != nil {
			r.reply <- map[string]string{"error": err.Error()}
			return
		}
		r.reply <- map[string]string{"ok": "reloaded"}
	case r.Cmd == "pause":
		if d.pauses == 0 {
			d.eng.Reset()
		}
		d.pauses++
		d.updatePadOn()
		r.reply <- map[string]string{"ok": "paused"}
		go func() { <-r.done; d.requests <- request{Cmd: "resume"} }()
	case r.Cmd == "resume":
		if d.pauses > 0 {
			d.pauses--
		}
		if d.pauses == 0 {
			d.render(true)
		}
		d.updatePadOn()
	case r.pad != nil:
		d.onPadDone(r.pad)
	case r.padLost != nil:
		d.onPadLost(r.padLost)
	case r.fw != nil:
		d.status.Firmware, d.status.Battery = r.fw.Version, r.fw.Battery
		if d.status.Connected {
			d.syncPad() // the pad answers now; bring its keymap in line
		}
	default:
		if r.reply != nil {
			r.reply <- map[string]string{"error": "unknown command " + r.Cmd}
		}
	}
}

func (d *daemon) snapshotStatus() Status {
	s := d.status
	s.Access = hid.CheckAccess().String()
	s.Accessibility = "denied"
	if d.trusted() {
		s.Accessibility = "granted"
	}
	s.LayerIndex = d.eng.Layer()
	s.Layer = d.cfg.Layers[s.LayerIndex].Name
	s.Layers = nil
	for _, l := range d.cfg.Layers {
		s.Layers = append(s.Layers, l.Name)
	}
	s.Slots = nil
	for i, sl := range d.slots {
		ss := SlotStatus{Key: fmt.Sprintf("AG%02d", i), State: sl.State}
		if sl.Pane != nil {
			ss.Block, ss.Host, ss.Session, ss.Label, ss.App = sl.Pane.Block, sl.Pane.Host, sl.Pane.SessionLabel, sl.Pane.Label, sl.Pane.App
		}
		s.Slots = append(s.Slots, ss)
	}
	s.Selected = d.eng.Selected()
	s.Paused = d.pauses > 0
	s.ConfigErr = d.cfgErr
	return s
}

func (d *daemon) serve(ln net.Listener, logger *log.Logger) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer c.Close()
			sc := bufio.NewScanner(c)
			for sc.Scan() {
				var r request
				if json.Unmarshal(sc.Bytes(), &r) != nil {
					return
				}
				r.reply = make(chan any, 1)
				done := make(chan struct{})
				r.done = done
				d.requests <- r
				out, _ := json.Marshal(<-r.reply)
				if _, err := c.Write(append(out, '\n')); err != nil {
					close(done)
					return
				}
				if r.Cmd == "pause" {
					// Hold the pause until the client hangs up.
					_, _ = c.Read(make([]byte, 1))
					close(done)
					return
				}
			}
		}()
	}
}

// Call sends one command to a running daemon and decodes its reply into out.
func Call(cmd string, out any) error {
	c, err := net.DialTimeout("unix", SocketPath(), time.Second)
	if err != nil {
		return ErrNotRunning
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := fmt.Fprintf(c, "{\"cmd\":%q}\n", cmd); err != nil {
		return err
	}
	line, err := bufio.NewReader(c).ReadBytes('\n')
	if err != nil {
		return err
	}
	return json.Unmarshal(line, out)
}

// Pause stops a running daemon from acting on the pad until release is called.
// It returns a no-op release when no daemon runs.
func Pause() (release func(), err error) {
	c, err := net.DialTimeout("unix", SocketPath(), time.Second)
	if err != nil {
		return func() {}, nil
	}
	if _, err := fmt.Fprintln(c, `{"cmd":"pause"}`); err != nil {
		c.Close()
		return nil, err
	}
	if _, err := bufio.NewReader(c).ReadBytes('\n'); err != nil {
		c.Close()
		return nil, err
	}
	return func() { c.Close() }, nil
}

// ErrNotRunning means no daemon answered on the socket.
var ErrNotRunning = errors.New("rex-micro is not running (start it with `rex-micro service install`)")
