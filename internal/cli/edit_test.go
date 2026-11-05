package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JiehoonKwak/rex-micro/internal/config"
	"github.com/JiehoonKwak/rex-micro/internal/editor"
)

func TestSaveRefusesExternalChangesAndFollowsLinks(t *testing.T) {
	t.Setenv("REX_MICRO_SOCKET", filepath.Join(t.TempDir(), "none.sock"))
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(config.Default), 0o600); err != nil {
		t.Fatal(err)
	}
	edited := config.Default + "\n# mine\n"
	if err := os.WriteFile(path, []byte(config.Default+"# theirs\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := saveConfig(path, edited, config.Default, false); !errors.Is(err, editor.ErrChanged) {
		t.Fatalf("external edit overwritten: %v", err)
	}
	if err := saveConfig(path, edited, config.Default, true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != edited {
		t.Fatal("force did not write")
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", st.Mode())
	}

	// A link whose target does not exist yet (a dotfiles repo not populated).
	link := filepath.Join(dir, "link.toml")
	target := filepath.Join(dir, "dotfiles", "rex-micro.toml")
	if err := os.Symlink("dotfiles/rex-micro.toml", link); err != nil {
		t.Fatal(err)
	}
	if err := saveConfig(link, edited, "", false); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the symlink was replaced by a file")
	}
	if b, _ := os.ReadFile(target); string(b) != edited {
		t.Fatal("target not written")
	}

	// A link inside a symlinked directory, pointing up with "..".
	real := filepath.Join(dir, "real", "deep")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("real", "deep"), filepath.Join(dir, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../actual.toml", filepath.Join(real, "config.toml")); err != nil {
		t.Fatal(err)
	}
	if err := saveConfig(filepath.Join(dir, "linked", "config.toml"), edited, "", false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "real", "actual.toml")); string(b) != edited {
		t.Fatal("wrote the wrong file through the symlinked parent")
	}
}

func TestServiceKeepsConfigOverride(t *testing.T) {
	t.Setenv("REX_MICRO_CONFIG", "/tmp/a&b/config.toml")
	t.Setenv("REX_MICRO_REX", "")
	if got := serviceEnv(); !strings.Contains(got, "<key>REX_MICRO_CONFIG</key><string>/tmp/a&amp;b/config.toml</string>") {
		t.Fatalf("env = %q", got)
	}
	t.Setenv("REX_MICRO_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	if got := serviceEnv(); got != "" {
		t.Fatalf("default config added env %q", got)
	}
}
