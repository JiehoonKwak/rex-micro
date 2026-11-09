package daemon

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/JiehoonKwak/rex-micro/internal/config"
	"github.com/JiehoonKwak/rex-micro/internal/micro"
	"github.com/JiehoonKwak/rex-micro/internal/padmap"
)

// padPort is the pad I/O the worker does; tests replace it.
type padPort interface {
	padmap.Pad
	Status(timeout time.Duration) (micro.Status, error)
	FocusApp(process string) error
}

// With [pad] layers = "sync", the pad's hardware layer and the engine's layer
// are kept equal: a layer the engine turns on is sent to the pad (its LEDs
// show it), and a layer picked with the touch sensor, read by polling
// device.status, turns on in the engine as if picked by hand. The engine stays
// the one owner of the active layer; the pad is its input and display.
//
// One worker goroutine does all of this pad I/O, so a keymap write, a layer
// notice and a poll never interleave.

var (
	padPoll = 250 * time.Millisecond
	// padSettle is how long a layer sent to the pad may take to show in a poll;
	// until then a different layer read back is the old one, not a touch.
	padSettle = 1500 * time.Millisecond
)

// padBackup is where the keymap sync replaced is kept.
func padBackup() string { return filepath.Join(filepath.Dir(SocketPath()), "pad-keymap.json") }

type padJob struct {
	gen    int
	on     bool
	layers []config.Layer
}

type padDone struct {
	gen   int
	on    bool
	wrote bool
	err   error
}

// keepNewest replaces whatever is waiting in a one-slot channel.
func keepNewest[T any](ch chan T, v T) {
	select {
	case <-ch:
	default:
	}
	select {
	case ch <- v:
	default:
	}
}

func (d *daemon) padWorker(stop <-chan struct{}) {
	t := time.NewTicker(padPoll)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case job := <-d.padJobs:
			wrote, err := padmap.Sync(d.padIO, job.on, job.layers, padBackup())
			select {
			case d.requests <- request{pad: &padDone{gen: job.gen, on: job.on, wrote: wrote, err: err}}:
			case <-stop:
				return
			}
		case i := <-d.padTell:
			if err := d.tell(i); err != nil {
				// Polls would now show the old layer and undo the engine's;
				// stop following the pad until it is synced again.
				select {
				case d.requests <- request{padLost: fmt.Errorf("could not show layer %d on the pad: %w", i+1, err)}:
				case <-stop:
					return
				}
			}
		case <-t.C:
			if !d.padOn.Load() {
				continue
			}
			if st, err := d.padIO.Status(time.Second); err == nil {
				keepNewest(d.padSeen, st.LayerIndex)
			}
		}
	}
}

// tell shows layer i on the pad, retrying a notice that got no reply.
func (d *daemon) tell(i int) error {
	var err error
	for try := 0; try < 3; try++ {
		if err = d.padIO.FocusApp(padmap.Link(i)); err == nil {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return err
}

// onPadLost stops following the pad's layer after a notice failed.
func (d *daemon) onPadLost(err error) {
	if !d.padSynced {
		return
	}
	d.padSynced = false
	d.status.PadLayers = "error: " + err.Error() + " (reconnect the pad or save the config to retry)"
	d.logger.Printf("pad layers: %v; no longer following the pad's layer", err)
	d.updatePadOn()
}

// syncPad has the worker bring the pad's keymap in line with the config. It
// runs when the pad connects and on every reload; with nothing to change it
// only reads the keymap.
func (d *daemon) syncPad() {
	d.padGen++
	d.padSynced = false
	d.status.PadLayers = "syncing"
	d.updatePadOn()
	keepNewest(d.padJobs, padJob{gen: d.padGen, on: d.cfg.SyncLayers(), layers: d.cfg.Layers})
}

func (d *daemon) onPadDone(r *padDone) {
	if r.gen != d.padGen {
		return // the config changed while it ran; a newer job follows
	}
	switch {
	case r.err != nil:
		d.status.PadLayers = "error: " + r.err.Error()
		d.logger.Printf("pad layers: %v", r.err)
	case r.on:
		d.padSynced = true
		d.status.PadLayers = "sync"
		if r.wrote {
			d.logger.Printf("pad layers: stored %d layers on the pad", min(len(d.cfg.Layers), padmap.MaxLayers))
		}
		d.tellPad(d.eng.Layer())
	default:
		d.status.PadLayers = "off"
		if r.wrote {
			d.logger.Printf("pad layers: wrote the pad's previous keymap back")
		}
	}
	d.updatePadOn()
}

// updatePadOn starts or stops polling the pad's layer.
func (d *daemon) updatePadOn() {
	d.padOn.Store(d.padSynced && d.status.Connected && d.pauses == 0)
}

// tellPad shows layer i on the pad.
func (d *daemon) tellPad(i int) {
	if !d.padSynced {
		return
	}
	d.padWant, d.padToldAt = i, time.Now()
	keepNewest(d.padTell, i)
}

// onPadLayer handles the pad's layer as polled (1-based, as device.status
// counts). The layer last sent to the pad, read back, confirms it. Any other
// layer is a touch, unless a layer sent to the pad may still be on its way.
func (d *daemon) onPadLayer(index int) {
	i := index - 1
	if !d.padOn.Load() || i < 0 || i >= len(d.cfg.Layers) {
		return
	}
	if i == d.padWant {
		d.padToldAt = time.Time{} // landed; the next different layer is a touch
	}
	if i == d.eng.Layer() {
		return
	}
	if i != d.padWant && time.Since(d.padToldAt) < padSettle {
		return
	}
	d.eng.SelectLayer(i) // OnLayer sends it back to the pad, which already shows it
	d.padToldAt = time.Time{}
}
