// Command gigafin is a personal finance manager: it serves the web UI, the gigafin API
// and the background jobs, and manages users, see internal/cli.
package main

import (
	"fmt"
	"os"

	"github.com/ysomad/gigafin/internal/cli"
)

func main() {
	if err := cli.Run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gigafin:", err)
		os.Exit(1)
	}
}
