# Contributing to OVD // Highway

Thanks for your interest in contributing. This document describes the
development workflow and the quality bar we expect.

## Development Setup

```bash
go mod tidy
go run cmd/engine/main.go
```

Copy `.env.example` to `.env` and fill in local credentials — never commit
real secrets. The UI lives in `ui/`:

```bash
cd ui
npm install
npm run dev
```

Postgres and Redis are required; `docker compose up` provides both locally.

## Code Standards

- **Go** — `gofmt` clean (tabs), `go vet` clean, structured `slog` logging,
  `errgroup` for goroutine lifecycles, wrapped errors (`%w`), no ignored
  errors, explicit HTTP client timeouts.
- **TypeScript/React** — strict mode, no `any`, types mirroring Go JSON
  structs 1-to-1.
- **Docs** — user-facing or operational changes must be reflected in
  `docs/` following the 4-language standard (English · Русский · Español ·
  简体中文).

## Before Submitting

Run the same checks CI runs:

```bash
gofmt -l cmd internal        # must print nothing
go test -race ./...
cd ui && npm run lint && npm run build
```

For stack-level verification use `scripts/smoke.ps1` against a running
engine.

## Pull Requests

- Keep PRs small and focused; one concern per PR.
- Use [Conventional Commits](https://www.conventionalcommits.org/) for
  commit messages (`feat:`, `fix:`, `docs:`, `chore:`, `ci:`).
- Fill in the PR template checklist.
- Update `CHANGELOG.md` for user-visible changes.

## Reporting Issues

Use the issue templates — bug reports need reproduction steps and
logs; feature requests need a problem statement and proposed solution.
Security issues go through `SECURITY.md`, not public issues.
