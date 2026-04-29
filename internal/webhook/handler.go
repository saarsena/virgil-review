// Package webhook implements Virgil's HTTP webhook receiver.
//
// It verifies GitHub's HMAC-SHA256 signature on every request,
// dispatches push events to the reviewer, and posts results back to
// GitHub via the Checks API.
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

	"github.com/saarsena/virgil-review/pkg/ghclient"
	"github.com/saarsena/virgil-review/pkg/reviewer"
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
	secret  []byte
	factory *ghclient.Factory
	logger  *slog.Logger
}

// New builds a Handler. The secret is the GitHub App's webhook secret
// in raw bytes; factory builds per-installation API clients.
func New(secret string, factory *ghclient.Factory, logger *slog.Logger) *Handler {
	return &Handler{
		secret:  []byte(secret),
		factory: factory,
		logger:  logger,
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

	// ACK fast — GitHub expects a webhook response within 10s. The
	// review work is moved to a background goroutine that uses a
	// detached context so it survives the request returning.
	writeOK(w, "accepted")

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Error("review panicked", "panic", fmt.Sprintf("%v", r))
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

	diff, err := ghclient.CompareDiff(ctx, client, owner, repo, before, after)
	if err != nil {
		return h.failCheckRun(ctx, client, log, owner, repo, runID, fmt.Errorf("fetch diff: %w", err))
	}

	result, err := reviewer.Review(ctx, diff)
	if err != nil {
		return h.failCheckRun(ctx, client, log, owner, repo, runID, fmt.Errorf("reviewer: %w", err))
	}

	title, summary, text, annotations := reviewer.FormatCheckRun(result)
	if err := ghclient.CompleteCheckRun(ctx, client, owner, repo, runID, "neutral", title, summary, text, annotations); err != nil {
		return err
	}
	log.Info("check run completed", "run_id", runID, "annotations", len(annotations))
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
