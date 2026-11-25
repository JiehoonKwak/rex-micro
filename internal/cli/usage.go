package cli

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/JiehoonKwak/rex-micro/internal/config"
	"github.com/JiehoonKwak/rex-micro/internal/daemon"
	"github.com/JiehoonKwak/rex-micro/internal/engine"
	usagelog "github.com/JiehoonKwak/rex-micro/internal/usage"
)

// usageCmd prints how often each binding was used, least used first.
func usageCmd(args []string) error {
	days := 30
	switch {
	case len(args) == 0:
	case len(args) == 2 && args[0] == "--days":
		n, err := strconv.Atoi(args[1])
		if err != nil || n < 1 {
			return fmt.Errorf("--days takes a whole number of days")
		}
		days = n
	default:
		return fmt.Errorf("usage: rex-micro usage [--days N]")
	}
	// The running daemon's applied bindings are what its counts were made
	// with; without a daemon, the config file is.
	var b daemon.Bound
	if err := daemon.Call("usage", &b); err == nil && b.Error != "" {
		return fmt.Errorf("the running daemon cannot report usage (%s); it is older than this rex-micro and updates itself within a minute, so try again then", b.Error)
	} else if err != nil {
		cfg, err := config.Load(config.Path())
		if err != nil {
			return err
		}
		b = daemon.Bound{Record: cfg.Usage.Record, Uses: engine.New(cfg, nil, nil, nil).Bound()}
	}
	log, err := usagelog.Load(daemon.UsagePath())
	if err != nil {
		return err // never report a record it could not read as zero use
	}
	return usageReport(os.Stdout, b, log, days, time.Now())
}

// usageReport lists every binding b has with its use over the last days
// days, least used first, so the keys worth rebinding come first.
func usageReport(out io.Writer, b daemon.Bound, log *usagelog.Log, days int, now time.Time) error {
	rows, since := log.Report(b.Uses, days, now)
	switch {
	case !b.Record:
		fmt.Fprintln(out, "Recording is off ([usage] record = false); these are the counts kept before.")
	case since == "":
		fmt.Fprintln(out, "Nothing recorded yet: the daemon counts pad presses as they happen.")
	default:
		fmt.Fprintf(out, "Last %d days; recorded since %s; `rex-micro press` is not counted.\n", days, since)
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	layer := ""
	for _, r := range sortRows(rows) {
		if r.Layer != layer {
			layer = r.Layer
			fmt.Fprintf(w, "\nlayer %s\n  KEY\tGESTURE\tDOES\tUSES\tDAYS\tLAST\n", layer)
		}
		last := r.Last
		if last == "" {
			last = "never"
		}
		fmt.Fprintf(w, "  %s\t%s\t%s\t%d\t%d\t%s\n", r.Key, r.Gesture, r.Does, r.Uses, r.Days, last)
	}
	return w.Flush()
}

// sortRows keeps the layers in config order and puts each layer's least
// used bindings first, ties in pad order.
func sortRows(rows []usagelog.Row) []usagelog.Row {
	order := map[string]int{}
	for i, r := range rows {
		if _, ok := order[r.Layer]; !ok {
			order[r.Layer] = i
		}
	}
	out := append([]usagelog.Row(nil), rows...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Layer != out[j].Layer {
			return order[out[i].Layer] < order[out[j].Layer]
		}
		return out[i].Uses < out[j].Uses
	})
	return out
}
