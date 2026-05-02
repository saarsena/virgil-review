package reviewer

import (
	"fmt"
	"strings"

	"github.com/google/go-github/v66/github"
)

// maxAnnotations is GitHub's per-Check-Run-update annotation cap.
const maxAnnotations = 50

// FormatCheckRun renders a ReviewResult as the input to a GitHub
// Check Run update.
//
// Both the webhook server and (later) the CLI use this function — do
// not duplicate this logic anywhere.
//
// Returns:
//   - title: short headline shown next to the Check Run name
//   - summary: 1-3 paragraph markdown overview
//   - text: full markdown body with Risk Areas, Concerns, Suggestions
//   - annotations: file:line annotations, capped at 50 entries
func FormatCheckRun(r ReviewResult) (title, summary, text string, annotations []*github.CheckRunAnnotation) {
	title = formatTitle(r)
	summary = formatSummary(r)
	text = formatText(r)
	annotations = formatAnnotations(r.Annotations)
	return
}

func formatTitle(r ReviewResult) string {
	parts := []string{}
	if n := len(r.RiskAreas); n > 0 {
		parts = append(parts, fmt.Sprintf("%d risk area%s", n, plural(n)))
	}
	if n := len(r.Concerns); n > 0 {
		parts = append(parts, fmt.Sprintf("%d concern%s", n, plural(n)))
	}
	if n := len(r.Suggestions); n > 0 {
		parts = append(parts, fmt.Sprintf("%d suggestion%s", n, plural(n)))
	}
	if len(parts) == 0 {
		return "Virgil reviewed this push"
	}
	return "Virgil: " + strings.Join(parts, ", ")
}

func formatSummary(r ReviewResult) string {
	var b strings.Builder
	if r.Summary != "" {
		b.WriteString(r.Summary)
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b,
		"**%d** risk area%s · **%d** concern%s · **%d** suggestion%s · **%d** annotation%s",
		len(r.RiskAreas), plural(len(r.RiskAreas)),
		len(r.Concerns), plural(len(r.Concerns)),
		len(r.Suggestions), plural(len(r.Suggestions)),
		len(r.Annotations), plural(len(r.Annotations)),
	)
	if n := len(r.BrainSuggestions); n > 0 {
		fmt.Fprintf(&b, " · **%d** brain suggestion%s", n, plural(n))
	}
	return b.String()
}

func formatText(r ReviewResult) string {
	var b strings.Builder
	writeSection(&b, "Risk areas", r.RiskAreas)
	writeSection(&b, "Concerns", r.Concerns)
	writeSection(&b, "Suggestions", r.Suggestions)
	writeBrainSuggestions(&b, r.BrainSuggestions)
	if b.Len() == 0 {
		return "_Nothing to flag._"
	}
	return strings.TrimRight(b.String(), "\n")
}

func writeBrainSuggestions(b *strings.Builder, items []BrainSuggestion) {
	if len(items) == 0 {
		return
	}
	b.WriteString("## Brain suggestions\n\n")
	b.WriteString("_Run `virgil brain accept <id>` to append to `.virgil/brain.md`, or `virgil brain reject <id>` to dismiss._\n\n")
	for _, item := range items {
		if item.ID > 0 {
			fmt.Fprintf(b, "- **(id %d)** %s\n", item.ID, item.Text)
		} else {
			fmt.Fprintf(b, "- %s\n", item.Text)
		}
		if item.Reason != "" {
			fmt.Fprintf(b, "  _why: %s_\n", item.Reason)
		}
	}
	b.WriteString("\n")
}

func writeSection(b *strings.Builder, heading string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(b, "## %s\n\n", heading)
	for _, item := range items {
		fmt.Fprintf(b, "- %s\n", item)
	}
	b.WriteString("\n")
}

func formatAnnotations(in []Annotation) []*github.CheckRunAnnotation {
	if len(in) == 0 {
		return nil
	}
	if len(in) > maxAnnotations {
		in = in[:maxAnnotations]
	}
	out := make([]*github.CheckRunAnnotation, 0, len(in))
	for _, a := range in {
		level := normalizeLevel(a.Level)
		ann := &github.CheckRunAnnotation{
			Path:            strPtr(a.Path),
			StartLine:       intPtr(a.StartLine),
			EndLine:         intPtr(endLine(a)),
			AnnotationLevel: &level,
			Message:         strPtr(a.Message),
		}
		if a.Title != "" {
			ann.Title = strPtr(a.Title)
		}
		out = append(out, ann)
	}
	return out
}

func endLine(a Annotation) int {
	if a.EndLine == 0 {
		return a.StartLine
	}
	return a.EndLine
}

func normalizeLevel(level string) string {
	switch level {
	case "notice", "warning", "failure":
		return level
	default:
		return "notice"
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }
