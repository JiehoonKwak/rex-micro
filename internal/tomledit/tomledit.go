// Package tomledit changes a rex-micro config file line by line, so every
// comment, blank line and ordering the user wrote survives an edit. It knows
// the file's shape: [[layer]] tables with fields (name, color, match) and a
// [layer.keys] table of single-line bindings. Callers validate the result by
// parsing it.
package tomledit

import (
	"errors"
	"fmt"
	"strings"
)

// Doc is a config file as lines.
type Doc struct {
	lines []string
}

// Parse splits text into lines; String joins them back unchanged.
func Parse(text string) *Doc { return &Doc{lines: strings.Split(text, "\n")} }

func (d *Doc) String() string { return strings.Join(d.lines, "\n") }

// line kinds
const (
	blank = iota
	comment
	header
	entry // first line of key = value
	cont  // a later line of a multi-line value
)

type line struct {
	kind  int
	path  []string // header table path, or the entry's dotted key
	array bool     // [[header]]
	end   int      // entry: index of its last line
	hash  int      // entry on one line: byte offset of its trailing comment, else -1
}

// scan classifies every line, tracking strings and brackets across lines.
func (d *Doc) scan() []line {
	out := make([]line, len(d.lines))
	depth, ml := 0, "" // bracket depth and open multi-line string delimiter
	start := -1        // entry being continued
	for i, text := range d.lines {
		t := strings.TrimSpace(text)
		switch {
		case start >= 0:
			out[i] = line{kind: cont}
		case t == "":
			out[i] = line{kind: blank}
			continue
		case t[0] == '#':
			out[i] = line{kind: comment}
			continue
		case t[0] == '[':
			arr := strings.HasPrefix(t, "[[")
			name := strings.TrimLeft(t, "[")
			if j := strings.Index(name, "]"); j >= 0 {
				name = name[:j]
			}
			out[i] = line{kind: header, path: splitKey(name), array: arr}
			continue
		default:
			k := t
			if j := strings.Index(k, "="); j >= 0 {
				k = k[:j]
			}
			out[i] = line{kind: entry, path: splitKey(k), hash: -1}
			start = i
		}
		hash := -1
		inValue := start != i // keys never hold brackets; only count from '='
		q := ""
		for j := 0; j < len(text); j++ {
			c := text[j]
			switch {
			case ml != "":
				if strings.HasPrefix(text[j:], ml) {
					// Up to two quotes may end the content: '''a'''' is "a'".
					n := 3
					for n < 5 && j+n < len(text) && text[j+n] == ml[0] {
						n++
					}
					j += n - 1
					ml = ""
				} else if c == '\\' && ml == `"""` {
					j++
				}
			case q != "":
				if c == '\\' && q == `"` {
					j++
				} else if string(c) == q {
					q = ""
				}
			case strings.HasPrefix(text[j:], `"""`) || strings.HasPrefix(text[j:], `'''`):
				ml = text[j : j+3]
				j += 2
			case c == '"' || c == '\'':
				q = string(c)
			case c == '#':
				hash = j
				j = len(text)
			case c == '=':
				inValue = true
			case inValue && (c == '[' || c == '{'):
				depth++
			case inValue && (c == ']' || c == '}'):
				depth--
			}
		}
		if depth <= 0 && ml == "" {
			depth = 0
			out[start].end = i
			if start == i {
				out[i].hash = hash
			}
			start = -1
		}
	}
	if start >= 0 {
		out[start].end = len(d.lines) - 1
	}
	return out
}

// splitKey splits a dotted TOML key, removing quotes and spaces.
func splitKey(s string) []string {
	var parts []string
	var b strings.Builder
	q := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case q != 0:
			if c == q {
				q = 0
			} else {
				b.WriteByte(c)
			}
		case c == '"' || c == '\'':
			q = c
		case c == '.':
			parts = append(parts, strings.TrimSpace(b.String()))
			b.Reset()
		case c == ' ' || c == '\t':
		default:
			b.WriteByte(c)
		}
	}
	return append(parts, strings.TrimSpace(b.String()))
}

func hasPrefix(path []string, prefix ...string) bool {
	if len(path) < len(prefix) {
		return false
	}
	for i, p := range prefix {
		if path[i] != p {
			return false
		}
	}
	return true
}

// layout locates one [[layer]] table.
type layout struct {
	start, end int // header line, last line of the layer's tables
	lines      []line
}

// Layers counts the [[layer]] tables.
func (d *Doc) Layers() int {
	n := 0
	for _, l := range d.scan() {
		if l.kind == header && l.array && hasPrefix(l.path, "layer") && len(l.path) == 1 {
			n++
		}
	}
	return n
}

