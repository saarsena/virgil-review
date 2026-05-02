// Package webhook implements Virgil's HTTP webhook receiver.
//
// It verifies GitHub's HMAC-SHA256 signature on every request,
// checks idempotency synchronously, and dispatches eligible push events
// to a background goroutine that runs the reviewer and posts a Check Run.
package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/go-github/v66/github"
	"golang.org/x/sync/errgroup"

	"github.com/saarsena/virgil-review/pkg/brain"
	"github.com/saarsena/virgil-review/pkg/difffilter"
	"github.com/saarsena/virgil-review/pkg/ghclient"
	"github.com/saarsena/virgil-review/pkg/reviewer"
	"github.com/saarsena/virgil-review/pkg/storage"
)

const (
	checkRunName     = "Virgil Review"
	zeroSHA          = "0000000000000000000000000000000000000000"
	signatureHeader  = "X-Hub-Signature-256"
	eventTypeHeader  = "X-GitHub-Event"
	deliveryIDHeader = "X-GitHub-Delivery"
)

// Handler serves the /webhook endpoint.
type Handler struct {
	secret   []byte
	factory  *ghclient.Factory
	reviewer *reviewer.Reviewer
	store    *storage.Store
	logger   *slog.Logger
}

// New builds a Handler. secret is the GitHub App's webhook secret in
// raw bytes; factory builds per-installation API clients; rev is the
// shared Anthropic-backed reviewer; store provides idempotency and
// usage persistence.
func New(secret string, factory *ghclient.Factory, rev *reviewer.Reviewer, store *storage.Store, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		secret:   []byte(secret),
		factory:  factory,
		reviewer: rev,
		store:    store,
		logger:   logger,
	}
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 25*1024*1024))
	if err != nil {
		h.logger.Warn("read body", "error", err)
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	if !verifySignature(h.secret, r.Header.Get(signatureHeader), body) {
		h.logger.Warn("signature mismatch",
			"delivery", r.Header.Get(deliveryIDHeader),
			"event", r.Header.Get(eventTypeHeader),
		)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	event := r.Header.Get(eventTypeHeader)
	delivery := r.Header.Get(deliveryIDHeader)
	log := h.logger.With("event", event, "delivery", delivery)

	switch event {
	case "push":
		h.handlePush(r.Context(), log, body, w)
	case "ping":
		log.Info("ping received")
		writeOK(w, "pong")
	default:
		log.Info("ignored event")
		writeOK(w, "ignored")
	}
}

func (h *Handler) handlePush(ctx context.Context, log *slog.Logger, body []byte, w http.ResponseWriter) {
	var evt github.PushEvent
	if err := json.Unmarshal(body, &evt); err != nil {
		log.Warn("parse push event", "error", err)
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}

	repo := evt.GetRepo()
	owner := repo.GetOwner().GetName()
	if owner == "" {
		owner = repo.GetOwner().GetLogin()
	}
	repoName := repo.GetName()
	defaultBranch := repo.GetDefaultBranch()
	ref := evt.GetRef()
	before := evt.GetBefore()
	after := evt.GetAfter()
	deleted := evt.GetDeleted()
	created := evt.GetCreated()
	forced := evt.GetForced()
	installationID := evt.GetInstallation().GetID()

	log = log.With(
		"owner", owner,
		"repo", repoName,
		"ref", ref,
		"before", shortSHA(before),
		"after", shortSHA(after),
		"installation_id", installationID,
	)

	if deleted {
		log.Info("branch deleted, skipping review")
		writeOK(w, "skipped: branch deleted")
		return
	}
	if created || before == zeroSHA {
		log.Info("branch created, skipping review for v1")
		writeOK(w, "skipped: branch created")
		return
	}
	if forced {
		log.Warn("force push detected, proceeding for v1")
	}

	if installationID == 0 {
		log.Error("push event missing installation id")
		http.Error(w, "missing installation", http.StatusBadRequest)
		return
	}

	if defaultBranch == "" {
		log.Warn("push event missing default_branch; proceeding without filter")
	} else if ref != "refs/heads/"+defaultBranch {
		log.Info("push to non-default branch, skipping", "default_branch", defaultBranch)
		writeOK(w, "skipped: non-default branch")
		return
	}

	already, err := h.store.HasReviewed(ctx, owner, repoName, after)
	if err != nil {
		// Storage failure is transient infra; let GitHub retry.
		log.Error("idempotency check failed", "error", err)
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	if already {
		log.Info("skipped: already reviewed")
		writeOK(w, "skipped: already reviewed")
		return
	}

	// ACK fast — GitHub expects a webhook response within 10s. The
	// review work is moved to a background goroutine that uses a
	// detached context so it survives the request returning.
	writeOK(w, "accepted")

	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				log.Error("review panicked", "panic", fmt.Sprintf("%v", rec))
			}
		}()
		bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := h.reviewPush(bgCtx, log, owner, repoName, before, after, installationID); err != nil {
			log.Error("review failed", "error", err)
		}
	}()
}

