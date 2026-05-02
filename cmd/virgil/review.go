package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/saarsena/virgil-review/pkg/config"
	"github.com/saarsena/virgil-review/pkg/difffilter"
	"github.com/saarsena/virgil-review/pkg/reviewer"
)

// reviewFlagsTakingValue mirrors brainFlagsTakingValue: the set of
// flag names that consume a separate value token, so flags can appear
// after positional args. Stdlib flag.Parse stops at the first non-flag.
var reviewFlagsTakingValue = map[string]bool{
	"brain":      true,
	"model":      true,
	"strictness": true,
	"max-tokens": true,
	"api-key":    true,
	"format":     true,
}

func splitReviewArgs(args []string) (flagTokens, positional []string) {
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
		if reviewFlagsTakingValue[name] && i+1 < len(args) {
			i++
			flagTokens = append(flagTokens, args[i])
		}
	}
	return
}

// runReview implements `virgil review`: a local dry-run of the reviewer
// against a git diff range. No GitHub auth, no Check Run posting, no
// SQLite writes. Brain suggestions are printed but NOT queued.
func runReview(args []string) {
	flagTokens, posArgs := splitReviewArgs(args)
	fs := flag.NewFlagSet("review", flag.ExitOnError)
	brainPath := fs.String("brain", ".virgil/brain.md", "path to project brain (optional, missing file is fine)")
	model := fs.String("model", "claude-sonnet-4-6", "Anthropic model id")
	strictness := fs.String("strictness", config.StrictnessBalanced, "lenient|balanced|strict")
	maxTokens := fs.Int("max-tokens", 16384, "max output tokens")
	apiKey := fs.String("api-key", "", "Anthropic API key (defaults to $ANTHROPIC_API_KEY)")
	format := fs.String("format", "text", "output format: text|json|markdown")
	if err := fs.Parse(flagTokens); err != nil {
		os.Exit(2)
	}

	diffRange, err := resolveRange(posArgs)
	if err != nil {
		fatalf("%v", err)
	}

	switch *strictness {
	case config.StrictnessLenient, config.StrictnessBalanced, config.StrictnessStrict:
	default:
		fatalf("invalid --strictness %q (want lenient|balanced|strict)", *strictness)
	}

	key := *apiKey
	if key == "" {
		key = os.Getenv("ANTHROPIC_API_KEY")
	}
	if key == "" {
		fatalf("Anthropic API key required: set ANTHROPIC_API_KEY or pass --api-key")
	}

	diff, err := gitDiff(diffRange)
	if err != nil {
		fatalf("%v", err)
	}
	if strings.TrimSpace(diff) == "" {
		fmt.Fprintln(os.Stderr, "no changes in range; nothing to review")
		return
	}

	rawDiffBytes := len(diff)
	filtered := difffilter.Filter(diff)
	brainText := readBrainFile(*brainPath)

	cfg := config.Config{
		Anthropic: config.AnthropicConfig{
			APIKey:    key,
			Model:     *model,
			MaxTokens: *maxTokens,
		},
		Reviewer: config.ReviewerConfig{Strictness: *strictness},
	}

	// Reviewer's internal warnings (e.g. oversize-diff) go to stderr so
	// they don't pollute the formatted result on stdout.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	rev := reviewer.NewReviewer(cfg, logger)
	result, usage, err := rev.Review(context.Background(), filtered.Diff, brainText)
	if err != nil {
		fatalf("review failed: %v", err)
	}

	switch *format {
	case "json":
		emitJSON(diffRange, cfg.Anthropic.Model, rawDiffBytes, filtered, brainText, result, usage)
	case "markdown":
		emitMarkdown(diffRange, cfg.Anthropic.Model, filtered, result, usage)
	case "text", "":
		emitText(diffRange, cfg.Anthropic.Model, rawDiffBytes, filtered, brainText, result, usage)
	default:
		fatalf("unknown --format %q (want text|json|markdown)", *format)
	}
}

// resolveRange turns positional args into a `git diff` range expression.
// 0 args → HEAD~1..HEAD (the most recent commit alone)
// 1 arg containing ".." → used verbatim (e.g. origin/main..HEAD)
// 1 arg without ".." → <sha>^..<sha> (the diff that commit introduced)
func resolveRange(args []string) (string, error) {
	switch len(args) {
	case 0:
		return "HEAD~1..HEAD", nil
	case 1:
		a := args[0]
		if strings.Contains(a, "..") {
			return a, nil
		}
		return a + "^.." + a, nil
	default:
		return "", fmt.Errorf("usage: virgil review [<sha>|<from>..<to>] [flags]")
	}
}

