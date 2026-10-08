# AGENTS.md

Repo: **decisor** — Go CLI + library client for Ollama's `POST /v1/systemone` decision endpoint (probabilities, not prose). Go 1.24, module `github.com/aratan/decisor`.

## Commands

```bash
gofmt -l .                 # must print nothing
go vet ./...
go test -race -cover ./...
go build -o decisor ./cmd/decisor
```

That exact order is what CI runs (`.github/workflows/ci.yml`). No Makefile, no linter beyond gofmt/vet, no codegen.

- Unit tests are hermetic (`httptest`, no network): `go test ./...`
- Single test: `go test ./ollama -run TestDecide -v` (same for any `Test*` name)
- Root-package test `TestEjemplosDelRepositorio` validates every question file in `examples/` with `ollama.ParseQuestions` plus a mojibake check. Files that are not question sets must be whitelisted in `noEsPreguntas` (`examples_test.go`) or the test fails. It runs inside plain `go test ./...`.
- Integration tests need a live Ollama + decision-capable model (e.g. `ollama pull nimble`) and are skipped unless enabled:
  ```bash
  SYSTEMONE_E2E=1 go test ./ollama -run Integration -v   # SYSTEMONE_URL / SYSTEMONE_MODEL override defaults
  ```

## Layout

- `ollama/` — the library. Core is `systemone.go` (types, validation, JSON shape) and `client.go` (HTTP). `errors.go` maps server messages to typed errors.
- `cmd/decisor/` — CLI (`modelos`, `preguntar`). Thin: builds `ollama.DecideRequest`, formats output.
- `examples/` — hand-written question sets, enforced by the root test.
- No sub-packages, no workspace: two packages, one module.

## Rules that matter

- **Zero dependencies.** Stdlib only, by design. A PR adding a dependency must justify it in the body (CONTRIBUTING.md).
- **Validate locally before the network.** Constructors return errors wrapping `ollama.ErrInvalidRequest` so a bad schema never costs a round trip.
- **`Answer` JSON is hand-written.** `MarshalJSON`/`UnmarshalJSON` exist on purpose (discriminated union on `type`). If you add a field, update BOTH and the round-trip test (`TestAnswerMarshalRoundTrip`).
- **No hardcoded keys/endpoints.** Everything goes through `WithBaseURL` / options.
- **`noul` criteria: omit ≠ `null`.** Omitting is valid; explicit `null` is rejected by the server. Keep the client omitting by default.

## Gotchas

- **Question key names enter the prompt.** Renaming a question changes the probabilities. Never "fix" a name for style; documented in README with measurements.
- Questions are scored independently — answers are not guaranteed mutually consistent.
- State is embedded in full in every question's prompt (8194-token cap, 64 KiB body, 1–64 questions). Nothing is truncated client-side; oversized requests fail locally by design (`TestDecideRejectsOversizedBodyLocally`).
- `EstimatePromptTokens` is a ~4 chars/token heuristic that overestimates; `usage.input_tokens` is truth.

## Conventions

- Prose (README, CONTRIBUTING, code comments, commit messages) is **Spanish**; keep new prose consistent with the file you touch.
- README.md is the extensive source of truth (API limits, benchmark tables, interpretation guidance). Trust config/scripts over docs if they ever conflict.
- No `opencode.json`, no repo-local instruction files besides this one.
