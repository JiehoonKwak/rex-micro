package daemon

// The CLI side of the control socket.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"time"
)

// Call sends one command to a running daemon and decodes its reply into out.
func Call(cmd string, out any) error {
	return call(request{Cmd: cmd}, 5*time.Second, out)
}

// Press makes the running daemon handle key as a pad press with gesture
// (press, double or hold) and waits until its effects have run.
func Press(key, gesture string) (Pressed, error) {
	var out Pressed
	err := call(request{Cmd: "press", Key: key, Gesture: gesture}, shellTimeout+10*time.Second, &out)
	return out, err
}

// Usage asks the running daemon for its usage report over days days. It
// returns ErrNotRunning only when no daemon listens; any other failure,
// including a daemon too old for the command, is an error, never a report.
func Usage(days int) (Report, error) {
	var out Report
	if err := call(request{Cmd: "usage", Days: days}, 5*time.Second, &out); err != nil {
		if _, serr := os.Stat(SocketPath()); errors.Is(err, ErrNotRunning) && !errors.Is(serr, fs.ErrNotExist) {
			// A socket that is there but does not answer may be a daemon
			// that is busy or not ours to reach: not proof that none runs.
			return Report{}, fmt.Errorf("the daemon's socket %s did not answer: %v", SocketPath(), err)
		}
		return Report{}, err
	}
	if out.Error != "" {
		return Report{}, fmt.Errorf("the running daemon could not report usage: %s", out.Error)
	}
	if out.Version != UsageReportVersion || out.Record == nil || out.Rows == nil || out.Days != min(max(days, 1), MaxUsageDays) {
		return Report{}, errors.New("the running daemon sent no usage report this rex-micro can read")
	}
	return out, nil
}

func call(r request, timeout time.Duration, out any) error {
	c, err := net.DialTimeout("unix", SocketPath(), time.Second)
	if err != nil {
		return fmt.Errorf("%w (%v)", ErrNotRunning, err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(timeout))
	msg, _ := json.Marshal(r)
	if _, err := c.Write(append(msg, '\n')); err != nil {
		return err
	}
	line, err := bufio.NewReader(c).ReadBytes('\n')
	if err != nil {
		return err
	}
	return json.Unmarshal(line, out)
}

// Pause stops a running daemon from acting on the pad until release is called.
// It returns a no-op release when no daemon runs.
func Pause() (release func(), err error) {
	c, err := net.DialTimeout("unix", SocketPath(), time.Second)
	if err != nil {
		return func() {}, nil
	}
	if _, err := fmt.Fprintln(c, `{"cmd":"pause"}`); err != nil {
		c.Close()
		return nil, err
	}
	if _, err := bufio.NewReader(c).ReadBytes('\n'); err != nil {
		c.Close()
		return nil, err
	}
	return func() { c.Close() }, nil
}

// ErrNotRunning means no daemon answered on the socket.
var ErrNotRunning = errors.New("rex-micro is not running (start it with `rex-micro service install`)")
