package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/saarsena/virgil-review/pkg/storage"
)

// defaultBrainPath is the in-repo location of the project brain. The
// CLI runs from the user's repo checkout so a relative path is correct.
const defaultBrainPath = ".virgil/brain.md"

// brainFlagsTakingValue is the set of flag names used by brain
// subcommands that consume a separate value token (e.g. `--db PATH`).
// splitBrainArgs uses this to recognize where a flag's value ends so
// users can write flags after positional args.
var brainFlagsTakingValue = map[string]bool{
	"db":     true,
	"brain":  true,
	"status": true,
}

// splitBrainArgs separates flag tokens from positional tokens so that
// `accept 7 --brain /tmp/x.md` and `accept --brain /tmp/x.md 7` both work.
// Stdlib flag.Parse stops at the first non-flag, which is hostile to
// the natural `<verb> <id> [opts]` shape.
func splitBrainArgs(args []string) (flagTokens, positional []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			positional = append(positional, a)
			continue
		}
		flagTokens = append(flagTokens, a)
		if strings.Contains(a, "=") {
			continue
		}
		name := strings.TrimLeft(a, "-")
		if brainFlagsTakingValue[name] && i+1 < len(args) {
			i++
			flagTokens = append(flagTokens, args[i])
		}
	}
	return
}

func runBrain(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: virgil brain <list|show|accept|reject> [args]")
		os.Exit(2)
	}
	switch args[0] {
	case "list":
		brainList(args[1:])
	case "show":
		brainShow(args[1:])
	case "accept":
		brainAccept(args[1:])
	case "reject":
		brainReject(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "virgil brain: unknown subcommand %q\n", args[0])
		os.Exit(2)
	}
}

// defaultDBPath returns ~/.local/share/virgil/state.db. Mirrors the
// server's default; users with a custom storage.path in config.yaml
// must pass --db explicitly.
func defaultDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "./state.db"
	}
	return filepath.Join(home, ".local", "share", "virgil", "state.db")
}

func openStore(dbPath string) *storage.Store {
	if _, err := os.Stat(dbPath); errors.Is(err, os.ErrNotExist) {
		fatalf("database %s does not exist (run the webhook server at least once, or pass --db)", dbPath)
	}
	store, err := storage.Open(dbPath)
	if err != nil {
		fatalf("opening database %s: %v", dbPath, err)
	}
	return store
}

func parseID(arg string) int64 {
	id, err := strconv.ParseInt(arg, 10, 64)
	if err != nil || id <= 0 {
		fatalf("expected a positive numeric id, got %q", arg)
	}
	return id
}

// --- list ---

