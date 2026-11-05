// Package cli implements the rex-micro command line.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"

	rexmicro "github.com/JiehoonKwak/rex-micro"
	"github.com/JiehoonKwak/rex-micro/internal/config"
	"github.com/JiehoonKwak/rex-micro/internal/daemon"
	"github.com/JiehoonKwak/rex-micro/internal/engine"
	"github.com/JiehoonKwak/rex-micro/internal/hid"
	"github.com/JiehoonKwak/rex-micro/internal/macos"
	"github.com/JiehoonKwak/rex-micro/internal/rex"
)

// Version is set at build time with -ldflags "-X .../cli.Version=v1.2.3".
var Version = "dev"

const usage = `rex-micro turns a Work Louder Codex Micro into a controller for Rex.

Usage:
  rex-micro <command> [arguments]

Daemon:
  run                  run in the foreground (what the background service runs)
  service install      start at login with launchd (run once; upgrades apply themselves)
  service uninstall    stop and remove the launchd service
  status [--json]      what the running daemon sees: pad, layer, Agent Keys
  reload               reload the config now (the daemon also reloads on save)

Setup and checks:
  doctor               check Rex, the pad, permissions and the config
  test [--lights]      guided hardware check: press each key, confirm the lights
  monitor              print raw pad events (Ctrl-C to stop)

Configuration:
  config path          print the config file location
  config init          write the default config (keeps an existing file)
  config check [FILE]  validate a config file
  config edit          open the config in $EDITOR
  keys [--layer NAME]  show what every key does
  edit                 edit bindings and layers on screen; press a pad control to pick it
  actions              list the Rex actions a binding can use

Agents:
  skill show           print the agent skill (SKILL.md)
  skill install [DIR]  install the skill (default ~/.claude/skills)

Other:
  lights demo|off      cycle the Agent Key colors, or turn the lights off
  version              print the version
`

// Main runs the CLI and returns the exit code.
func Main(args []string) int {
	if len(args) == 0 {
		fmt.Print(usage)
		return 2
	}
	var err error
	switch args[0] {
	case "run":
		logger := log.New(os.Stderr, "", log.LstdFlags)
		if updated, err := refreshFromSource(); err != nil {
			logger.Printf("could not update from %s: %v", os.Getenv("REX_MICRO_SOURCE"), err)
		} else if updated {
			logger.Printf("installed the new version from %s; restarting", os.Getenv("REX_MICRO_SOURCE"))
			return 0 // launchd's KeepAlive starts the new copy
		}
		err = daemon.Run(config.Path(), logger, refreshFromSource)
	case "status":
		err = status(len(args) > 1 && args[1] == "--json")
	case "reload":
		var out map[string]string
		if err = daemon.Call("reload", &out); err == nil {
			if e := out["error"]; e != "" {
				err = errors.New(e)
			} else {
				fmt.Println("reloaded")
			}
		}
	case "doctor":
		return doctor()
	case "test":
		err = hardwareTest(args[1:])
	case "monitor":
		err = monitor()
	case "config":
		err = configCmd(args[1:])
	case "keys":
		err = keys(args[1:])
	case "edit":
		err = edit()
	case "actions":
		err = actions()
	case "service":
		err = service(args[1:])
	case "skill":
		err = skill(args[1:])
	case "lights":
		err = lights(args[1:])
	case "_rpc": // hidden: send raw JSON lines to the pad and print what comes back
		err = rawRPC(args[1:])
	case "_access": // hidden: report this executable's own Input Monitoring state
		return accessProbe(len(args) > 1 && args[1] == "--request")
	case "_trusted": // hidden: exit 0 when this executable has Accessibility
		if macos.Trusted() {
			return 0
		}
		return 1
	case "version", "--version", "-v":
		fmt.Println("rex-micro", Version)
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "rex-micro: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "rex-micro:", err)
		return 1
	}
	return 0
}

