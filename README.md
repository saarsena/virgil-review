# Virgil

A GitHub App that reviews every push using Claude.

Different from a PR reviewer: Virgil fires on `push` events, posts results
via the Checks API, and (in later phases) reads a per-repo project brain
at `.virgil/brain.md`.

## Status

**Phase 1.** Webhook receiver + stub reviewer + Check Run posting.
The reviewer returns hardcoded placeholder content; the real
Anthropic-backed review arrives in Phase 2.

## Layout

```
cmd/server/      virgil-server — the webhook receiver
cmd/virgil/      virgil — the operator CLI (only `version` in Phase 1)
pkg/reviewer/    ReviewResult schema, Review() stub, FormatCheckRun()
pkg/ghclient/    GitHub App auth + Check Run helpers
pkg/config/      YAML config loader
pkg/brain/       (stub — Phase 2)
pkg/classifier/  (stub — Phase 5)
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

## What Phase 1 does and doesn't do

Does:

- Verifies `X-Hub-Signature-256` (rejects 401 on mismatch)
- Handles push events: skips branch creation/deletion, warns on
  force-push but proceeds, otherwise creates a Check Run on the head
  SHA and updates it with stub review content
- Logs as JSON via `slog`

Doesn't (these are explicit non-goals for Phase 1):

- Call Anthropic — the reviewer is a stub
- Read `.virgil/brain.md` — Phase 2
- Classify changed files — Phase 5
- Track delivery IDs for idempotency — Phase 4
- Truncate large diffs — Phase 4

## Branch policy in v1

- **Branch deleted:** logged and ignored
- **Branch created:** logged and ignored (Phase 4 reconsiders)
- **Force push:** logged at WARN level, review proceeds
- **Normal push:** review runs

## Contributing

Phase 1 is intentionally narrow. Don't expand its scope here — add new
behavior in subsequent phases.
