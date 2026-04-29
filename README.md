# Virgil

A GitHub App that reviews every push using Claude.

Different from a PR reviewer: Virgil fires on `push` events, posts results
via the Checks API, and (in later phases) reads a per-repo project brain
at `.virgil/brain.md`.

## Status

**Phase 2.** Real Anthropic-backed reviewer, project brain
(`.virgil/brain.md`), and SQLite-backed idempotency + token usage
tracking. Reviews fire on pushes to the default branch only; webhook
retries are deduplicated by `(owner, repo, after_sha)`.

## Layout

```
cmd/server/       virgil-server — the webhook receiver
cmd/virgil/       virgil — the operator CLI (only `version` in Phase 1)
pkg/reviewer/     ReviewResult schema, Reviewer + Anthropic call, FormatCheckRun()
pkg/brain/        Reads .virgil/brain.md from the pushed commit
pkg/storage/      SQLite-backed idempotency + usage tracking
pkg/ghclient/     GitHub App auth + Check Run helpers
pkg/config/       YAML config loader
pkg/classifier/   (stub — Phase 5)
internal/webhook/ HMAC verify + push dispatch
```

## Prerequisites

- Go 1.24 or newer
- A registered GitHub App named `virgil-review` with:
  - Subscribed events: **Push**
  - Permissions: Contents=Read, Checks=Write, Metadata=Read, Pull requests=Read
  - Installed on a test repository
- The App's numeric ID, downloaded private key (`.pem`), and webhook secret
- A [smee.io](https://smee.io) channel for forwarding webhook deliveries
  to your local machine

## Setup

```sh
cp config.example.yaml config.yaml
# edit config.yaml: app_id, private_key_path, webhook_secret
```

Place the App private key somewhere the running process can read it
(the example assumes `./virgil-review.private-key.pem`). Both
`config.yaml` and `*.pem` are already in `.gitignore`.

## Running locally

In one terminal, start the webhook receiver:

```sh
go run ./cmd/server -config ./config.yaml
```

It listens on `:8081` by default with two endpoints:

- `POST /webhook` — GitHub deliveries land here
- `GET  /healthz` — returns `200 ok`

In a second terminal, forward your smee channel to it:

```sh
npx smee-client --url https://smee.io/<your-channel> --target http://localhost:8081/webhook
```

Then push to the test repo. You should see a structured JSON log line
on each delivery and a "Virgil Review" Check Run appear on the head SHA
in the GitHub UI within a few seconds.

## CLI

```sh
go run ./cmd/virgil version
```

## Project brain

If a repository contains `.virgil/brain.md` at the pushed commit, its
contents are prepended to the diff with framing instructions before the
model reads it. Use it for project-specific conventions, intentional
design choices, and known quirks. The reviewer is told to flag diffs
that violate brain content.

The file is optional. Missing files are silently skipped. Files larger
than 32KB are truncated with a warning log.

## Storage

Virgil writes to a SQLite database at `~/.local/share/virgil/state.db`
(override via `storage.path` in `config.yaml`). Two tables:

- `reviews` — one row per `(owner, repo, after_sha)`; deduplicates
  webhook retries.
- `usage` — one row per Anthropic call; tracks token counts per push.

The database is created on first start; schema migrations are
idempotent.

## What Phase 2 does and doesn't do

Does:

- Verifies `X-Hub-Signature-256` (rejects 401 on mismatch)
- Filters pushes to the repository's default branch
- Deduplicates retries by `(owner, repo, after_sha)`
- Fetches the unified diff and `.virgil/brain.md` in parallel
- Calls the Anthropic Messages API with a forced `submit_review` tool
  to get structured output
- Posts the result as a "Virgil Review" Check Run
- Records token usage to SQLite
- Logs as JSON via `slog`

Doesn't (explicit non-goals for Phase 2):

- Provide a CLI `review` subcommand — Phase 3
- Truncate large diffs or enforce token budgets — later phase
- Classify changed files into per-mode prompts (e.g. Godot-aware) — Phase 5
- Auto-update `.virgil/brain.md` from review output — Phase 4
- Review non-default branches — later phase

## Branch policy

- **Branch deleted:** logged and ignored
- **Branch created:** logged and ignored
- **Non-default branch:** logged and ignored
- **Force push to default branch:** logged at WARN level, review proceeds
- **Normal push to default branch:** review runs

## Contributing

Phase 2 is intentionally narrow. Don't expand its scope here — add new
behavior in subsequent phases.
