package reviewer

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"

	"github.com/saarsena/virgil-review/pkg/config"
)

// diffSizeWarnBytes is the threshold above which we log a warning about
// possible context-length issues. Diff filtering / truncation is a
// later-phase concern; for now the full diff is sent and the API
// surfaces overflow as an error.
const diffSizeWarnBytes = 50_000

// toolName is the Anthropic tool the model is forced to call. It mirrors
// the ReviewResult schema and gives us API-level structured-output
// enforcement instead of post-hoc JSON parsing.
const toolName = "submit_review"

// brainTemplate wraps the brain content with the framing paragraph that
// tells the model HOW to weight it. The framing is load-bearing — do
// not abbreviate.
const brainTemplate = `## Project Brain

The following is project-specific context the developer maintains. Use it to inform your review — the project's conventions, intentional design choices, and known quirks. If the diff appears to violate something in the brain, flag it explicitly.

%s

---

## Recent Changes

%s`

// Reviewer holds the Anthropic client and per-process configuration so
// the HTTP handler can reuse a single instance across requests.
type Reviewer struct {
	client     anthropic.Client
	model      string
	maxTokens  int
	strictness string
	logger     *slog.Logger
}

// Usage is the per-call token accounting returned alongside a ReviewResult.
//
// The reviewer package does not own storage; callers turn this into a
// storage.UsageRecord and persist it themselves.
type Usage struct {
	Model               string
	InputTokens         int
	OutputTokens        int
	CacheReadTokens     int
	CacheCreationTokens int
}

// NewReviewer constructs a Reviewer from the loaded configuration.
//
// The Anthropic client is created once and reused per call. The logger
// is retained so diff-size warnings surface in the same JSON log stream
// as the rest of the server.
func NewReviewer(cfg config.Config, logger *slog.Logger) *Reviewer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Reviewer{
		client:     anthropic.NewClient(option.WithAPIKey(cfg.Anthropic.APIKey)),
		model:      cfg.Anthropic.Model,
		maxTokens:  cfg.Anthropic.MaxTokens,
		strictness: cfg.Reviewer.Strictness,
		logger:     logger,
	}
}

// Review calls the Anthropic Messages API with a forced tool_use,
// decodes the tool input into a ReviewResult, and returns it along
// with token-usage accounting from the response.
//
// brain may be empty. When non-empty it is wrapped with brainTemplate
// and prepended to the diff in the user message.
func (r *Reviewer) Review(ctx context.Context, diff, brain string) (ReviewResult, Usage, error) {
	if len(diff) > diffSizeWarnBytes {
		r.logger.Warn("diff exceeds size threshold; sending unchanged",
			slog.Int("bytes", len(diff)),
			slog.Int("threshold", diffSizeWarnBytes),
		)
	}

	systemPrompt, err := SystemPrompt(r.strictness)
	if err != nil {
		return ReviewResult{}, Usage{}, fmt.Errorf("building system prompt: %w", err)
	}

	userMessage := buildUserMessage(diff, brain)

	tool := anthropic.ToolParam{
		Name:        toolName,
		Description: param.NewOpt("Submit your structured review of the push. Call this exactly once."),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: reviewToolProperties(),
			Required: []string{
				"summary",
				"risk_areas",
				"concerns",
				"suggestions",
				"annotations",
			},
		},
	}

	resp, err := r.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(r.model),
		MaxTokens: int64(r.maxTokens),
		System:    []anthropic.TextBlockParam{{Text: systemPrompt}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(userMessage)),
		},
		Tools:      []anthropic.ToolUnionParam{{OfTool: &tool}},
		ToolChoice: anthropic.ToolChoiceParamOfTool(toolName),
	})
	if err != nil {
		return ReviewResult{}, Usage{}, fmt.Errorf("anthropic messages: %w", err)
	}

	usage := Usage{
		Model:               r.model,
		InputTokens:         int(resp.Usage.InputTokens),
		OutputTokens:        int(resp.Usage.OutputTokens),
		CacheReadTokens:     int(resp.Usage.CacheReadInputTokens),
		CacheCreationTokens: int(resp.Usage.CacheCreationInputTokens),
	}

	for _, block := range resp.Content {
		tu := block.AsToolUse()
		if tu.Name != toolName {
			continue
		}
		var result ReviewResult
		if err := json.Unmarshal(tu.Input, &result); err != nil {
			return ReviewResult{}, usage, fmt.Errorf("decoding tool input: %w", err)
		}
		return result, usage, nil
	}

	return ReviewResult{}, usage, fmt.Errorf("model did not emit a %s tool call (stop_reason=%q)",
		toolName, resp.StopReason)
}

func buildUserMessage(diff, brain string) string {
	if strings.TrimSpace(brain) == "" {
		return diff
	}
	return fmt.Sprintf(brainTemplate, brain, diff)
}

// reviewToolProperties is the JSON-schema properties block for the
// submit_review tool. It mirrors the ReviewResult struct one-for-one;
// changing one without the other will silently drift.
func reviewToolProperties() map[string]any {
	stringArray := func(desc string) map[string]any {
		return map[string]any{
			"type":        "array",
			"description": desc,
			"items":       map[string]any{"type": "string"},
		}
	}
	annotationItem := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Repo-relative path of the file the annotation applies to.",
			},
			"start_line": map[string]any{
				"type":        "integer",
				"description": "1-indexed start line in the file.",
			},
			"end_line": map[string]any{
				"type":        "integer",
				"description": "Optional 1-indexed end line; omit or set equal to start_line for a single-line annotation.",
			},
			"level": map[string]any{
				"type":        "string",
				"enum":        []string{"notice", "warning", "failure"},
				"description": "Severity. Use sparingly: 'failure' implies the build should be considered red.",
			},
			"message": map[string]any{
				"type":        "string",
				"description": "Specific, actionable description of the issue.",
			},
			"title": map[string]any{
				"type":        "string",
				"description": "Optional short headline shown in the GitHub UI.",
			},
		},
		"required": []string{"path", "start_line", "level", "message"},
	}
	return map[string]any{
		"summary": map[string]any{
			"type":        "string",
			"description": "A 2-4 sentence overall summary of the push. State what changed and your overall judgment.",
		},
		"risk_areas":  stringArray("Fragile code, untested edge cases, places where assumptions might not hold. Cite file:line where possible. Empty array if none."),
		"concerns":    stringArray("Correctness bugs, security issues, and things you'll regret in three days (TODOs, commented-out code, half-finished refactors). The must-fix-now bucket. Cite file:line. Empty array if none."),
		"suggestions": stringArray("Optional polish or refactor ideas — usually empty for solo work. Empty array preferred over weak guesses."),
		"annotations": map[string]any{
			"type":        "array",
			"description": "Optional file:line specific notes. At most a handful per review.",
			"items":       annotationItem,
		},
	}
}
