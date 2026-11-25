// Package usage owns the pad's usage record: how often each binding was
// used, per day, so a report can show which bindings earn their
// key. Only the daemon adds to it, and only for presses on the pad itself.
package usage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Use is one binding's gesture: the layer that binds it, the key, the
// gesture, and the action it runs. Action identifies the binding (a key
// rebound to something else starts from zero, a renamed label does not);
// Does is how it reads, for the report only.
type Use struct {
	Layer   string `json:"layer"`
	Key     string `json:"key"`
	Gesture string `json:"gesture"`
	Action  string `json:"action"`
	Does    string `json:"does,omitempty"`
}

func (u Use) id() Use { u.Does = ""; return u }

type count struct {
	Use
	N int `json:"n"`
}

type file struct {
	Days map[string][]count `json:"days"` // "2006-01-02" in local time
}

// Keep is how long days are kept.
const Keep = 365 * 24 * time.Hour

const day = "2006-01-02"

// Log is the record in memory. It is safe for concurrent use.
type Log struct {
	save   sync.Mutex // held for a whole save, so saves land in order
	mu     sync.Mutex
	days   map[string]map[Use]int
	gen    int   // bumped by every Add
	saved  int   // the gen the file on disk has
	frozen error // why Save must not write (a record it could not read)
}

// New is an empty record.
func New() *Log { return &Log{days: map[string]map[Use]int{}} }

// Load reads the record at path; a missing file is an empty record. Load
// never changes the file: one it cannot read or parse comes back as an
// error with an empty record that will not save, so the file is kept for
// its owner to fix or remove, and the counts until then stay in memory.
func Load(path string) (*Log, error) {
	l := New()
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return l, nil
	}
	var f file
	if err == nil {
		err = json.Unmarshal(b, &f)
	}
	if err != nil {
		l.frozen = fmt.Errorf("%s cannot be read (%v); keeping it and saving nothing until it is fixed or removed", path, err)
		return l, l.frozen
	}
	for d, cs := range f.Days {
		m := map[Use]int{}
		for _, c := range cs {
			m[c.Use.id()] += c.N
		}
		l.days[d] = m
	}
	return l, nil
}

// Frozen reports whether the record will not save because Load could not
// read the file (Load's error said so once).
func (l *Log) Frozen() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.frozen != nil
}

// Add counts one use at time at.
func (l *Log) Add(u Use, at time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	d := at.Format(day)
	if l.days[d] == nil {
		l.days[d] = map[Use]int{}
	}
	l.days[d][u.id()]++
	l.gen++
}

// Save writes the record to path if it changed since the last save,
// dropping days older than Keep. The file is replaced whole, never left half
// written, and saves run one at a time, so a later one never lands first.
func (l *Log) Save(path string, now time.Time) error {
	l.save.Lock()
	defer l.save.Unlock()
	l.mu.Lock()
	if l.frozen != nil {
		l.mu.Unlock()
		return l.frozen
	}
	if l.gen == l.saved {
		l.mu.Unlock()
		return nil
	}
	gen := l.gen
	f := file{Days: map[string][]count{}}
	oldest := now.Add(-Keep).Format(day)
	for d, m := range l.days {
		if d < oldest {
			delete(l.days, d)
			continue
		}
		for u, n := range m {
			f.Days[d] = append(f.Days[d], count{u, n})
		}
	}
	l.mu.Unlock()
	for d := range f.Days {
		sort.Slice(f.Days[d], func(i, j int) bool { return less(f.Days[d][i].Use, f.Days[d][j].Use) })
	}
	b, err := json.MarshalIndent(f, "", " ")
	if writeHook != nil {
		writeHook()
	}
	if err == nil {
		err = writeAtomic(path, b)
	}
	if err == nil {
		l.mu.Lock()
		l.saved = gen // uses added while writing stay unsaved
		l.mu.Unlock()
	}
	return err
}

// writeHook, when set by a test, runs between taking the snapshot and
// writing it.
var writeHook func()

func less(a, b Use) bool {
	if a.Layer != b.Layer {
		return a.Layer < b.Layer
	}
	if a.Key != b.Key {
		return a.Key < b.Key
	}
	if a.Gesture != b.Gesture {
		return a.Gesture < b.Gesture
	}
	return a.Action < b.Action
}

func writeAtomic(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".usage-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Row is one binding's use over a window of days.
type Row struct {
	Use
	Uses int    // times used
	Days int    // days it was used on
	Last string // the last day it was used ("2006-01-02"), "" for never
}

// Report counts each of uses over the days days ending with now's day,
// in the order given. Since is the first day the record has, "" when empty.
func (l *Log) Report(uses []Use, days int, now time.Time) (rows []Row, since string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	from := now.AddDate(0, 0, -(days - 1)).Format(day)
	to := now.Format(day)
	for d := range l.days {
		if since == "" || d < since {
			since = d
		}
	}
	for _, u := range uses {
		r := Row{Use: u}
		for d, m := range l.days {
			n := m[u.id()]
			if n == 0 {
				continue
			}
			if d > r.Last {
				r.Last = d
			}
			if d >= from && d <= to {
				r.Uses += n
				r.Days++
			}
		}
		rows = append(rows, r)
	}
	return rows, since
}