func brainList(args []string) {
	fs := flag.NewFlagSet("brain list", flag.ExitOnError)
	dbPath := fs.String("db", defaultDBPath(), "path to the virgil SQLite database")
	status := fs.String("status", storage.BrainStatusPending, "filter by status: pending|accepted|rejected|all")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	filter := *status
	if filter == "all" {
		filter = ""
	}

	store := openStore(*dbPath)
	defer store.Close()

	rows, err := store.ListBrainSuggestions(context.Background(), filter)
	if err != nil {
		fatalf("listing suggestions: %v", err)
	}
	if len(rows) == 0 {
		if *status == "all" {
			fmt.Println("no brain suggestions yet.")
		} else {
			fmt.Printf("no %s brain suggestions.\n", *status)
		}
		return
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tREPO\tAGE\tSTATUS\tSUGGESTION")
	for _, r := range rows {
		fmt.Fprintf(tw, "%d\t%s/%s\t%s\t%s\t%s\n",
			r.ID, r.Owner, r.Repo, humanAge(r.SuggestedAt), r.Status, truncate(r.Text, 60),
		)
	}
	tw.Flush()
	fmt.Printf("\n%d %s. Use `virgil brain show <id>` for details.\n", len(rows), pluralWord(len(rows), "suggestion"))
}

// --- show ---

func brainShow(args []string) {
	flagTokens, posArgs := splitBrainArgs(args)
	fs := flag.NewFlagSet("brain show", flag.ExitOnError)
	dbPath := fs.String("db", defaultDBPath(), "path to the virgil SQLite database")
	if err := fs.Parse(flagTokens); err != nil {
		os.Exit(2)
	}
	if len(posArgs) != 1 {
		fmt.Fprintln(os.Stderr, "usage: virgil brain show <id>")
		os.Exit(2)
	}

	id := parseID(posArgs[0])
	store := openStore(*dbPath)
	defer store.Close()

	r, err := store.GetBrainSuggestion(context.Background(), id)
	if errors.Is(err, storage.ErrBrainSuggestionNotFound) {
		fatalf("no brain suggestion with id %d", id)
	}
	if err != nil {
		fatalf("fetching suggestion %d: %v", id, err)
	}

	fmt.Printf("ID:        %d\n", r.ID)
	fmt.Printf("Repo:      %s/%s\n", r.Owner, r.Repo)
	fmt.Printf("SHA:       %s\n", shortSHA(r.AfterSHA))
	fmt.Printf("Suggested: %s (%s)\n", r.SuggestedAt.Local().Format("2006-01-02 15:04"), humanAge(r.SuggestedAt))
	fmt.Printf("Status:    %s\n", r.Status)
	if r.DecidedAt != nil {
		fmt.Printf("Decided:   %s\n", r.DecidedAt.Local().Format("2006-01-02 15:04"))
	}
	fmt.Println()
	fmt.Println("Suggestion:")
	fmt.Printf("  %s\n", r.Text)
	if r.Reason != "" {
		fmt.Println()
		fmt.Println("Reason:")
		fmt.Printf("  %s\n", r.Reason)
	}
}

// --- accept ---

func brainAccept(args []string) {
	flagTokens, posArgs := splitBrainArgs(args)
	fs := flag.NewFlagSet("brain accept", flag.ExitOnError)
	dbPath := fs.String("db", defaultDBPath(), "path to the virgil SQLite database")
	brainPath := fs.String("brain", defaultBrainPath, "path to the brain.md file to append to")
	if err := fs.Parse(flagTokens); err != nil {
		os.Exit(2)
	}
	if len(posArgs) != 1 {
		fmt.Fprintln(os.Stderr, "usage: virgil brain accept <id>")
		os.Exit(2)
	}

	id := parseID(posArgs[0])
	store := openStore(*dbPath)
	defer store.Close()

	r, err := store.GetBrainSuggestion(context.Background(), id)
	if errors.Is(err, storage.ErrBrainSuggestionNotFound) {
		fatalf("no brain suggestion with id %d", id)
	}
	if err != nil {
		fatalf("fetching suggestion %d: %v", id, err)
	}
	if r.Status != storage.BrainStatusPending {
		fatalf("suggestion %d is already %s; cannot accept", id, r.Status)
	}

	if err := appendToBrain(*brainPath, r.Text); err != nil {
		fatalf("appending to %s: %v", *brainPath, err)
	}

	if err := store.DecideBrainSuggestion(context.Background(), id, storage.BrainStatusAccepted); err != nil {
		fatalf("marking accepted: %v", err)
	}

	fmt.Printf("Accepted suggestion %d. Appended to %s.\n", id, *brainPath)
	fmt.Println("Edit if needed, then `git add` and commit.")
}

// --- reject ---

func brainReject(args []string) {
	flagTokens, posArgs := splitBrainArgs(args)
	fs := flag.NewFlagSet("brain reject", flag.ExitOnError)
	dbPath := fs.String("db", defaultDBPath(), "path to the virgil SQLite database")
	if err := fs.Parse(flagTokens); err != nil {
		os.Exit(2)
	}
	if len(posArgs) != 1 {
		fmt.Fprintln(os.Stderr, "usage: virgil brain reject <id>")
		os.Exit(2)
	}

	id := parseID(posArgs[0])
	store := openStore(*dbPath)
	defer store.Close()

	if err := store.DecideBrainSuggestion(context.Background(), id, storage.BrainStatusRejected); err != nil {
		fatalf("rejecting suggestion %d: %v", id, err)
	}
	fmt.Printf("Rejected suggestion %d.\n", id)
}

// --- helpers ---

// appendToBrain appends a suggestion as a standalone paragraph at the
// end of the brain file. Creates the parent directory and the file if
// they do not yet exist. A leading blank line separates it from any
// existing content.
//
// Uses O_APPEND so a single write atomically lands at end-of-file —
// concurrent edits by the user can't be clobbered. Worst case under a
// race is an extra blank line, never lost data.
func appendToBrain(path, text string) error {
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}

	var sep string
	if info.Size() > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, info.Size()-1); err != nil {
			return err
		}
		if last[0] == '\n' {
			sep = "\n"
		} else {
			sep = "\n\n"
		}
	}

	if _, err := f.WriteString(sep + strings.TrimSpace(text) + "\n"); err != nil {
		return err
	}
	return nil
}

func humanAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return t.Local().Format("2006-01-02")
	}
}

func truncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return s[:max-1] + "…"
}

func shortSHA(s string) string {
	if len(s) >= 7 {
		return s[:7]
	}
	return s
}

func pluralWord(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}
