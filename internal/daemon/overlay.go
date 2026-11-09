package daemon

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/JiehoonKwak/rex-micro/internal/config"
	"github.com/JiehoonKwak/rex-micro/internal/keysheet"
	"github.com/JiehoonKwak/rex-micro/internal/macos"
)

// keysView shows the keys overlay (builtin keys.show). show with "" hides it.
type keysView interface {
	show(page string)
}

// overlayHelper runs `rex-micro _overlay`, the window process, and feeds it
// pages. AppKit wants the main thread, which the daemon's loop does not give
// it, and a crash in the window stays out of the daemon. It starts on first
// use (or warm, when the config uses keys.show) and again if it died. With no
// executable (Options.Self empty, as in tests) it starts nothing.
type overlayHelper struct {
	exe  string
	logf func(string, ...any)

	mu   sync.Mutex
	cmd  *exec.Cmd
	in   io.WriteCloser
	done map[*exec.Cmd]chan struct{} // closed when that helper has exited
}

func (o *overlayHelper) start() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.startLocked()
}

var errNoHelper = errors.New("no rex-micro executable to run the overlay with")

func (o *overlayHelper) startLocked() error {
	if o.cmd != nil {
		return nil
	}
	if o.exe == "" {
		return errNoHelper
	}
	cmd := exec.Command(o.exe, "_overlay")
	cmd.Stderr = os.Stderr // the daemon's log
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	o.cmd, o.in = cmd, in
	if o.done == nil {
		o.done = map[*exec.Cmd]chan struct{}{}
	}
	done := make(chan struct{})
	o.done[cmd] = done
	go func() {
		_ = cmd.Wait()
		close(done)
		o.mu.Lock()
		if o.cmd == cmd { // it died on its own; the next show starts another
			o.cmd, o.in = nil, nil
			delete(o.done, cmd)
		}
		o.mu.Unlock()
	}()
	return nil
}

// warmKeys starts the overlay helper ahead of the first keys.show, when the
// config has one, so the first hold shows at once.
func (d *daemon) warmKeys() {
	if h, ok := d.keys.(*overlayHelper); ok && usesKeysShow(d.cfg.Layers) {
		go func() { _ = h.start() }()
	}
}

func (o *overlayHelper) show(page string) {
	c := macos.OverlayCommand{HTML: page}
	if page != "" {
		c.Width, c.Height = keysheet.Width, keysheet.Height
	}
	line, _ := json.Marshal(c)
	line = append(line, '\n')
	o.mu.Lock()
	defer o.mu.Unlock()
	for try := 0; try < 2; try++ {
		if page == "" && o.cmd == nil {
			return // nothing shown
		}
		if err := o.startLocked(); err != nil {
			if err != errNoHelper {
				o.logf("keys overlay: %v", err)
			}
			return
		}
		if _, err := o.in.Write(line); err == nil {
			return
		}
		o.endLocked() // stuck or gone; start a fresh one
	}
}

// stop ends the helper and waits for it to be gone.
func (o *overlayHelper) stop() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.endLocked()
}

// endLocked closes the helper's input, which ends it, and kills it if it is
// still there a second later; either way it has exited on return.
func (o *overlayHelper) endLocked() {
	cmd, in := o.cmd, o.in
	o.cmd, o.in = nil, nil
	if cmd == nil {
		return
	}
	_ = in.Close()
	select {
	case <-o.done[cmd]:
	case <-time.After(time.Second):
		_ = cmd.Process.Kill()
		<-o.done[cmd]
	}
	delete(o.done, cmd)
}

// onKeys answers the engine's keys.show: it draws the active layer, or hides.
func (d *daemon) onKeys(show bool) {
	page := ""
	if show {
		page = keysheet.HTML(keysheet.Build(d.eng, d.cfg))
	}
	keepNewest(d.keysOut, page)
}

// keysWorker hands pages to the view off the loop, newest first.
func (d *daemon) keysWorker(stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case page := <-d.keysOut:
			d.keys.show(page)
		}
	}
}

// usesKeysShow reports whether any binding shows the keys overlay, so the
// helper can start before the first hold.
func usesKeysShow(layers []config.Layer) bool {
	for _, l := range layers {
		for _, b := range l.Keys {
			for _, g := range []string{"press", "double", "hold", "down", "up"} {
				if a := b.Gesture(g); a != nil && a.Builtin == "keys.show" {
					return true
				}
			}
		}
	}
	return false
}
