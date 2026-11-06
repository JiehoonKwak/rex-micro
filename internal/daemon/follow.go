package daemon

import (
	"context"
	"time"

	"github.com/JiehoonKwak/rex-micro/internal/config"
	"github.com/JiehoonKwak/rex-micro/internal/rex"
)

// watchRex keeps one Rex watcher running and hands its snapshots to the loop.
// A lost connection (Rex quit or restarted) is retried every poll interval;
// a config change to rex.app or rex.poll restarts the watcher at once.
func (d *daemon) watchRex() {
	for {
		ctx, cancel := context.WithCancel(context.Background())
		d.rewatch.Store(&cancel)
		err := d.rex.Watch(ctx, time.Duration(d.poll.Load()), d.refresh, func(panes []rex.Pane) {
			d.snapshots <- snapshot{panes, nil}
		})
		restarted := ctx.Err() != nil
		cancel()
		if err != nil && !restarted {
			d.snapshots <- snapshot{nil, err}
			select {
			case <-time.After(time.Duration(d.poll.Load())):
			case <-d.refresh:
			}
		}
	}
}

func (d *daemon) onSnapshot(s snapshot) {
	if s.err != nil {
		if d.status.RexErr != s.err.Error() {
			d.logger.Printf("rex: %v", s.err)
		}
		d.status.RexErr = s.err.Error()
		d.rex.ForgetApp()
		return
	}
	d.status.RexErr = ""
	d.panes = s.panes
	d.slots = d.assigner.Assign(s.panes, d.cfg.Agents)
	d.eng.SetPanes(d.panes, d.slots)
	d.followContext()
	d.render(false)
}

// pollFront watches which app is in front and hands each change to the loop.
func (d *daemon) pollFront(stop <-chan struct{}) {
	last := ""
	t := time.NewTicker(frontPoll)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		app := d.frontApp()
		if app == "" || app == last {
			continue
		}
		last = app
		select { // keep only the newest
		case <-d.fronts:
		default:
		}
		d.fronts <- app
	}
}

var frontPoll = 300 * time.Millisecond

// followContext gives the engine the app in front and, inside Rex, the
// program in the pane Rex shows, so layers with match rules take over.
func (d *daemon) followContext() {
	program := ""
	if config.AppName(d.front) == config.RexApp {
		if p := rex.Current(d.panes); p != nil {
			program = p.Program
		}
	}
	d.status.FrontApp, d.status.Program = d.front, program
	d.context.Store(&[2]string{d.front, program})
	d.eng.SetContext(d.front, program)
}
