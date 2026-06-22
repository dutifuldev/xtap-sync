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
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.0 run
./scripts/check-go-coverage.sh
go run github.com/dutifuldev/slophammer/go/cmd/slophammer-go@v0.4.0 dry . --max-candidates 0
go run github.com/dutifuldev/slophammer/go/cmd/slophammer-go@v0.4.0 crap . --max-score 8
go run github.com/dutifuldev/slophammer/go/cmd/slophammer-go@v0.4.0 check . --coverage-profile coverage.out --only go.coverage-required --only go.dry-required --only go.crap-required
git diff --check
```

Use the pinned `go run` commands above so local checks match CI.

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
