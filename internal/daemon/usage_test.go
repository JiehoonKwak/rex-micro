package daemon

import (
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
	dev, _, stop := startDaemon(t, cfgText, rx)
	dev.events <- report(t, `{"m":"v.oai.hid","p":{"k":"ACT07","act":1,"ag":0}}`)
	dev.events <- report(t, `{"m":"v.oai.hid","p":{"k":"ACT07","act":0,"ag":0}}`)
	eventually(t, "Enter sent", func() bool { return rx.has("-C client:app do pane.send_key key=enter") })
	if out, err := Press("ACT07", ""); err != nil || out.Error != "" {
		t.Fatalf("press: %v %+v", err, out)
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
	var approve []usage.Use
	for _, u := range engine.New(cfg, nil, nil, nil).Uses(0) {
		if u.Key == "ACT07" {
			approve = append(approve, u)
		}
	}
	rows, _ := l.Report(approve, 1, time.Now())
	if rows[0].Uses != 1 {
		t.Fatalf("ACT07 recorded %d uses, want 1 (the pad's, not rex-micro press's)", rows[0].Uses)
	}
}
