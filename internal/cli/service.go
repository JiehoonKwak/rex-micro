package cli

import (
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/JiehoonKwak/rex-micro/internal/config"
)

const label = "io.github.jiehoonkwak.rex-micro"

func plistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist")
}

func logPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Logs", "rex-micro.log")
}

// stableExe prefers Homebrew's version-independent opt path, so the service and
// its Input Monitoring grant survive upgrades.
func stableExe() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, _ = filepath.EvalSymlinks(exe)
	if i := strings.Index(exe, "/Cellar/rex-micro/"); i >= 0 {
		opt := exe[:i] + "/opt/rex-micro/bin/rex-micro"
		if _, err := os.Stat(opt); err == nil {
			return opt, nil
		}
	}
	return exe, nil
}

// serviceEnv carries settings that move the config or the rex CLI into the
// service, so the daemon reads the same file `rex-micro edit` writes.
func serviceEnv() string {
	var b strings.Builder
	if os.Getenv("REX_MICRO_CONFIG") != "" || os.Getenv("XDG_CONFIG_HOME") != "" {
		p, _ := filepath.Abs(config.Path())
		fmt.Fprintf(&b, "\n    <key>REX_MICRO_CONFIG</key><string>%s</string>", xmlText(p))
	}
	if r := os.Getenv("REX_MICRO_REX"); r != "" {
		fmt.Fprintf(&b, "\n    <key>REX_MICRO_REX</key><string>%s</string>", xmlText(r))
	}
	return b.String()
}

func xmlText(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func service(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: rex-micro service install|uninstall")
	}
	uid := fmt.Sprint(os.Getuid())
	switch args[0] {
	case "install":
		src, err := stableExe()
		if err != nil {
			return err
		}
		exe := StablePath()
		if err := copyExecutable(src, exe); err != nil {
			return fmt.Errorf("copy to %s: %w", exe, err)
		}
		plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key><array><string>%s</string><string>run</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ProcessType</key><string>Interactive</string>
  <key>EnvironmentVariables</key><dict>
    <key>PATH</key><string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
    <key>REX_MICRO_SOURCE</key><string>%s</string>%s
  </dict>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`, label, xmlText(exe), xmlText(src), serviceEnv(), xmlText(logPath()), xmlText(logPath()))
		if err := os.MkdirAll(filepath.Dir(plistPath()), 0o755); err != nil {
			return err
		}
		_ = exec.Command("launchctl", "bootout", "gui/"+uid+"/"+label).Run()
		if err := os.WriteFile(plistPath(), []byte(plist), 0o644); err != nil {
			return err
		}
		// bootout finishes asynchronously; retry until the old job is gone.
		var out []byte
		for i := 0; i < 20; i++ {
			if out, err = exec.Command("launchctl", "bootstrap", "gui/"+uid, plistPath()).CombinedOutput(); err == nil {
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
		if err != nil {
			return fmt.Errorf("launchctl bootstrap: %s", strings.TrimSpace(string(out)))
		}
		fmt.Printf("service installed and started\n  runs:    %s\n  updates: from %s (checked every 30s)\n  logs:    %s\n", exe, src, logPath())
		fmt.Println("If macOS asks, allow rex-micro under Privacy & Security > Input Monitoring, then run `rex-micro doctor`.")
	case "uninstall":
		_ = exec.Command("launchctl", "bootout", "gui/"+uid+"/"+label).Run()
		if err := os.Remove(plistPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		_ = os.Remove(StablePath())
		fmt.Println("service removed")
	default:
		return fmt.Errorf("unknown service command %q", args[0])
	}
	return nil
}
