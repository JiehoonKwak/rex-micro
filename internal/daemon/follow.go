package daemon

import (
	"context"
	"time"

	"github.com/JiehoonKwak/rex-micro/internal/config"
	"github.com/JiehoonKwak/rex-micro/internal/rex"
)

// watchRex keeps one Rex watcher running and hands its snapshots to the loop
// until ctx ends. A lost connection (Rex quit or restarted) is retried every
// poll interval; restartRex restarts it at once, also during that wait.
func (d *daemon) watchRex(ctx context.Context) {
	defer close(d.watchDone)
	send := func(s snapshot) bool {
		select {
		case d.snapshots <- s:
			return true
		case <-ctx.Done():
			return false
		}
	}
	for ctx.Err() == nil {
		select { // a restart asked for before this run began is this run
		case <-d.rewake:
		default:
		}
		run, cancel := context.WithCancel(ctx)
		d.rewatch.Store(&cancel)
		err := d.rex.Watch(run, time.Duration(d.poll.Load()), d.refresh, func(panes []rex.Pane) {
			send(snapshot{panes, nil})
		})
		restarted := run.Err() != nil
		cancel()
		if err == nil || restarted {
			continue
		}
		if !send(snapshot{nil, err}) {
			return
		}
		select {
		case <-time.After(time.Duration(d.poll.Load())):
		case <-d.refresh:
		case <-d.rewake:
		case <-ctx.Done():
		}
	}
}

// restartRex makes watchRex start over with the current [rex] settings.
func (d *daemon) restartRex() {
	if stop := d.rewatch.Load(); stop != nil {
		(*stop)()
	}
	select {
	case d.rewake <- struct{}{}:
	default:
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
	var shown onScreen
	if p := rex.Current(d.panes); p != nil {
		if config.AppName(d.front) == config.RexApp {
			shown.program = p.Program
		}
		if p.Host == "" { // the app's own server runs the config actions
			shown.session = p.Session
		}
	}
	d.status.FrontApp, d.status.Program = d.front, shown.program
	d.context.Store(&shown)
	d.eng.SetContext(d.front, shown.program)
}

// onScreen is what the effect worker needs from the loop: the program in the
// Rex pane in front (empty unless Rex is in front) and the session the app
// shows when it lives on this Mac's server.
type onScreen struct{ program, session string }
