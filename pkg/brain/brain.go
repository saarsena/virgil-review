// Package brain reads a repository's per-repo project brain from
// .virgil/brain.md at a given commit SHA and exposes it to the reviewer.
//
// The brain file is optional: if absent (404), Read returns an empty
// string with no error. Files larger than maxBrainBytes are truncated
// with a warning log to keep the prompt within sane bounds.
package brain

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/google/go-github/v66/github"
)

const (
	// BrainPath is the in-repo path Virgil looks at for the project brain.
	BrainPath = ".virgil/brain.md"

	// maxBrainBytes caps how much brain content we forward to the model.
	// 32KB is generous for prose-style guidance and keeps token costs
	// predictable when someone accidentally checks in a giant file.
	maxBrainBytes = 32 * 1024
)

// Read fetches BrainPath at the given commit SHA. Missing files (404)
// return ("", nil). Other errors are wrapped and returned. Content
// over maxBrainBytes is truncated with a warning.
func Read(ctx context.Context, c *github.Client, owner, repo, sha string, logger *slog.Logger) (string, error) {
	if logger == nil {
		logger = slog.Default()
	}

	file, _, _, err := c.Repositories.GetContents(ctx, owner, repo, BrainPath, &github.RepositoryContentGetOptions{Ref: sha})
	if err != nil {
		var ghErr *github.ErrorResponse
		if errors.As(err, &ghErr) && ghErr.Response != nil && ghErr.Response.StatusCode == http.StatusNotFound {
			return "", nil
		}
		return "", fmt.Errorf("fetching %s @ %s: %w", BrainPath, sha, err)
	}
	if file == nil {
		return "", nil
	}

	content, err := file.GetContent()
	if err != nil {
		return "", fmt.Errorf("decoding %s: %w", BrainPath, err)
	}

	if len(content) > maxBrainBytes {
		logger.Warn("brain exceeds size cap, truncating",
			slog.String("path", BrainPath),
			slog.Int("bytes", len(content)),
			slog.Int("cap", maxBrainBytes),
		)
		content = content[:maxBrainBytes]
	}

	return content, nil
}
