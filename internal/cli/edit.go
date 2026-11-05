package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"

	"github.com/JiehoonKwak/rex-micro/internal/config"
	"github.com/JiehoonKwak/rex-micro/internal/daemon"
	"github.com/JiehoonKwak/rex-micro/internal/editor"
	"github.com/JiehoonKwak/rex-micro/internal/engine"
	"github.com/JiehoonKwak/rex-micro/internal/hid"
	"github.com/JiehoonKwak/rex-micro/internal/macos"
	"github.com/JiehoonKwak/rex-micro/internal/micro"
	"github.com/JiehoonKwak/rex-micro/internal/protocol"
	"github.com/JiehoonKwak/rex-micro/internal/rex"
)

// edit runs the full-screen config editor.
func edit() error {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("edit needs a terminal")
	}
	path := config.Path()
	data, err := os.ReadFile(path)
	text := string(data)
	if errors.Is(err, os.ErrNotExist) {
		text = config.Default
	} else if err != nil {
		return err
	}
	cfg, err := config.Parse(text)
	if err != nil {
		return fmt.Errorf("%s: %w; fix it first", path, err)
	}

	disk := string(data) // what the file held when last read or written; "" = missing
	opts := editor.Options{Path: path, Text: text, Apps: macos.RunningApps, Save: func(t string, force bool) error {
		if err := saveConfig(path, t, disk, force); err != nil {
			return err
		}
		disk = t
		return nil
	}}
	rc := &rex.Client{Runner: rex.ExecRunner{Path: daemon.RexPath()}, App: cfg.Rex.App}
	opts.Actions = rexActions(rc)
	if panes, err := rc.Snapshot(); err == nil {
		seen := map[string]bool{}
		for _, p := range panes {
			if p.Program != "" && !seen[p.Program] {
				seen[p.Program] = true
				opts.Programs = append(opts.Programs, p.Program)
			}
		}
		sort.Strings(opts.Programs)
	}

	// Read the pad ourselves, with the daemon paused so presses only select.
	if hid.CheckAccess() != hid.AccessGranted {
		opts.PadNote = "This terminal lacks Input Monitoring, so pad presses cannot select keys; use the arrow keys."
	} else if release, err := daemon.Pause(); err != nil {
		opts.PadNote = "Could not pause the daemon: " + err.Error()
	} else {
		defer release()
		if dev, err := hid.Open(); err != nil {
			opts.PadNote = "Pad not found (" + err.Error() + "); use the arrow keys."
		} else {
			pad := micro.New(dev)
			defer pad.Close()
			opts.Pad = controls(pad, cfg.Stick)
		}
	}

	m, err := editor.New(opts)
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

// controls turns pad events into the names of the controls pressed.
func controls(pad *micro.Pad, stick config.StickConfig) <-chan string {
	out := make(chan string, 8)
	go func() {
		defer close(out)
		dir := ""
		for ev := range pad.Events() {
			in := ev.Input
			if in == nil {
				continue
			}
			name := ""
			switch in.Kind {
			case protocol.InputKey:
				if in.Down {
					name = in.Key
					if name == "ACT10" || name == "ACT11" {
						name = "MIC"
					}
				}
			case protocol.InputDial:
				name = in.Key
			case protocol.InputStick:
				switch {
				case in.Dist < stick.Threshold*0.5:
					dir = ""
				case in.Dist >= stick.Threshold && dir == "":
					dir = engine.Direction(in.Angle, stick.AngleOffset)
					name = "STICK_" + dir
				}
			}
			if name != "" {
				select {
				case out <- name:
				default:
				}
			}
		}
	}()
	return out
}

// rexActions lists the actions Rex offers: config actions (rex.action in
// init.lua) and the app's own.
func rexActions(rc *rex.Client) []editor.RexAction {
	var out []editor.RexAction
	seen := map[string]bool{}
	add := func(args ...string) {
		raw, err := rex.Command(context.Background(), daemon.RexPath(), append([]string{"actions", "--json"}, args...)...).Output()
		if err != nil {
			return
		}
		var list struct {
			Actions []struct {
				Name     string `json:"name"`
				Title    string `json:"title"`
				Category string `json:"category"`
				Args     struct {
					Properties map[string]any `json:"properties"`
					Required   []string       `json:"required"`
				} `json:"args"`
			} `json:"actions"`
		}
		if json.Unmarshal(raw, &list) != nil {
			return
		}
		for _, a := range list.Actions {
			if seen[a.Name] {
				continue
			}
			seen[a.Name] = true
			ra := editor.RexAction{Name: a.Name, Title: a.Title, Category: a.Category}
			for k := range a.Args.Properties {
				ra.Args = append(ra.Args, k)
			}
			sort.Strings(ra.Args)
			if ra.Title == "" {
				ra.Title = a.Name
			}
			out = append(out, ra)
		}
	}
	add()
	if app, err := rc.AppClient(); err == nil {
		add("-C", app)
	}
	return out
}

// saveConfig writes the file in place of the old one and asks the daemon to
// reload it. It refuses when the file no longer holds expect (someone edited
// it meanwhile) unless force, and writes through symlinks, even dangling
// ones, so a dotfiles link stays a link.
func saveConfig(path, text, expect string, force bool) error {
	if _, err := config.Parse(text); err != nil {
		return err
	}
	target, err := resolveLink(path)
	if err != nil {
		return err
	}
	cur, err := os.ReadFile(target)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if !force && string(cur) != expect {
		return editor.ErrChanged
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".config-*.toml")
	if err != nil {
		return err
	}
	if _, err := tmp.WriteString(text); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	tmp.Close()
	if st, err := os.Stat(target); err == nil {
		_ = os.Chmod(tmp.Name(), st.Mode().Perm())
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	var out map[string]string
	switch err := daemon.Call("reload", &out); {
	case errors.Is(err, daemon.ErrNotRunning):
		return nil
	case err != nil:
		return fmt.Errorf("saved, but the daemon did not reload: %w", err)
	case out["error"] != "":
		return errors.New("saved, but the daemon rejected it: " + out["error"])
	}
	return nil
}

// resolveLink follows symlinks from path to the file they name, which need
// not exist yet.
func resolveLink(path string) (string, error) {
	for range 16 {
		fi, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return path, nil
		}
		if err != nil {
			return "", err
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			return path, nil
		}
		link, err := os.Readlink(path)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(link) {
			// Relative to where the link really lives, which matters when
			// a parent directory is itself a symlink.
			dir := filepath.Dir(path)
			if real, err := filepath.EvalSymlinks(dir); err == nil {
				dir = real
			}
			link = filepath.Join(dir, link)
		}
		path = link
	}
	return "", fmt.Errorf("%s: too many symlinks", path)
}
