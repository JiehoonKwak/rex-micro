package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/JiehoonKwak/rex-micro/internal/config"
	"github.com/JiehoonKwak/rex-micro/internal/engine"
	"github.com/JiehoonKwak/rex-micro/internal/macos"
	"github.com/JiehoonKwak/rex-micro/internal/rex"
)

// enqueue hands an effect to the worker without ever blocking the main loop.
// When Rex stops answering and the backlog grows, new presses are dropped, but
// essential effects (a key's release, cleanup after unplug or pause) always
// queue, so a push-to-talk start is never left without its stop.
func (d *daemon) enqueue(f func(), essential bool) {
	if !d.effects.push(f, essential) {
		d.logger.Printf("Rex is not keeping up; dropping key actions until it does")
	}
}

func (d *daemon) runEffects() {
	for {
		f, ok := d.effects.pop()
		if !ok {
			return
		}
		f()
		d.effects.done()
		d.kick()
	}
}

// effectQueue is an ordered queue of key actions for one worker.
type effectQueue struct {
	mu      sync.Mutex
	cond    *sync.Cond
	q       []queued
	ran     int // effects finished, so drain can tell progress
	busy    bool
	closed  bool
	dropped bool
}

const effectBacklog = 16

func (e *effectQueue) init() {
	if e.cond == nil {
		e.cond = sync.NewCond(&e.mu)
	}
}

// push queues f and reports false only on the first drop of a run of drops.
func (e *effectQueue) push(f func(), essential bool) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.init()
	if e.closed {
		return true
	}
	if !essential && len(e.q) >= effectBacklog {
		first := !e.dropped
		e.dropped = true
		return !first
	}
	e.dropped = false
	e.q = append(e.q, queued{f, essential})
	e.cond.Signal()
	return true
}

func (e *effectQueue) pop() (func(), bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.init()
	for len(e.q) == 0 && !e.closed {
		e.cond.Wait()
	}
	if len(e.q) == 0 {
		return nil, false
	}
	f := e.q[0].f
	e.q = e.q[1:]
	e.busy = true
	return f, true
}

type queued struct {
	f         func()
	essential bool
}

// keepEssential drops queued effects that start something, keeping those
// that finish what a held key began. Used when the daemon stops.
func (e *effectQueue) keepEssential() {
	e.mu.Lock()
	defer e.mu.Unlock()
	keep := e.q[:0]
	for _, q := range e.q {
		if q.essential {
			keep = append(keep, q)
		}
	}
	e.q = keep
}

// finish runs what must run before the daemon exits: queued presses are
// dropped, releases kept. When one slow action holds the worker past timeout,
// the releases run here alongside it rather than not at all, so a
// push-to-talk is never left on. It reports whether the worker drained.
func (e *effectQueue) finish(timeout time.Duration) bool {
	e.keepEssential()
	if e.drain(timeout) {
		return true
	}
	e.mu.Lock()
	left := e.q
	e.q = nil
	e.mu.Unlock()
	for _, q := range left {
		q.f()
	}
	return false
}

func (e *effectQueue) done() {
	e.mu.Lock()
	e.ran++
	e.busy = false
	e.mu.Unlock()
}

// drain waits until every queued effect has run. It gives up only when no
// effect finishes for timeout, so a backlog of slow effects still drains.
func (e *effectQueue) drain(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	last := -1
	for {
		e.mu.Lock()
		idle := len(e.q) == 0 && !e.busy
		ran := e.ran
		e.mu.Unlock()
		if ran != last {
			last, deadline = ran, time.Now().Add(timeout)
		}
		if idle {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (e *effectQueue) close() {
	e.mu.Lock()
	e.init()
	e.closed = true
	e.cond.Broadcast()
	e.mu.Unlock()
}

// executor performs engine effects through Rex, macOS and the shell.
type executor struct {
	rex     *rex.Client
	post    func(macos.Shortcut) error
	hold    func(sc macos.Shortcut, down bool) error
	front   func() string
	shown   func() onScreen // the Rex pane on screen, as last seen
	logf    func(string, ...any)
}

// Shortcut posts combo unless the app it was meant for has left the front
// while the effect waited: a browser's cmd+w must not close another app's
// document.
func (x executor) Shortcut(combo, app, program string) error {
	sc, err := macos.ParseShortcut(combo)
	if err != nil {
		return err
	}
	if err := x.guard(combo, app, program); err != nil {
		return err
	}
	return x.post(sc)
}

// guard returns ErrSkipped when the app or program a shortcut was meant for
// has left the front while the effect waited.
func (x executor) guard(combo, app, program string) error {
	if now := x.front(); app != "" && config.AppName(now) != config.AppName(app) {
		x.logf("shortcut %s was for %s, but %s is in front now; skipped", combo, app, now)
		return engine.ErrSkipped
	}
	if now := x.shown().program; program != "" && now != program {
		x.logf("shortcut %s was for %s, but the Rex pane now runs %s; skipped", combo, program, now)
		return engine.ErrSkipped
	}
	return nil
}

// HoldShortcut presses or releases a shortcut held with a pad key. The
// engine calls it the moment the pad key moves, so there is no focus to
// recheck.
func (x executor) HoldShortcut(combo string, down bool) error {
	sc, err := macos.ParseShortcut(combo)
	if err != nil {
		return err
	}
	return x.hold(sc, down)
}

func (x executor) Show(p rex.Pane, zoom bool) error {
	if err := x.rex.Show(p); err != nil {
		return err
	}
	if zoom {
		return x.rex.Zoom(p.Block, true)
	}
	return nil
}
func (x executor) Perform(a string, args map[string]string) error {
	return x.rex.PerformIn(x.shown().session, a, args)
}
func (x executor) SendKeys(b string, k []string) error            { return x.rex.SendKeys(b, k...) }
func (x executor) SendText(b, t string) error                     { return x.rex.SendText(b, t) }
func (x executor) CLI(a []string) error                           { return x.rex.CLI(a...) }

// Shell runs a command. Effects run in order (push-to-talk start before stop),
// so it waits up to 10 seconds; detach starts it and moves on.
// shellTimeout bounds a shell binding that does not set detach.
var shellTimeout = 10 * time.Second

func (x executor) Shell(c string, detach bool) error {
	if detach {
		cmd := exec.Command("/bin/sh", "-c", c)
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		if err := cmd.Start(); err != nil {
			return err
		}
		go cmd.Wait()
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), shellTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", c)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	// Own process group, so a timeout stops the commands the shell started
	// too; a survivor could otherwise finish after the next key's command.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("shell command still running after %v and was stopped; add detach = true: %s", shellTimeout, c)
		}
		return err
	}
	return nil
}
