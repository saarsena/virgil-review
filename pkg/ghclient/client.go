// Package ghclient handles GitHub App authentication and the small
// set of GitHub API operations Virgil needs (Check Runs and the
// compare-commits diff).
//
// A single Factory is created per process and holds the App-level
// transport. Per-installation clients are built on demand.
package ghclient

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v66/github"
)

// Factory creates per-installation GitHub clients authenticated as
// the Virgil App for the requested installation.
type Factory struct {
	appID      int64
	privateKey []byte
	atr        *ghinstallation.AppsTransport
}

// NewFactory builds a Factory by reading the App's PEM-encoded private
// key from disk and constructing the App-level (JWT-signing) transport.
func NewFactory(appID int64, privateKeyPath string) (*Factory, error) {
	key, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("reading private key %s: %w", privateKeyPath, err)
	}
	atr, err := ghinstallation.NewAppsTransport(http.DefaultTransport, appID, key)
	if err != nil {
		return nil, fmt.Errorf("building apps transport: %w", err)
	}
	return &Factory{
		appID:      appID,
		privateKey: key,
		atr:        atr,
	}, nil
}

// ClientFor returns a *github.Client authenticated as the given
// installation. The underlying ghinstallation transport caches and
// auto-refreshes installation tokens.
func (f *Factory) ClientFor(installationID int64) (*github.Client, error) {
	itr := ghinstallation.NewFromAppsTransport(f.atr, installationID)
	return github.NewClient(&http.Client{Transport: itr}), nil
}

// CreateCheckRun opens an in-progress Check Run on the given head SHA
// and returns its ID for later updates.
func CreateCheckRun(ctx context.Context, c *github.Client, owner, repo, headSHA, name string) (int64, error) {
	status := "in_progress"
	run, _, err := c.Checks.CreateCheckRun(ctx, owner, repo, github.CreateCheckRunOptions{
		Name:    name,
		HeadSHA: headSHA,
		Status:  &status,
	})
	if err != nil {
		return 0, fmt.Errorf("creating check run for %s/%s@%s: %w", owner, repo, headSHA, err)
	}
	return run.GetID(), nil
}

// CompleteCheckRun marks a Check Run as completed with the given
// conclusion ("neutral", "success", "failure", etc.) and attaches the
// formatted output (title, summary, text, annotations).
//
// GitHub limits a single Check Run update to 50 annotations. Callers
// should pre-truncate; this helper does not.
func CompleteCheckRun(
	ctx context.Context,
	c *github.Client,
	owner, repo string,
	runID int64,
	conclusion, title, summary, text string,
	annotations []*github.CheckRunAnnotation,
) error {
	status := "completed"
	output := &github.CheckRunOutput{
		Title:       &title,
		Summary:     &summary,
		Text:        &text,
		Annotations: annotations,
	}
	_, _, err := c.Checks.UpdateCheckRun(ctx, owner, repo, runID, github.UpdateCheckRunOptions{
		Name:       "Virgil Review",
		Status:     &status,
		Conclusion: &conclusion,
		Output:     output,
	})
	if err != nil {
		return fmt.Errorf("completing check run %d on %s/%s: %w", runID, owner, repo, err)
	}
	return nil
}

// CompareDiff fetches the unified diff between two commits using the
// repos.CompareCommitsRaw endpoint with the application/vnd.github.v3.diff
// media type.
func CompareDiff(ctx context.Context, c *github.Client, owner, repo, base, head string) (string, error) {
	diff, _, err := c.Repositories.CompareCommitsRaw(ctx, owner, repo, base, head, github.RawOptions{Type: github.Diff})
	if err != nil {
		return "", fmt.Errorf("fetching diff %s/%s %s...%s: %w", owner, repo, base, head, err)
	}
	return diff, nil
}
