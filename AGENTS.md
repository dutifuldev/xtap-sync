# AGENTS.md

These instructions apply to this repository.

## Slophammer Standards

This Go repository follows the Slophammer guidance from
`dutifuldev/slophammer/docs/AGENT_ENTRYPOINT.md`.

## Local Checks

Before finishing a change, run:

```sh
gofmt -w cmd internal
go test -coverprofile=coverage.out ./...
go vet ./...
slophammer-go check . --coverage-profile coverage.out
git diff --check
```

If `slophammer-go` is not installed, use the matching Go checker from a local
Slophammer checkout and state that path in the handoff.

## Go Rules

- Keep domain merge/deduplication logic in `internal/syncer` separate from CLI
  parsing and launchd process concerns.
- Keep synced tweet JSONL files under `data/tweets/YYYY/MM/`; do not write
  managed xTap data files at the repository root.
- Prefer the Go standard library; do not add runtime dependencies unless they
  remove meaningful complexity.
- Add tests for sync, deduplication, git conflict recovery, service generation,
  and verification behavior.
- Never commit xTap media downloads, partial files, logs, credentials, or local
  machine-specific output.
