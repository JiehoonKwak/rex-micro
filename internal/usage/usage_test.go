package usage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

var day0 = time.Date(2026, 11, 25, 15, 0, 0, 0, time.Local)

// The record survives a save and load, counts by day, keeps a rebound key's
// new binding apart from its old one, and reports never-used bindings too.
func TestRecordReportsUseByBinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	close := Use{Layer: "agents", Key: "ACT07", Gesture: "press", Action: `{"Shortcut":"cmd+w"}`, Does: "Close pane"}
	split := Use{Layer: "agents", Key: "ACT07", Gesture: "press", Action: `{"Action":"pane.split.auto"}`, Does: "Close pane"} // bound before, same label
	back := Use{Layer: "agents", Key: "ACT08", Gesture: "press", Action: `{"Shortcut":"cmd+;"}`, Does: "Back"}
	l := New()
	l.Add(split, day0.AddDate(0, 0, -40))
	l.Add(close, day0.AddDate(0, 0, -2))
	l.Add(close, day0.AddDate(0, 0, -2))
	renamed := close
	renamed.Does = "Close" // a new label for the same binding keeps its count
	l.Add(renamed, day0)
	if err := l.Save(path, day0); err != nil {
		t.Fatal(err)
	}
	l, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	rows, since := l.Report([]Use{close, back, split}, 30, day0)
	if since != day0.AddDate(0, 0, -40).Format(day) {
		t.Errorf("since = %q", since)
	}
	want := []Row{
		{Use: close, Uses: 3, Days: 2, Last: day0.Format(day)},
		{Use: back},
		{Use: split, Last: day0.AddDate(0, 0, -40).Format(day)}, // outside the window, still known
	}
	for i, r := range rows {
		if r != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, r, want[i])
		}
	}
	// The window's first day counts; the day before it does not.
	l.Add(back, day0.AddDate(0, 0, -29))
	l.Add(back, day0.AddDate(0, 0, -30))
	if rows, _ := l.Report([]Use{back}, 30, day0); rows[0].Uses != 1 {
		t.Errorf("window edge: %+v", rows[0])
	}
}

// Days older than a year are dropped on save; an unchanged record is not
// written again.
func TestSaveDropsOldDaysAndSkipsNoChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	u := Use{Layer: "agents", Key: "ACT06", Gesture: "press", Action: `{"Action":"pane.zoom"}`}
	l := New()
	l.Add(u, day0.Add(-Keep-48*time.Hour))
	l.Add(u, day0)
	if err := l.Save(path, day0); err != nil {
		t.Fatal(err)
	}
	if rows, since := l.Report([]Use{u}, 10000, day0); rows[0].Days != 1 || since != day0.Format(day) {
		t.Errorf("old day kept: %+v since %s", rows[0], since)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := l.Save(path, day0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("an unchanged record was written again")
	}
}

// A record that cannot be read or parsed is kept as it is: loading never
// changes it and saving never writes over it.
func TestUnreadableRecordIsKept(t *testing.T) {
	dir := t.TempDir()
	for name, mode := range map[string]os.FileMode{"broken.json": 0o600, "other.json": 0o600, "partial.json": 0o600, "locked.json": 0o000} {
		path := filepath.Join(dir, name)
		content := "{not json"
		switch {
		case mode == 0:
			content = `{"days":{}}`
		case name == "other.json":
			content = `{"day":{}}` // JSON, but not a record
		case name == "partial.json":
			content = `{"days":{"2026-11-25":[{"layer":"a","key":"ACT07","gesture":"press","action":"x"}]}}` // no count
		}
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
		l, err := Load(path)
		if err == nil {
			if mode == 0 {
				continue // running as a user who reads mode-0 files
			}
			t.Fatalf("%s loaded without a word", name)
		}
		l.Add(Use{Key: "ACT06"}, day0)
		if err := l.Save(path, day0); err == nil {
			t.Errorf("%s: saved over a record it could not read", name)
		}
		os.Chmod(path, 0o600)
		if b, _ := os.ReadFile(path); string(b) != content {
			t.Errorf("%s changed to %q", name, b)
		}
		if extra, _ := filepath.Glob(path + "*"); len(extra) != 1 {
			t.Errorf("%s: files beside it: %v", name, extra)
		}
	}
}

// Uses added while a save is writing are not lost: the save marks only what
// it wrote as saved, and the next save writes the rest.
func TestUsesDuringASaveAreSavedNext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	u := Use{Layer: "agents", Key: "ACT06", Gesture: "press", Action: "zoom"}
	l := New()
	l.Add(u, day0)
	written := make(chan struct{})
	release := make(chan struct{})
	writeHook = func() { close(written); <-release }
	defer func() { writeHook = nil }()
	done := make(chan error)
	go func() { done <- l.Save(path, day0) }()
	<-written
	l.Add(u, day0) // arrives mid-write
	writeHook = nil
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := l.Save(path, day0); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if rows, _ := got.Report([]Use{u}, 1, day0); rows[0].Uses != 2 {
		t.Fatalf("saved %d uses, want 2", rows[0].Uses)
	}
}
