package macos

import "testing"

func TestParseShortcut(t *testing.T) {
	for in, want := range map[string]Shortcut{
		"cmd+shift+]":  {30, flagCommand | flagShift, false},
		"ctrl+tab":     {48, flagControl, false},
		"Opt+Left":     {123, flagOption, false},
		"f5":           {96, 0, false},
		"cmd++":        {24, flagCommand | flagShift, false},
		"right_option": {61, flagOption | 0x40, true},
		"fn":           {63, flagFn, true},
	} {
		got, err := ParseShortcut(in)
		if err != nil || got != want {
			t.Errorf("%q = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "cmd+", "cmd+shift", "right_option+a", "hyper+a", "cmd+ä"} {
		if _, err := ParseShortcut(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestParseLSName(t *testing.T) {
	if n, ok := parseLSName("\"Brave Browser\" ASN:0x0-0x3d146109: (in front) \n    bundleID=[ NULL ]\n"); !ok || n != "Brave Browser" {
		t.Fatalf("%q %v", n, ok)
	}
	if _, ok := parseLSName(""); ok {
		t.Fatal("empty output parsed")
	}
}
