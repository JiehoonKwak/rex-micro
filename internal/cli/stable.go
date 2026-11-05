package cli

import (
	"bytes"
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"
)

// macOS remembers Input Monitoring per executable path and code signature,
// for the path launchd spawned (exec'ing another file afterwards does not
// count). Homebrew installs each version under a new Cellar path, so the
// service runs a copy at one fixed path, and the daemon refreshes that copy
// from the installed binary (REX_MICRO_SOURCE) when it changes.

func stableDir() string {
	if d := os.Getenv("REX_MICRO_BIN_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Application Support", "rex-micro", "bin")
}

// StablePath is where the service's executable lives.
func StablePath() string { return filepath.Join(stableDir(), "rex-micro") }

// refreshFromSource copies REX_MICRO_SOURCE over this executable when they
// differ. It reports true when a new version was installed, so the caller
// exits and launchd restarts the daemon from the same path.
func refreshFromSource() (bool, error) {
	src := os.Getenv("REX_MICRO_SOURCE")
	if src == "" {
		return false, nil
	}
	src, err := filepath.EvalSymlinks(src)
	if err != nil {
		return false, nil // uninstalled or mid-upgrade; try again later
	}
	self, err := os.Executable()
	if err != nil {
		return false, err
	}
	self, _ = filepath.EvalSymlinks(self)
	if src == self || sameContent(src, self) {
		return false, nil
	}
	return true, copyExecutable(src, self)
}

func digest(path string) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil
	}
	return h.Sum(nil)
}

func sameContent(a, b string) bool {
	da, db := digest(a), digest(b)
	return da != nil && bytes.Equal(da, db)
}

func copyExecutable(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".rex-micro-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// rename replaces the file atomically; a running copy keeps its old inode.
	return os.Rename(tmp.Name(), dst)
}