func (d *Doc) layer(i int) (layout, error) {
	ls := d.scan()
	n := -1
	lo := layout{start: -1, lines: ls}
	for j, l := range ls {
		if l.kind != header {
			continue
		}
		top := l.array && len(l.path) == 1 && l.path[0] == "layer"
		if lo.start >= 0 && (top || !hasPrefix(l.path, "layer")) {
			lo.end = j - 1
			return lo, nil
		}
		if top {
			n++
			if n == i {
				lo.start = j
			}
		}
	}
	if lo.start < 0 {
		return lo, fmt.Errorf("no layer %d in the file", i+1)
	}
	lo.end = len(ls) - 1
	return lo, nil
}

// section returns the table path a line in the layer belongs to (relative
// to the layer), and the header line that opened it.
func (lo layout) sections() []([]string) {
	out := make([][]string, lo.end+1)
	var cur []string
	for j := lo.start; j <= lo.end; j++ {
		if lo.lines[j].kind == header {
			cur = lo.lines[j].path[1:]
		}
		out[j] = cur
	}
	return out
}

// lastContent is the last line in [from, to] that is not blank or a comment.
func (lo layout) lastContent(from, to int) int {
	last := from
	for j := from; j <= to; j++ {
		if k := lo.lines[j].kind; k != blank && k != comment {
			last = j
		}
	}
	return last
}

// ErrInlineKeys means a layer writes keys = { ... } on one line, which this
// editor does not rewrite.
var ErrInlineKeys = errors.New("this layer writes its keys as one inline table; split it into a [layer.keys] table to edit it here")

// SetKey replaces the binding of key in layer i with value (TOML inline
// table text), or removes it when value is "". Only the binding's own lines
// change; a replaced line keeps its trailing comment.
func (d *Doc) SetKey(i int, key, value string) error {
	return d.set(i, []string{"keys"}, key, value)
}

// SetField sets a layer field (name, color, match) to TOML value text, or
// removes it when value is "".
func (d *Doc) SetField(i int, field, value string) error {
	return d.set(i, nil, field, value)
}