func (h *Handler) reviewPush(ctx context.Context, log *slog.Logger, owner, repo, before, after string, installationID int64) error {
	client, err := h.factory.ClientFor(installationID)
	if err != nil {
		return fmt.Errorf("client for installation %d: %w", installationID, err)
	}

	runID, err := ghclient.CreateCheckRun(ctx, client, owner, repo, after, checkRunName)
	if err != nil {
		return err
	}
	log.Info("check run created", "run_id", runID)

	var (
		diff      string
		brainText string
	)
	// Diff fetch is essential and short-circuits the errgroup on failure.
	// Brain fetch is optional: a non-404 error (rate limit, transient 5xx)
	// is logged and degraded to an empty brain so the review still runs
	// on the diff alone. brain.Read already maps 404 to ("", nil) so we
	// only see hard errors here.
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		d, err := ghclient.CompareDiff(gctx, client, owner, repo, before, after)
		if err != nil {
			return fmt.Errorf("fetch diff: %w", err)
		}
		diff = d
		return nil
	})
	g.Go(func() error {
		b, err := brain.Read(gctx, client, owner, repo, after, log)
		if err != nil {
			log.Warn("brain fetch failed, proceeding without brain", "error", err)
			return nil
		}
		brainText = b
		return nil
	})
	if err := g.Wait(); err != nil {
		return h.failCheckRun(ctx, client, log, owner, repo, runID, err)
	}
	rawDiffBytes := len(diff)
	filtered := difffilter.Filter(diff)
	diff = filtered.Diff
	if len(filtered.Dropped) > 0 {
		log.Info("filtered noise paths from diff",
			"dropped", filtered.Dropped,
			"dropped_count", len(filtered.Dropped),
			"raw_bytes", rawDiffBytes,
			"filtered_bytes", len(diff),
		)
	}
	log.Info("inputs fetched", "diff_bytes", len(diff), "brain_bytes", len(brainText))

	result, usage, err := h.reviewer.Review(ctx, diff, brainText)
	if err != nil {
		return h.failCheckRun(ctx, client, log, owner, repo, runID, fmt.Errorf("reviewer: %w", err))
	}

	// Persist brain suggestions before formatting so the Check Run text
	// can render "(id N)" markers the user acts on via `virgil brain`.
	// A failed insert is logged but does not block the review — the
	// suggestion just won't be queued for accept/reject.
	for i := range result.BrainSuggestions {
		bs := &result.BrainSuggestions[i]
		id, insErr := h.store.InsertBrainSuggestion(ctx, owner, repo, after, bs.Text, bs.Reason)
		if insErr != nil {
			log.Error("insert brain suggestion failed", "error", insErr, "text", bs.Text)
			continue
		}
		bs.ID = id
	}
	if n := len(result.BrainSuggestions); n > 0 {
		log.Info("brain suggestions emitted", "count", n)
	}

	title, summary, text, annotations := reviewer.FormatCheckRun(result)
	if err := ghclient.CompleteCheckRun(ctx, client, owner, repo, runID, "neutral", title, summary, text, annotations); err != nil {
		return err
	}
	log.Info("check run completed",
		"run_id", runID,
		"annotations", len(annotations),
		"input_tokens", usage.InputTokens,
		"output_tokens", usage.OutputTokens,
	)

	// Mark AFTER posting. A storage failure here results in a duplicate
	// Check Run on the next retry, which is the lesser evil compared to
	// a marked-but-unposted state that would silently swallow the next
	// review attempt.
	if err := h.store.MarkReviewed(ctx, owner, repo, after, runID); err != nil {
		log.Error("mark reviewed failed", "error", err)
	}

	if err := h.store.RecordUsage(ctx, storage.UsageRecord{
		Model:               usage.Model,
		InputTokens:         usage.InputTokens,
		OutputTokens:        usage.OutputTokens,
		CacheReadTokens:     usage.CacheReadTokens,
		CacheCreationTokens: usage.CacheCreationTokens,
		Owner:               owner,
		Repo:                repo,
		AfterSHA:            after,
	}); err != nil {
		log.Error("record usage failed", "error", err)
	}

	return nil
}

func (h *Handler) failCheckRun(ctx context.Context, client *github.Client, log *slog.Logger, owner, repo string, runID int64, cause error) error {
	title := "Virgil errored"
	summary := "Virgil could not complete the review."
	text := fmt.Sprintf("```\n%s\n```", cause.Error())
	if err := ghclient.CompleteCheckRun(ctx, client, owner, repo, runID, "neutral", title, summary, text, nil); err != nil {
		log.Error("failed to mark check run errored", "error", err, "cause", cause)
	}
	return cause
}

// verifySignature performs a constant-time comparison of the HMAC-SHA256
// signature GitHub sends against the one we compute over the body.
func verifySignature(secret []byte, header string, body []byte) bool {
	if len(secret) == 0 || header == "" {
		return false
	}
	const prefix = "sha256="
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	got, err := hex.DecodeString(header[len(prefix):])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	want := mac.Sum(nil)
	return hmac.Equal(got, want)
}

func writeOK(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": msg})
}

func shortSHA(s string) string {
	if len(s) >= 7 {
		return s[:7]
	}
	return s
}
