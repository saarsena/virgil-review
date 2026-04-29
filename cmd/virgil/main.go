// Command virgil is the operator CLI.
//
// Phase 1 only ships `virgil version`. Subcommands like `review` and
// `brain init|show` arrive in later phases.
package main

import (
	"fmt"
	"os"
)

// version is the binary version string. It can be overridden at build
// time via -ldflags '-X main.version=...'.
var version = "0.1.0-phase1"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "version", "-v", "--version":
		fmt.Printf("virgil %s\n", version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "virgil: subcommand %q is not implemented yet (Phase 1)\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: virgil <command>")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "commands:")
	fmt.Fprintln(os.Stderr, "  version    print the virgil version")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "future phases will add: review, brain init, brain show")
}
