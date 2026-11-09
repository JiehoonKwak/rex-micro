// Command rex-micro turns a Work Louder Codex Micro into a controller for Rex.
package main

import (
	"os"
	"runtime"

	"github.com/JiehoonKwak/rex-micro/internal/cli"
)

// The main goroutine stays on the main thread, where AppKit must run the keys
// overlay (`rex-micro _overlay`).
func init() { runtime.LockOSThread() }

func main() { os.Exit(cli.Main(os.Args[1:])) }
