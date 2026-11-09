package padmap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JiehoonKwak/rex-micro/internal/config"
)

type pad struct {
	file   string
	writes int
}

func (p *pad) ReadFile(string) (string, error) { return p.file, nil }
func (p *pad) WriteFile(_, data string) error  { p.file = data; p.writes++; return nil }

const inputKeymap = `{"version":1,"profiles":[{"layers":[{"id":0,"name":"Layer 1"},{"id":3,"name":"General"}]}],"linkedApps":[{"id":0,"process":"com.brave.Browser"}]}`

var layers = []config.Layer{{Name: "agents", Color: "#8B5CF6"}, {Name: "browser", Color: "#F97316"}}

// Turning sync on keeps the keymap Work Louder Input wrote; turning it off
// puts that keymap back and forgets the copy. Neither rewrites a pad that
// already matches.
func TestSyncKeepsAndRestoresThePadsKeymap(t *testing.T) {
	backup := filepath.Join(t.TempDir(), "pad-keymap.json")
	p := &pad{file: inputKeymap}

	if wrote, err := Sync(p, true, layers, backup); err != nil || !wrote || p.file != Build(layers) {
		t.Fatalf("sync on: wrote=%v err=%v pad=%q", wrote, err, p.file)
	}
	if b, _ := os.ReadFile(backup); string(b) != inputKeymap {
		t.Fatalf("backup = %q, want Input's keymap", b)
	}
	if wrote, _ := Sync(p, true, layers, backup); wrote || p.writes != 1 {
		t.Fatalf("an unchanged config rewrote the pad (%d writes)", p.writes)
	}
	if wrote, err := Sync(p, false, layers, backup); err != nil || !wrote || p.file != inputKeymap {
		t.Fatalf("sync off: wrote=%v err=%v pad=%q", wrote, err, p.file)
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatalf("backup still there after restoring it: %v", err)
	}
	if wrote, _ := Sync(p, false, layers, backup); wrote {
		t.Fatal("sync off rewrote a keymap that is not rex-micro's")
	}
}

// Changing the layers while sync is on must not replace the saved keymap with
// rex-micro's own older one.
func TestResyncKeepsTheOriginalBackup(t *testing.T) {
	backup := filepath.Join(t.TempDir(), "pad-keymap.json")
	p := &pad{file: inputKeymap}
	if _, err := Sync(p, true, layers[:1], backup); err != nil {
		t.Fatal(err)
	}
	if wrote, err := Sync(p, true, layers, backup); err != nil || !wrote || p.file != Build(layers) {
		t.Fatalf("resync: wrote=%v err=%v", wrote, err)
	}
	if b, _ := os.ReadFile(backup); string(b) != inputKeymap {
		t.Fatalf("backup = %q, want Input's keymap", b)
	}
}

// Every pad layer sends vendor events (so rex-micro sees presses on any layer)
// and is linked to its own made-up app (so rex-micro can switch to it).
func TestBuildLinksEveryLayer(t *testing.T) {
	ls := append(append([]config.Layer{}, layers...), config.Layer{Name: "리뷰"})
	out := Build(ls)
	for _, r := range out {
		if r > 0x7F {
			t.Fatalf("non-ASCII left in the keymap: %q", out)
		}
	}
	var km struct {
		Profiles []struct {
			Layers []struct {
				Name   string `json:"name"`
				Linked int    `json:"linkedAppId"`
				Color  int    `json:"color"`
				Layout struct {
					Keymap   [][]string `json:"keymap"`
					Joystick struct{ Type string }
				} `json:"layout"`
			} `json:"layers"`
		} `json:"profiles"`
		LinkedApps []struct {
			ID      int    `json:"id"`
			Process string `json:"process"`
		} `json:"linkedApps"`
	}
	if err := json.Unmarshal([]byte(out), &km); err != nil {
		t.Fatal(err)
	}
	got := km.Profiles[0].Layers
	if len(got) != 3 || got[2].Name != "리뷰" || got[0].Color != 0x8B5CF6 {
		t.Fatalf("layers = %+v", got)
	}
	process := map[int]string{}
	for _, a := range km.LinkedApps {
		process[a.ID] = a.Process
	}
	for i, l := range got {
		keys := strings.Join(append(append(append(l.Layout.Keymap[0], l.Layout.Keymap[1]...), l.Layout.Keymap[2]...), l.Layout.Keymap[3]...), " ")
		if strings.Count(keys, "KV_OAI_") != 13 || l.Layout.Joystick.Type != "VENDOR" {
			t.Fatalf("layer %d keys %q joystick %q", i, keys, l.Layout.Joystick.Type)
		}
		if process[l.Linked] != Link(i) {
			t.Fatalf("layer %d links to %q, want %q", i, process[l.Linked], Link(i))
		}
	}
	if !Ours(out) || Ours(inputKeymap) {
		t.Fatal("Ours does not tell rex-micro's keymap from Input's")
	}
}