func status(asJSON bool) error {
	var s daemon.Status
	if err := daemon.Call("status", &s); err != nil {
		return err
	}
	if asJSON {
		out, _ := json.MarshalIndent(s, "", "  ")
		fmt.Println(string(out))
		return nil
	}
	pad := "not connected"
	if s.Connected {
		pad = fmt.Sprintf("connected over %s, firmware %s, battery %.0f%%", s.Transport, s.Firmware, s.Battery)
	}
	fmt.Printf("pad:     %s\n", pad)
	fmt.Printf("layer:   %s (%d of %d)\n", s.Layer, s.LayerIndex+1, len(s.Layers))
	fmt.Printf("access:  Input Monitoring %s\n", s.Access)
	if s.Paused {
		fmt.Println("paused:  yes (a `rex-micro test` is running)")
	}
	for _, e := range []struct{ name, v string }{{"config", s.ConfigErr}, {"rex", s.RexErr}, {"last", s.LastErr}} {
		if e.v != "" {
			fmt.Printf("%s error: %s\n", e.name, e.v)
		}
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "\nKEY\tSTATE\tAGENT\tSESSION\tPANE")
	for _, sl := range s.Slots {
		mark := ""
		if sl.Block != "" && sl.Block == s.Selected {
			mark = " *"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s%s\n", sl.Key, sl.State, dash(sl.App), dash(sl.Session), shortBlock(sl.Block), mark)
	}
	return w.Flush()
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func shortBlock(b string) string {
	if len(b) > 8 {
		return b[len(b)-8:]
	}
	return dash(b)
}

func configCmd(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: rex-micro config path|init|check|edit")
	}
	path := config.Path()
	switch args[0] {
	case "path":
		fmt.Println(path)
	case "init":
		if _, err := os.Stat(path); err == nil {
			fmt.Printf("%s already exists; left unchanged\n", path)
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(config.Default), 0o644); err != nil {
			return err
		}
		fmt.Println("wrote", path)
	case "check":
		if len(args) > 1 {
			path = args[1]
		}
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			fmt.Printf("%s does not exist; the built-in default is in use\n", path)
			return nil
		} else if err != nil {
			return err
		}
		if _, err := config.Parse(string(data)); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		fmt.Printf("%s: ok\n", path)
	case "edit":
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			if err := configCmd([]string{"init"}); err != nil {
				return err
			}
		}
		editor := os.Getenv("EDITOR")
		if editor == "" {
			editor = "vi"
		}
		cmd := exec.Command("/bin/sh", "-c", editor+` "$0"`, path)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return err
		}
		return configCmd([]string{"check"})
	default:
		return fmt.Errorf("unknown config command %q", args[0])
	}
	return nil
}

func keys(args []string) error {
	cfg, err := config.Load(config.Path())
	if err != nil {
		return err
	}
	only := ""
	if len(args) == 2 && args[0] == "--layer" {
		only = args[1]
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for i, layer := range cfg.Layers {
		if only != "" && layer.Name != only {
			continue
		}
		when := ""
		if d := layer.Match.Describe(); d != "" {
			when = " (on when " + d + ")"
		}
		fmt.Fprintf(w, "layer %d: %s%s\n", i+1, layer.Name, when)
		for _, k := range config.KeyNames {
			fmt.Fprintf(w, "  %s\t%s\n", k, describe(cfg, i, k))
		}
	}
	return w.Flush()
}

// describe explains key k on layer i without driving the engine.
func describe(cfg *config.Config, layer int, k string) string {
	c := *cfg
	c.Layers = append([]config.Layer{}, cfg.Layers...)
	// Put the wanted layer's keys over the base so fall-through shows.
	merged := config.Layer{Name: cfg.Layers[layer].Name, Keys: map[string]*config.KeyBinding{}}
	for n, b := range cfg.Layers[0].Keys {
		merged.Keys[n] = b
	}
	for n, b := range cfg.Layers[layer].Keys {
		merged.Keys[n] = b
	}
	c.Layers[0] = merged
	return engine.New(&c, nil, nil, nil).Describe(k)
}

func skill(args []string) error {
	if len(args) == 0 || args[0] == "show" {
		fmt.Print(rexmicro.Skill)
		return nil
	}
	if args[0] != "install" {
		return fmt.Errorf("unknown skill command %q", args[0])
	}
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".claude", "skills")
	if len(args) > 1 {
		dir = args[1]
	}
	dest := filepath.Join(dir, "rex-micro", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(dest, []byte(rexmicro.Skill), 0o644); err != nil {
		return err
	}
	fmt.Println("installed", dest)
	return nil
}

func prompt(r io.Reader, q string) string {
	fmt.Print(q)
	var b strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		if n == 0 || err != nil || buf[0] == '\n' {
			break
		}
		b.WriteByte(buf[0])
	}
	return strings.TrimSpace(b.String())
}

func actions() error {
	cfg, err := config.Load(config.Path())
	if err != nil {
		return err
	}
	rc := &rex.Client{Runner: rex.ExecRunner{Path: daemon.RexPath()}, App: cfg.Rex.App}
	app, err := rc.AppClient()
	if err != nil {
		return err
	}
	cmd := rex.Command(context.Background(), daemon.RexPath(), "actions", "-C", app)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func accessProbe(request bool) int {
	exe, _ := os.Executable()
	a := hid.CheckAccess()
	if a != hid.AccessGranted && request {
		hid.RequestAccess()
		a = hid.CheckAccess()
	}
	fmt.Printf("%s version=%s access=%s\n", exe, Version, a)
	if a != hid.AccessGranted {
		return 1
	}
	return 0
}
