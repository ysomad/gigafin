// Command gigafin is a personal finance manager.
//
// Usage:
//
//	gigafin serve
//	gigafin user add -login alice [-role admin]
//	gigafin user passwd -login alice
//	gigafin user role -login alice -role user
//
// Settings come from the environment, see internal/config.
package main

import (
	"fmt"
	"os"

	"github.com/ysomad/gigafin/internal/app"
)

func main() {
	if err := app.Run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gigafin:", err)
		os.Exit(1)
	}
}
