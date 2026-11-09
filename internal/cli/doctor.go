package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/JiehoonKwak/rex-micro/internal/config"
	"github.com/JiehoonKwak/rex-micro/internal/daemon"
	"github.com/JiehoonKwak/rex-micro/internal/hid"
	"github.com/JiehoonKwak/rex-micro/internal/micro"
	"github.com/JiehoonKwak/rex-micro/internal/rex"
)

type checker struct{ failed, warned int }

func (c *checker) ok(name, detail string) { fmt.Printf("  ok    %-18s %s\n", name, detail) }
func (c *checker) warn(name, detail string) {
	c.warned++
	fmt.Printf("  warn  %-18s %s\n", name, detail)
}
func (c *checker) fail(name, detail string) {
	c.failed++
	fmt.Printf("  FAIL  %-18s %s\n", name, detail)
}

func doctor() int {
	c := &checker{}
	fmt.Println("rex-micro doctor", Version)

	// Config.
	path := config.Path()
	cfg, err := config.Load(path)
	switch {
	case err != nil:
		c.fail("config", fmt.Sprintf("%s: %v", path, err))
	default:
		if _, statErr := os.Stat(path); errors.Is(statErr, os.ErrNotExist) {
			c.ok("config", "built-in default (run `rex-micro config init` to customize)")
		} else {
			c.ok("config", path)
		}
	}

	// Rex.
	rc := &rex.Client{Runner: rex.ExecRunner{Path: daemon.RexPath()}}
	if cfg != nil {
		rc.App = cfg.Rex.App
	}
	if v, err := rc.Version(); err != nil {
		c.fail("rex", "the rex CLI is not reachable: "+err.Error())
	} else {
		c.ok("rex", firstLine(v))
		if app, err := rc.AppClient(); err != nil {
			c.fail("rex app", err.Error())
		} else {
			c.ok("rex app", app)
		}
		if panes, err := rc.Snapshot(); err != nil {
			c.fail("rex agents", err.Error())
		} else {
			n := 0
			for _, p := range panes {
				if p.Agent() {
					n++
				}
			}
			c.ok("rex agents", fmt.Sprintf("%d panes, %d running an agent", len(panes), n))
		}
	}

	// Daemon.
	var st daemon.Status
	daemonUp := daemon.Call("status", &st) == nil
	if daemonUp {
		c.ok("daemon", fmt.Sprintf("running (pid %d, %s)", st.PID, st.Executable))
		if st.Executable != StablePath() {
			c.warn("daemon path", "not running from "+StablePath()+"; Input Monitoring will be asked again after upgrades. Run `rex-micro service install`")
		}
		if st.Access != "granted" {
			c.fail("daemon access", "Input Monitoring is "+st.Access+" for "+st.Executable+
				"; allow it in System Settings > Privacy & Security > Input Monitoring, then restart the service")
			openInputMonitoring()
		} else {
			c.ok("daemon access", "Input Monitoring granted")
		}
		if st.Connected {
			c.ok("daemon pad", fmt.Sprintf("connected over %s, firmware %s", st.Transport, st.Firmware))
			if strings.HasPrefix(st.PadLayers, "error") {
				c.fail("pad layers", strings.TrimPrefix(st.PadLayers, "error: "))
			} else if st.PadLayers == "sync" {
				c.ok("pad layers", "stored on the pad; the touch sensor switches them")
			}
		} else {
			c.warn("daemon pad", "the daemon does not see the pad; plug it in or pair it")
		}
		switch {
		case st.Accessibility == "granted":
			c.ok("daemon shortcuts", "Accessibility granted")
		case cfg != nil && cfg.UsesShortcut():
			c.fail("daemon shortcuts", "shortcut bindings need Accessibility for "+st.Executable+
				"; allow it in System Settings > Privacy & Security > Accessibility")
			_ = exec.Command("open", "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility").Run()
		default:
			c.ok("daemon shortcuts", "no shortcut bindings (Accessibility not needed)")
		}
		if st.FrontApp != "" {
			detail := st.FrontApp
			if st.Program != "" {
				detail += ", program " + st.Program
			}
			c.ok("daemon context", detail+" -> layer "+st.Layer)
		}
		if t, err := time.Parse(time.RFC3339, st.Foreign); err == nil && time.Since(t) < 2*time.Minute {
			c.fail("other app", fmt.Sprintf("another app changed the Agent Key lights %s ago; remove ChatGPT from Input Monitoring and restart it, and quit Work Louder Input", time.Since(t).Round(time.Second)))
		}
		if st.RexErr != "" {
			c.fail("daemon rex", st.RexErr)
		}
		if st.ConfigErr != "" {
			c.fail("daemon config", st.ConfigErr)
		}
	} else {
		c.warn("daemon", "not running; start it with `rex-micro service install`")
	}

	// The pad, from this process.
	access := hid.CheckAccess()
	if access != hid.AccessGranted {
		c.warn("terminal access", "this terminal lacks Input Monitoring ("+access.String()+"), so doctor cannot talk to the pad itself")
	} else if dev, err := hid.Open(); err != nil {
		c.fail("pad", err.Error())
	} else {
		pad := micro.New(dev)
		deadline := time.After(1500 * time.Millisecond)
		connected := false
	wait:
		for {
			select {
			case ev := <-pad.Events():
				if ev.Connected {
					connected = true
					break wait
				}
			case <-deadline:
				break wait
			}
		}
		if !connected {
			c.fail("pad", "no Codex Micro found over USB or Bluetooth")
		} else if s, err := pad.Status(2 * time.Second); err != nil {
			c.fail("pad", "found over "+pad.Transport()+" but device.status got no reply: "+err.Error())
		} else {
			c.ok("pad", fmt.Sprintf("%s, firmware %s, battery %.0f%%", pad.Transport(), s.Version, s.Battery))
		}
		pad.Close()
	}

	// Apps that also drive the pad.
	if running("ChatGPT") {
		c.warn("ChatGPT", "running; it shares the pad unless it lacks Input Monitoring. Turn ChatGPT off under System Settings > Privacy & Security > Input Monitoring and restart it")
	}
	if running("input") {
		if cfg != nil && cfg.SyncLayers() {
			c.fail("Work Louder Input", "running while pad.layers = \"sync\"; it rewrites the pad's keymap and switches its layers. Quit it")
		} else {
			c.warn("Work Louder Input", "running; it is not needed and can fight over the pad. Quit it")
		}
	}

	// Signature: decides whether Input Monitoring survives updates.
	if exe, err := os.Executable(); err == nil {
		out, _ := exec.Command("codesign", "-dvv", exe).CombinedOutput()
		switch {
		case strings.Contains(string(out), "Signature=adhoc"):
			c.warn("signature", "ad-hoc signed: macOS may ask for Input Monitoring again after each update")
		case strings.Contains(string(out), "Authority="):
			c.ok("signature", authority(string(out)))
		}
	}

	fmt.Println()
	switch {
	case c.failed > 0:
		fmt.Printf("%d problem(s), %d warning(s)\n", c.failed, c.warned)
		return 1
	case c.warned > 0:
		fmt.Printf("ok with %d warning(s)\n", c.warned)
	default:
		fmt.Println("all good")
	}
	return 0
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

func authority(codesign string) string {
	for _, l := range strings.Split(codesign, "\n") {
		if strings.HasPrefix(l, "Authority=") {
			return strings.TrimPrefix(l, "Authority=")
		}
	}
	return "signed"
}

func running(name string) bool {
	return exec.Command("pgrep", "-x", name).Run() == nil
}

func openInputMonitoring() {
	_ = exec.Command("open", "x-apple.systempreferences:com.apple.preference.security?Privacy_ListenEvent").Start()
}
