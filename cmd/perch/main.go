package main

import (
	"fmt"
	"os"
)

// stub: full CLI dispatch (7-verb subcommand switch + doctor/version) lands in M1-E.
var version = "dev"

func main() {
	fmt.Fprintf(os.Stderr, "perch %s\n", version)
	fmt.Fprintf(os.Stderr, "Usage: perch <command>\n")
	os.Exit(0)
}