// set edits name within table (nil = the layer table itself).
func (d *Doc) set(i int, table []string, name, value string) error {
	lo, err := d.layer(i)
	if err != nil {
		return err
	}
	secs := lo.sections()
	full := append(append([]string{}, table...), name) // path from the layer
	type span struct{ from, to int }
	var drop []span
	at, hash, dotted, same := -1, -1, false, false
	var rel []string // the matched entry's own key, e.g. keys.ACT06 in the layer table
	tableAt, tableEnd, ownEnd := -1, -1, lo.start
	// A sibling written with dotted keys above the table (ACT06.hold = ...
	// under [layer.keys]) means the table exists only in dotted form, so a new
	// entry joins it the same way, after that sibling.
	sibEnd, sibDepth := -1, 0
	dropping := false // inside a table that is being removed whole
	for j := lo.start; j <= lo.end; j++ {
		l, sec := lo.lines[j], secs[j]
		if l.kind == header {
			dropping = false
		}
		if dropping {
			continue
		}
		switch l.kind {
		case header:
			if j == lo.start {
				continue
			}
			if equal(sec, table) {
				tableAt = j
			}
			if hasPrefix(sec, full...) { // [layer.keys.ACT06] style table
				end := j
				for k := j + 1; k <= lo.end && lo.lines[k].kind != header; k++ {
					if lo.lines[k].kind == entry || lo.lines[k].kind == cont {
						end = k
					}
				}
				drop = append(drop, span{j, end})
				dropping = true
			}
		case entry:
			path := append(append([]string{}, sec...), l.path...)
			if len(sec) == 0 {
				ownEnd = l.end
			}
			if equal(sec, table) {
				tableEnd = l.end
			}
			if table != nil && len(sec) == 0 && equal(l.path, table) {
				return ErrInlineKeys
			}
			if hasPrefix(path, full...) {
				switch {
				case at < 0 && len(path) == len(full):
					at, rel = j, l.path // replace in place, in its own form
					if l.end == j {
						hash, same = l.hash, true
					}
				case at < 0 && equal(sec, table):
					at = j
				}
				if len(sec) < len(table) {
					dotted = true // keys.ACT06 = ... in the layer table
				}
				drop = append(drop, span{j, l.end})
			} else if table != nil && len(sec) < len(table) && hasPrefix(path, table...) {
				if len(sec) == 0 {
					dotted = true
				}
				sibEnd, sibDepth = l.end, len(sec)
			}
		}
	}
	var newLine string
	if value != "" {
		key := name
		switch {
		case rel != nil:
			key = strings.Join(rel, ".")
		case sibEnd >= 0 && tableAt < 0:
			key = strings.Join(full[sibDepth:], ".")
		case dotted && tableAt < 0:
			key = strings.Join(full, ".")
		}
		newLine = key + " = " + value
		if same {
			// Same key on one line: keep its spacing and trailing comment.
			old := d.lines[at]
			if eq := strings.Index(old, "="); eq >= 0 {
				rest := old[eq+1:]
				newLine = old[:eq+1] + rest[:len(rest)-len(strings.TrimLeft(rest, " \t"))] + value
			}
			if hash >= 0 {
				newLine += strings.Repeat(" ", max(1, hash-len(newLine))) + old[hash:]
			}
		}
	}
	// Remove from the bottom so indexes above stay valid.
	insert, extra := at, []string(nil)
	for k := len(drop) - 1; k >= 0; k-- {
		s := drop[k]
		d.lines = append(d.lines[:s.from], d.lines[s.to+1:]...)
		if insert > s.from {
			insert -= s.to - s.from + 1
		}
		if tableAt > s.from {
			tableAt -= s.to - s.from + 1
			tableEnd -= s.to - s.from + 1
		}
		if ownEnd > s.from {
			ownEnd -= s.to - s.from + 1
		}
		if sibEnd > s.from {
			sibEnd -= s.to - s.from + 1
		}
	}
	if value == "" {
		return nil
	}
	if insert < 0 {
		switch {
		case table == nil:
			insert = ownEnd + 1
		case tableAt >= 0:
			insert = max(tableAt, tableEnd) + 1
		case sibEnd >= 0:
			insert = sibEnd + 1
		case dotted:
			insert = ownEnd + 1
		default:
			// No [layer.keys] yet: open one after the layer's last content.
			lo, _ := d.layer(i)
			insert = lo.lastContent(lo.start, lo.end) + 1
			extra = []string{"", "[layer." + strings.Join(table, ".") + "]"}
		}
	}
	add := append(extra, newLine)
	d.lines = append(d.lines[:insert], append(add, d.lines[insert:]...)...)
	return nil
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// AddLayer appends a [[layer]] table with the given field lines.
func (d *Doc) AddLayer(fields ...string) {
	for len(d.lines) > 0 && strings.TrimSpace(d.lines[len(d.lines)-1]) == "" {
		d.lines = d.lines[:len(d.lines)-1]
	}
	d.lines = append(d.lines, "", "[[layer]]")
	d.lines = append(d.lines, fields...)
	d.lines = append(d.lines, "")
}

// DeleteLayer removes layer i's tables. Comments and blank lines after its
// last setting stay, since they usually introduce what follows.
func (d *Doc) DeleteLayer(i int) error {
	lo, err := d.layer(i)
	if err != nil {
		return err
	}
	end := lo.lastContent(lo.start, lo.end)
	from := lo.start
	if from > 0 && strings.TrimSpace(d.lines[from-1]) == "" && end+1 < len(d.lines) && strings.TrimSpace(d.lines[end+1]) == "" {
		from-- // don't leave two blank lines behind
	}
	d.lines = append(d.lines[:from], d.lines[end+1:]...)
	return nil
}

// RenameLayerRefs points builtin = "layer.<old>" bindings at the new name.
// It reads each line as TOML tokens, so a string that merely contains
// `builtin = "layer.<old>"` (a shell command, typed text) stays as written.
func (d *Doc) RenameLayerRefs(old, new string) {
	for j, l := range d.scan() {
		if l.kind != entry {
			continue
		}
		text := d.lines[j]
		toks := tokens(text)
		for k := 0; k+2 < len(toks); k++ {
			key, eq, val := toks[k], toks[k+1], toks[k+2]
			if key.value != "builtin" || key.kind == tokPunct || eq.kind != tokPunct || eq.value != "=" ||
				val.kind != tokString || val.value != "layer."+old {
				continue
			}
			repl := `"layer.` + escapeBasic(new) + `"`
			text = text[:val.start] + repl + text[val.end:]
			d.lines[j] = text
			d.RenameLayerRefs(old, new) // offsets moved; rescan
			return
		}
	}
}

func escapeBasic(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}

const (
	tokBare = iota
	tokString
	tokPunct
)

type token struct {
	kind       int
	value      string // bare text, punctuation, or a string's contents
	start, end int    // byte range in the line
}

// tokens splits one line of TOML into bare words, single-line strings and
// punctuation, stopping at a comment.
func tokens(s string) []token {
	var out []token
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t':
			i++
		case c == '#':
			return out
		case c == '"' || c == '\'':
			j := i + 1
			var b strings.Builder
			for j < len(s) && s[j] != c {
				if c == '"' && s[j] == '\\' && j+1 < len(s) {
					j++
					switch s[j] {
					case 'n':
						b.WriteByte('\n')
					case 't':
						b.WriteByte('\t')
					default:
						b.WriteByte(s[j])
					}
				} else {
					b.WriteByte(s[j])
				}
				j++
			}
			out = append(out, token{tokString, b.String(), i, min(j+1, len(s))})
			i = j + 1
		case strings.ContainsRune("=,{}[].", rune(c)):
			out = append(out, token{tokPunct, string(c), i, i + 1})
			i++
		default:
			j := i
			for j < len(s) && !strings.ContainsRune(" \t=,{}[].#\"'", rune(s[j])) {
				j++
			}
			out = append(out, token{tokBare, s[i:j], i, j})
			i = j
		}
	}
	return out
}

