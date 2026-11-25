## Summary

<!-- What does this PR change and why? -->

## Checklist

- [ ] `gofmt -l cmd internal` is clean (tabs, no formatting diffs)
- [ ] `go test ./...` passes locally
- [ ] `cd ui && npm run lint && npm run build` passes if `ui/` was touched
- [ ] New/changed env vars are documented in `.env.example`
- [ ] User-facing or operational changes are reflected in `docs/` — remember the 4-language standard (English · Русский · Español · 简体中文)
- [ ] `CHANGELOG.md` updated if the change is user-visible
- [ ] No secrets, tokens, or `.env` contents committed

## Test plan

<!-- How was this verified? e.g. `go test -race ./...`, `scripts/smoke.ps1`, manual UI check -->

- [ ] Verified locally

## Notes

<!-- Migration steps, rollout order, known limitations -->
