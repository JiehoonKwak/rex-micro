// Package padmap owns the keymap stored on the pad when rex-micro's layers
// drive the pad's own layers ([pad] layers = "sync").
//
// The pad switches hardware layers itself: its touch sensor cycles them and
// shows the active one on its LEDs, and Work Louder Input links a layer to an
// app so the pad switches when told which app is in front (host.focused_app).
// Sync writes a keymap with one hardware layer per rex-micro layer, every key
// sending the vendor events rex-micro reads, and links layer N to the made-up
// app Link(N). rex-micro then picks a hardware layer by "focusing" its link and
// reads the touch sensor's choice from device.status.
//
// The keymap it replaces is kept in a backup file and written back when sync
// is turned off.
package padmap

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JiehoonKwak/rex-micro/internal/config"
	"github.com/JiehoonKwak/rex-micro/internal/protocol"
)

// MaxLayers is how many layers the pad's touch sensor cycles through.
const MaxLayers = 6

// File is the keymap's name on the pad.
const File = "keymap.json"

const linkPrefix = "rex-micro.layer."

// Link is the made-up bundle ID the pad's layer i (0-based) is linked to.
func Link(i int) string { return fmt.Sprintf("%s%d", linkPrefix, i+1) }

// Pad is the part of the pad Sync needs.
type Pad interface {
	ReadFile(name string) (string, error)
	WriteFile(name, data string) error
}

// Build returns the keymap for these layers (at most MaxLayers).
func Build(layers []config.Layer) string {
	type app struct {
		ID      int    `json:"id"`
		Name    string `json:"name"`
		Process string `json:"process"`
		Path    string `json:"path"`
	}
	type layer struct {
		ID          int    `json:"id"`
		Name        string `json:"name"`
		Color       int    `json:"color"`
		Layout      any    `json:"layout"`
		LinkedAppID int    `json:"linkedAppId"`
	}
	var ls []layer
	var apps []app
	for i, l := range layers[:min(len(layers), MaxLayers)] {
		color := 0xFFFFFF
		if c, err := protocol.ParseColor(l.Color); err == nil {
			color = c
		}
		ls = append(ls, layer{ID: i, Name: l.Name, Color: color, Layout: vendorLayout, LinkedAppID: i + 1})
		apps = append(apps, app{ID: i + 1, Name: "rex-micro layer " + l.Name, Process: Link(i)})
	}
	km := map[string]any{
		"version":         1,
		"activeProfileId": 0,
		"profiles": []any{map[string]any{
			"id": 0, "name": "rex-micro", "layers": ls,
			"macrosUsed": []int{}, "multiActionsUsed": []int{},
		}},
		"multiActions": []any{}, "macros": []any{},
		"macrosGroups": []any{}, "multiActionsGroups": []any{},
		"linkedApps": apps,
	}
	b, _ := json.Marshal(km)
	return ascii(string(b)) + "\n"
}

// ascii escapes non-ASCII characters (a layer named in Korean, say) as \uXXXX,
// as Work Louder Input does before writing the pad.
func ascii(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 0x80:
			b.WriteRune(r)
		case r > 0xFFFF:
			r -= 0x10000
			fmt.Fprintf(&b, `\u%04x\u%04x`, 0xD800+(r>>10), 0xDC00+(r&0x3FF))
		default:
			fmt.Fprintf(&b, `\u%04x`, r)
		}
	}
	return b.String()
}

// vendorLayout is the pad's Codex layer: every key, the dial and the stick
// send v.oai.* events instead of keyboard input.
var vendorLayout = map[string]any{
	"keymap": [][]string{
		{"KV_OAI_AG00", "KV_OAI_AG01"},
		{"KV_OAI_AG02", "KV_OAI_AG03", "KV_OAI_AG04", "KV_OAI_AG05"},
		{"KV_OAI_ACT06", "KV_OAI_ACT07", "KV_OAI_ACT08", "KV_OAI_ACT09"},
		{"KV_OAI_ACT10", "KV_OAI_ACT11", "KV_OAI_ACT12"},
	},
	"encoders": [][]string{{"KV_OAI_ENC_CC", "KV_OAI_ENC_CW", "KV_OAI_ENC_CLK"}},
	"joystick": map[string]any{"type": "VENDOR", "sectors": []any{}},
}

// Ours reports whether a pad keymap is one Build wrote.
func Ours(keymap string) bool {
	var km struct {
		LinkedApps []struct {
			Process string `json:"process"`
		} `json:"linkedApps"`
	}
	if json.Unmarshal([]byte(keymap), &km) != nil {
		return false
	}
	for _, a := range km.LinkedApps {
		if strings.HasPrefix(a.Process, linkPrefix) {
			return true
		}
	}
	return false
}

// Sync makes the pad's keymap match on. With on, it writes Build(layers),
// first saving a keymap that is not ours to backup. With on false, it writes
// the backup back over a keymap of ours and removes the backup. It reports
// whether it wrote the pad.
func Sync(p Pad, on bool, layers []config.Layer, backup string) (bool, error) {
	cur, err := p.ReadFile(File)
	if err != nil {
		return false, err
	}
	if on {
		want := Build(layers)
		if cur == want {
			return false, nil
		}
		if !Ours(cur) {
			if err := save(backup, cur); err != nil {
				return false, fmt.Errorf("keep the pad's keymap: %w", err)
			}
		}
		return true, p.WriteFile(File, want)
	}
	if !Ours(cur) {
		return false, nil
	}
	old, err := os.ReadFile(backup)
	if errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("the pad still has rex-micro's layers but %s is gone; set its layers again in Work Louder Input", backup)
	}
	if err != nil {
		return false, err
	}
	if err := p.WriteFile(File, string(old)); err != nil {
		return true, err
	}
	return true, os.Remove(backup)
}

// save writes a file atomically, so a crash never leaves half a backup.
func save(path, data string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(data), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
