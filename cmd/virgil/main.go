// Command virgil is the operator CLI.
//
// Subcommands:
//   - version            print the binary version
//   - brain list         list brain suggestions queued by the reviewer
//   - brain show <id>    show full text + reason for a suggestion
//   - brain accept <id>  append suggestion to .virgil/brain.md, mark accepted
//   - brain reject <id>  mark suggestion rejected (no file change)
package main

import (
	"fmt"
	"os"
)

// version is the binary version string. Override at build time via
// -ldflags '-X main.version=...'.
var version = "0.2.0-phase2"

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
	case "brain":
		runBrain(os.Args[2:])
	case "review":
		runReview(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "virgil: unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: virgil <command> [args]")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "commands:")
	fmt.Fprintln(os.Stderr, "  version              print the virgil version")
	fmt.Fprintln(os.Stderr, "  review [<range>]     run the reviewer locally on a git diff range")
	fmt.Fprintln(os.Stderr, "  brain list           list pending brain suggestions")
	fmt.Fprintln(os.Stderr, "  brain show <id>      show a suggestion in full")
	fmt.Fprintln(os.Stderr, "  brain accept <id>    append suggestion to .virgil/brain.md")
	fmt.Fprintln(os.Stderr, "  brain reject <id>    discard a pending suggestion")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "review examples:")
	fmt.Fprintln(os.Stderr, "  virgil review                          # last commit (HEAD~1..HEAD)")
	fmt.Fprintln(os.Stderr, "  virgil review 4842bd5                  # that commit vs its parent")
	fmt.Fprintln(os.Stderr, "  virgil review origin/main..HEAD        # range")
	fmt.Fprintln(os.Stderr, "  virgil review HEAD --format markdown   # markdown output")
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "virgil: "+format+"\n", args...)
	os.Exit(1)
}
