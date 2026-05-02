# Virgil

A GitHub App that reviews every push using Claude.

Different from a PR reviewer: Virgil fires on `push` events, posts results
via the Checks API, and (in later phases) reads a per-repo project brain
at `.virgil/brain.md`.

## Status

**Phase 2+.** Real Anthropic-backed reviewer, project brain
(`.virgil/brain.md`), and SQLite-backed idempotency + token usage
tracking. Reviews fire on pushes to the default branch only; webhook
retries are deduplicated by `(owner, repo, after_sha)`. Diff filtering
strips lockfiles / vendored / generated paths before sending. Reviewer
can propose brain additions; user accepts/rejects via the `virgil brain`
CLI.

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

Build and install with `make install-cli` (drops into `~/.local/bin/virgil`).

```sh
virgil version
virgil review                      # dry-run reviewer on HEAD~1..HEAD
virgil review <sha>                # dry-run on that commit (vs its parent)
virgil review <from>..<to>         # dry-run on a range
virgil brain list                  # pending suggestions across all repos
virgil brain show <id>             # full text + reason for one suggestion
virgil brain accept <id>           # append to .virgil/brain.md, mark accepted
virgil brain reject <id>           # discard
```

### `virgil review`

Runs the reviewer locally — no GitHub auth, no Check Run posting, no
SQLite writes. Useful for "what would Virgil say if I pushed this?"
before actually pushing. Reads `.virgil/brain.md` from the current
directory (skip with `--brain ""` if you don't want it considered).
Brain suggestions are printed but NOT queued — push to have them
queued for accept/reject.

Output formats: `--format text` (default, terminal-friendly),
`--format markdown` (pipeable to a renderer), `--format json` (raw,
suitable for scripting).

Requires `ANTHROPIC_API_KEY` in env (or `--api-key`). Other knobs:
`--model`, `--strictness`, `--max-tokens`.

### `virgil brain ...`

`brain accept` defaults to writing `.virgil/brain.md` in the current
directory. Override with `--brain PATH` if you want to inspect output
before committing it to a repo. The DB defaults to
`~/.local/share/virgil/state.db`; override with `--db PATH` if you've
moved it.

## Project brain

If a repository contains `.virgil/brain.md` at the pushed commit, its
contents are prepended to the diff with framing instructions before the
model reads it. Use it for project-specific conventions, intentional
design choices, and known quirks. The reviewer is told to flag diffs
that violate brain content.

The file is optional. Missing files are silently skipped. Files larger
than 32KB are truncated with a warning log.

### Brain auto-update

The reviewer may emit `brain_suggestions` in its review output — short
sentences proposing additions to `.virgil/brain.md` based on what it
saw in the push. These never apply automatically. Each suggestion is
queued in SQLite with an id and surfaced in the Check Run with an
`(id N)` marker. To act on them:

```sh
virgil brain list                  # see what's pending
virgil brain show 7                # inspect one
virgil brain accept 7              # append to .virgil/brain.md
git add .virgil/brain.md && git commit -m "..."
```

Rejected suggestions stay in the DB with `status = 'rejected'` for
audit but are not shown by `brain list` unless you pass
`--status rejected` (or `--status all`).

## Diff filtering

Before the model sees the diff, sections for noise paths are stripped
to save tokens and keep the model focused on real changes:

- Lockfiles: `go.sum`, `package-lock.json`, `yarn.lock`, `pnpm-lock.yaml`,
  `bun.lockb`, `Cargo.lock`, `Pipfile.lock`, `poetry.lock`, `uv.lock`,
  `composer.lock`, `Gemfile.lock`, `mix.lock`, `Podfile.lock`, `flake.lock`
- Directory prefixes: `vendor/`, `node_modules/`, `dist/`, `build/`,
  `target/`, `__pycache__/`, `.next/`, `.nuxt/`, `.svelte-kit/`,
  `out/`, `.godot/`, `.import/`
- Generated suffixes: `.pb.go`, `_pb2.py`, `_pb2_grpc.py`,
  `.min.js`, `.min.css`, `.map`

Filtered paths are logged as `filtered noise paths from diff` so you
can verify Virgil isn't silently dropping real signal.

## Storage

Virgil writes to a SQLite database at `~/.local/share/virgil/state.db`
(override via `storage.path` in `config.yaml`). Three tables:

- `reviews` — one row per `(owner, repo, after_sha)`; deduplicates
  webhook retries.
- `usage` — one row per Anthropic call; tracks token counts per push.
- `brain_suggestions` — queue of model-proposed additions to
  `.virgil/brain.md`, with status pending/accepted/rejected.

The database is created on first start; schema migrations are
idempotent.

## Branch policy

- **Branch deleted:** logged and ignored
- **Branch created:** logged and ignored
- **Non-default branch:** logged and ignored
- **Force push to default branch:** logged at WARN level, review proceeds
- **Normal push to default branch:** review runs

## Contributing

Phase 2 is intentionally narrow. Don't expand its scope here — add new
behavior in subsequent phases.