func gitDiff(rng string) (string, error) {
	cmd := exec.Command("git", "diff", "--no-color", rng)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git diff %s: %w (%s)", rng, err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

// readBrainFile returns the brain content or "" if absent / unreadable.
// Mirrors the server's brain.Read behavior: missing is fine, oversized
// is truncated.
func readBrainFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	const maxBrainBytes = 32 * 1024
	if len(data) > maxBrainBytes {
		return string(data[:maxBrainBytes])
	}
	return string(data)
}

func emitText(rng, model string, rawDiffBytes int, filtered difffilter.Result, brainText string, r reviewer.ReviewResult, u reviewer.Usage) {
	fmt.Printf("Range:    %s\n", rng)
	fmt.Printf("Model:    %s\n", model)
	if rawDiffBytes != len(filtered.Diff) {
		fmt.Printf("Diff:     %d bytes raw → %d bytes after filter\n", rawDiffBytes, len(filtered.Diff))
	} else {
		fmt.Printf("Diff:     %d bytes\n", rawDiffBytes)
	}
	if brainText != "" {
		fmt.Printf("Brain:    %d bytes\n", len(brainText))
	}
	if n := len(filtered.Dropped); n > 0 {
		fmt.Printf("Filtered: %d noise path%s — %s\n", n, plural(n), strings.Join(filtered.Dropped, ", "))
	}
	fmt.Println()

	if r.Summary != "" {
		fmt.Println("Summary:")
		fmt.Printf("  %s\n\n", r.Summary)
	}

	printList("Risk areas", r.RiskAreas)
	printList("Concerns", r.Concerns)
	printList("Suggestions", r.Suggestions)

	if len(r.Annotations) > 0 {
		fmt.Println("Annotations:")
		for _, a := range r.Annotations {
			loc := fmt.Sprintf("%d", a.StartLine)
			if a.EndLine > 0 && a.EndLine != a.StartLine {
				loc = fmt.Sprintf("%d-%d", a.StartLine, a.EndLine)
			}
			fmt.Printf("  %s:%s [%s]\n    %s\n", a.Path, loc, a.Level, a.Message)
		}
		fmt.Println()
	}

	if len(r.BrainSuggestions) > 0 {
		fmt.Println("Brain suggestions (not queued — push to have them queued for accept/reject):")
		for _, bs := range r.BrainSuggestions {
			fmt.Printf("  - %s\n", bs.Text)
			if bs.Reason != "" {
				fmt.Printf("    why: %s\n", bs.Reason)
			}
		}
		fmt.Println()
	}

	fmt.Printf("Tokens:   %d in / %d out", u.InputTokens, u.OutputTokens)
	if u.CacheReadTokens > 0 || u.CacheCreationTokens > 0 {
		fmt.Printf(" (cache read %d, cache write %d)", u.CacheReadTokens, u.CacheCreationTokens)
	}
	fmt.Println()
}

func emitMarkdown(rng, model string, filtered difffilter.Result, r reviewer.ReviewResult, u reviewer.Usage) {
	title, summary, text, _ := reviewer.FormatCheckRun(r)
	fmt.Printf("# %s\n\n", title)
	fmt.Printf("_Range: `%s` · Model: `%s` · %d in / %d out tokens_\n\n",
		rng, model, u.InputTokens, u.OutputTokens)
	if n := len(filtered.Dropped); n > 0 {
		fmt.Printf("_Filtered %d noise path%s before review._\n\n", n, plural(n))
	}
	fmt.Println(summary)
	fmt.Println()
	fmt.Println(text)
}

func emitJSON(rng, model string, rawDiffBytes int, filtered difffilter.Result, brainText string, r reviewer.ReviewResult, u reviewer.Usage) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(map[string]any{
		"range":            rng,
		"model":            model,
		"brain_bytes":      len(brainText),
		"raw_diff_bytes":   rawDiffBytes,
		"filtered_diff_bytes": len(filtered.Diff),
		"dropped_paths":    filtered.Dropped,
		"usage":            u,
		"review":           r,
	})
}

func printList(label string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Printf("%s:\n", label)
	for _, item := range items {
		fmt.Printf("  - %s\n", item)
	}
	fmt.Println()
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
