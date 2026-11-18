// Command app serves gigafin, a personal finance manager: the web UI, the gigafin API
// and the background jobs, see internal/app.
package main

import (
	"fmt"
	"os"

	"github.com/ysomad/gigafin/internal/app"
)

func main() {
	if err := app.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "gigafin:", err)
		os.Exit(1)
	}
}
