// cmd/taskr/main.go
package main

import (
	"os"

	"github.com/thomas-cabral/taskr-cli/cli"
)

// taskr is the CLI a person types — an HTTP client and nothing else. It
// holds no domain logic and never opens a database; internal/cli is where
// everything beyond argument parsing lives.
func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}
