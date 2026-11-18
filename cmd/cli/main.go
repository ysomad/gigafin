// Command cli manages the users of gigafin, see internal/cli.
package main

import (
	"fmt"
	"os"

	"github.com/ysomad/gigafin/internal/cli"
)

func main() {
	if err := cli.Run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gigafin-cli:", err)
		os.Exit(1)
	}
}
