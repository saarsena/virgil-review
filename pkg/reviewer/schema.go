// Package reviewer defines the review result schema and the function
// that produces it from a diff. In Phase 1 the Review function is a
// stub; Phase 2 will swap in a real Anthropic-backed implementation.
package reviewer

// ReviewResult is the structured output of a single review pass.
// It is intentionally softer than a strict pass/fail PR review:
// Concerns surface things worth a human's attention without
// implying a blocking failure.
type ReviewResult struct {
	Summary     string       `json:"summary"`
	RiskAreas   []string     `json:"risk_areas"`
	Concerns    []string     `json:"concerns"`
	Suggestions []string     `json:"suggestions"`
	Annotations []Annotation `json:"annotations"`
}

// Annotation is a file:line specific note attached to a Check Run.
// Level mirrors GitHub's Check Run annotation levels:
// "notice" | "warning" | "failure".
type Annotation struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Level     string `json:"level"`
	Message   string `json:"message"`
	Title     string `json:"title,omitempty"`
}
