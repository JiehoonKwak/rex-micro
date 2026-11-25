package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

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
		if err != nil || n < 1 || n > daemon.MaxUsageDays {
			return fmt.Errorf("--days takes a whole number of days, 1 to %d", daemon.MaxUsageDays)
		}
		days = n
	default:
		return fmt.Errorf("usage: rex-micro usage [--days N]")
	}
	// The running daemon has the live counts and the bindings it applied;
	// only when none runs do the saved record and the config file stand in.
	r, err := daemon.Usage(days)
	if errors.Is(err, daemon.ErrNotRunning) {
		r, err = savedReport(days)
	} else if err != nil && strings.Contains(err.Error(), "unknown command") {
		err = fmt.Errorf("%w; it is older than this rex-micro and updates itself within a minute, so try again then", err)
	}
	if err != nil {
		return err
	}
	return usageReport(os.Stdout, r)
}

// savedReport is the report from the saved record and the config file, for
// when no daemon runs.
func savedReport(days int) (daemon.Report, error) {
	cfg, err := config.Load(config.Path())
	if err != nil {
		return daemon.Report{}, err
	}
	log, err := usagelog.Load(daemon.UsagePath())
	if err != nil {
		return daemon.Report{}, err // never report a record it could not read as zero use
	}
	return daemon.NewReport(cfg.Usage.Record, engine.New(cfg, nil, nil, nil).Bound(), log, days, time.Now()), nil
}

// usageReport prints r, least used bindings first in each layer, so the
// keys worth rebinding come first.
func usageReport(out io.Writer, r daemon.Report) error {
	switch {
	case r.Record == nil || !*r.Record:
		fmt.Fprintln(out, "Recording is off ([usage] record = false); these are the counts kept before.")
	case r.Since == "":
		fmt.Fprintln(out, "Nothing recorded yet: the daemon counts pad presses as they happen.")
	default:
		fmt.Fprintf(out, "Last %d days; recorded since %s; `rex-micro press` is not counted.\n", r.Days, r.Since)
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	layer := ""
	for _, row := range sortRows(r.Rows) {
		if row.Layer != layer {
			layer = row.Layer
			fmt.Fprintf(w, "\nlayer %s\n  KEY\tGESTURE\tDOES\tUSES\tDAYS\tLAST\n", oneLine(layer))
		}
		last := row.Last
		if last == "" {
			last = "never"
		}
		fmt.Fprintf(w, "  %s\t%s\t%s\t%d\t%d\t%s\n", row.Key, row.Gesture, oneLine(row.Does), row.Uses, row.Days, last)
	}
	return w.Flush()
}

// oneLine keeps a name on its row: one with control characters, quotes or
// spaces at an end shows quoted and escaped, so distinct names never read
// alike.
func oneLine(s string) string {
	if s != strings.TrimSpace(s) || strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) || r == '"' || r == '\\' }) {
		return strconv.Quote(s)
	}
	return s
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
