package engine

import "testing"

// Glyphs shows the shortcut that is posted, read the way ParseShortcut reads
// it: spaces, a literal + and a lone modifier included (review of PR 10).
func TestGlyphsShowWhatIsPosted(t *testing.T) {
	for in, want := range map[string]string{
		"cmd+shift+z":       "⌘⇧Z",
		" cmd + shift + z ": "⌘⇧Z",
		"cmd++":             "⌘⇧=",
		"Opt+Left":          "⌥←",
		"right_option":      "right ⌥",
		"page_down":         "PgDn",
	} {
		if got := Glyphs(in); got != want {
			t.Errorf("Glyphs(%q) = %q, want %q", in, got, want)
		}
	}
}
