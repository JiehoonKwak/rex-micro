package daemon

import (
	"os"
	"slices"
	"testing"
	"time"

	"github.com/JiehoonKwak/rex-micro/internal/config"
	"github.com/JiehoonKwak/rex-micro/internal/engine"
	"github.com/JiehoonKwak/rex-micro/internal/usage"
)

// A press on the pad is recorded; the same key from `rex-micro press` acts
// but is not; the record is on disk once the daemon stops.
func TestPadUseIsRecorded(t *testing.T) {
	rx := &fakeRex{}
	const cfgText = `[[layer]]
name = "agents"
[layer.keys]
ACT07 = { key = "enter", label = "Approve" }
`
	dev, cfgPath, stop := startDaemon(t, cfgText, rx)
	dev.events <- report(t, `{"m":"v.oai.hid","p":{"k":"ACT07","act":1,"ag":0}}`)
	dev.events <- report(t, `{"m":"v.oai.hid","p":{"k":"ACT07","act":0,"ag":0}}`)
	eventually(t, "Enter sent", func() bool { return rx.has("-C client:app do pane.send_key key=enter") })
	if out, err := Press("ACT07", ""); err != nil || out.Error != "" {
		t.Fatalf("press: %v %+v", err, out)
	}
	// The report has the use at once, before any save, and keeps to the
	// bindings the daemon applied after the file stops loading.
	approve := func() usage.Row {
		r, err := Usage(1)
		if err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(r.Rows, func(row usage.Row) bool { return row.Key == "ACT07" })
		if i < 0 || !*r.Record {
			t.Fatalf("report = %+v", r)
		}
		return r.Rows[i]
	}
	if row := approve(); row.Uses != 1 || row.Does != "Approve" {
		t.Fatalf("live report ACT07 = %+v, want 1 use", row)
	}
	if err := os.WriteFile(cfgPath, []byte("[[layer]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var reply map[string]string
	if Call("reload", &reply); reply["error"] == "" {
		t.Fatalf("a broken config reloaded: %v", reply)
	}
	if row := approve(); row.Uses != 1 || row.Does != "Approve" {
		t.Fatalf("after a rejected reload ACT07 = %+v", row)
	}
	path := UsagePath()
	stop()
	l, err := usage.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse(cfgText)
	if err != nil {
		t.Fatal(err)
	}
	var saved []usage.Use
	for _, u := range engine.New(cfg, nil, nil, nil).Uses(0) {
		if u.Key == "ACT07" {
			saved = append(saved, u)
		}
	}
	rows, _ := l.Report(saved, 1, time.Now())
	if rows[0].Uses != 1 {
		t.Fatalf("ACT07 recorded %d uses, want 1 (the pad's, not rex-micro press's)", rows[0].Uses)
	}
}
