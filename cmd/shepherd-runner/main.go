package main

import (
	"fmt"
	"os"

	"github.com/alecthomas/kong"
)

type CLI struct {
	Serve ServeCmd `cmd:"" default:"1" help:"Run the runner HTTP server (default)"`
	Hook  HookCmd  `cmd:"" help:"Handle Claude Code Stop hook"`
}

func main() {
	cli := CLI{}
	ctx := kong.Parse(&cli,
		kong.Name("shepherd-runner"),
		kong.Description("Shepherd runner for coding tasks"),
	)
	if err := ctx.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
