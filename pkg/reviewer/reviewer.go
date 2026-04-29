package reviewer

import "context"

// Review produces a ReviewResult for a unified diff.
//
// Phase 1: stub. Returns hardcoded content so the end-to-end push →
// Check Run pipeline can be exercised without Anthropic credentials.
// Phase 2 will replace the body with a real model call (and at that
// point will actually use ctx and diff).
func Review(_ context.Context, _ string) (ReviewResult, error) {
	return ReviewResult{
		Summary:   "Virgil review (Phase 1 stub). The real reviewer arrives in Phase 2.",
		RiskAreas: []string{"None — this is a stub response"},
		Concerns:  []string{},
		Suggestions: []string{
			"Wire up the real Anthropic call in Phase 2",
		},
		Annotations: []Annotation{},
	}, nil
}