var gestureKeys = map[string]bool{"press": true, "double": true, "hold": true, "down": true, "up": true}

// GestureTable reports whether layer i writes key as a [layer.keys.KEY]
// table of gestures, which SetGesture edits one entry at a time.
func (d *Doc) GestureTable(i int, key string) bool {
	lo, err := d.layer(i)
	if err != nil {
		return false
	}
	secs := lo.sections()
	found := false
	for j := lo.start; j <= lo.end; j++ {
		l, sec := lo.lines[j], secs[j]
		if !hasPrefix(sec, "keys", key) {
			continue
		}
		switch {
		case l.kind == header && len(sec) == 2:
			found = true
		case l.kind == header && len(sec) == 3 && gestureKeys[sec[2]]:
			found = true // [layer.keys.KEY.hold] holds one gesture's action
		case l.kind == header:
			return false
		case l.kind == entry && len(sec) == 2 && !gestureKeys[l.path[0]]:
			return false // direct fields (action = ...)
		}
	}
	for j := lo.start; j <= lo.end; j++ {
		l, sec := lo.lines[j], secs[j]
		path := append(append([]string{}, sec...), l.path...)
		if l.kind != entry || len(sec) >= 2 || len(path) < 3 || path[0] != "keys" || path[1] != key {
			continue
		}
		if !gestureKeys[path[2]] {
			return false
		}
		found = true // ACT06.hold = ... under [layer.keys], or keys.ACT06.hold under [[layer]]
	}
	return found
}

// SetGesture sets one gesture inside a [layer.keys.KEY] table, leaving the
// other gestures and their comments alone. "" removes it.
func (d *Doc) SetGesture(i int, key, gesture, value string) error {
	return d.set(i, []string{"keys", key}, gesture, value)
}

// SetGlobal sets key in a top-level table such as [agents], adding the table
// before the first layer when the file has none. "" removes the key.
func (d *Doc) SetGlobal(table, key, value string) error {
	ls := d.scan()
	at, end, firstLayer := -1, -1, -1
	in := false
	for j, l := range ls {
		if l.kind == header {
			in = !l.array && equal(l.path, []string{table})
			if in {
				end = j
			}
			if firstLayer < 0 && l.array && equal(l.path, []string{"layer"}) {
				firstLayer = j
			}
			continue
		}
		if !in || l.kind != entry {
			continue
		}
		end = l.end
		if equal(l.path, []string{key}) {
			at = j
			line := key + " = " + value
			if l.hash >= 0 {
				line += strings.Repeat(" ", max(1, l.hash-len(line))) + d.lines[j][l.hash:]
			}
			if value == "" {
				d.lines = append(d.lines[:j], d.lines[l.end+1:]...)
			} else {
				d.lines = append(append(d.lines[:j:j], line), d.lines[l.end+1:]...)
			}
			return nil
		}
	}
	if value == "" {
		return nil
	}
	line := key + " = " + value
	switch {
	case at < 0 && end >= 0:
		d.lines = append(d.lines[:end+1], append([]string{line}, d.lines[end+1:]...)...)
	case firstLayer >= 0:
		d.lines = append(d.lines[:firstLayer], append([]string{"[" + table + "]", line, ""}, d.lines[firstLayer:]...)...)
	default:
		d.AddTable(table, line)
	}
	return nil
}

// AddTable appends a [table] with lines.
func (d *Doc) AddTable(table string, lines ...string) {
	for len(d.lines) > 0 && strings.TrimSpace(d.lines[len(d.lines)-1]) == "" {
		d.lines = d.lines[:len(d.lines)-1]
	}
	d.lines = append(append(d.lines, "", "["+table+"]"), lines...)
	d.lines = append(d.lines, "")
}
