// Command rex-micro turns a Work Louder Codex Micro into a controller for Rex.
package main

import (
	"os"

	"github.com/JiehoonKwak/rex-micro/internal/cli"
)

func main() { os.Exit(cli.Main(os.Args[1:])) }
