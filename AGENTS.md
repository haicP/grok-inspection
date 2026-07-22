# Repository Guidelines

## Project Structure & Module Organization

Grok Inspection is a CPA plugin built as a C shared library (`package main` at the repo root).

- Root `*.go`: entry (`main.go`), inspection engine, probe/classify, ban store/scheduler, management API, and embedded UI (`ui.go`, `i18n.go`).
- `*_test.go`: tests next to the code they cover.
- `cpasdk/pluginabi`, `cpasdk/pluginapi`: CPA host contracts—change only when the host ABI requires it.
- `docs/ARCHITECTURE.md`: runtime layout, routes, and probe flow.
- `build.sh` / `build.ps1`: local test + plugin build; `.github/workflows/` for CI/release.
- Ignore build/runtime output: `dist/`, `data/`, `*.so` / `*.dll` / `*.dylib`.

## Build, Test, and Development Commands

Requires **Go 1.22+** and a **C toolchain** (`CGO_ENABLED=1`).

```bash
go test ./... -count=1   # all packages, non-cached
./build.sh               # test + dist/grok-inspection.{so|dylib}
.\build.ps1              # Windows → dist/grok-inspection.dll
```

Load the artifact into CPA and enable `plugins.configs.grok-inspection`. See `README.md` for install and `CPA_MANAGEMENT_BASE_URL`.

## Coding Style & Naming Conventions

- Standard Go with tabs; run `gofmt` on touched files.
- Prefer short domain file names (`engine.go`, `probe.go`, `ban_store.go`).
- Route all new user-facing strings through `i18n` (Chinese default, English supported).
- Keep changes surgical; avoid drive-by refactors of the host SDK.

## Testing Guidelines

- Use the Go `testing` package only (no external test deps).
- Name tests `TestThingBehavior` (e.g. `TestClassifyPermissionDenied`).
- Cover classify/ban/engine/i18n/management paths you change.
- Tests must be hermetic—no live Grok or CPA. CI runs `go test ./... -count=1` on linux/windows/darwin.

## Commit & Pull Request Guidelines

History uses short conventional subjects: `fix:`, `docs:`, `chore:`, `release: vX.Y.Z`, sometimes scoped (`fix(pr-14):`).

PRs should state what/why, link issues when relevant, include `go test` (and build) evidence, and add UI screenshots or before/after notes for management-page or i18n changes.

## Security & Configuration Tips

Never log or persist full account tokens. Management actions need a CPA Management Key—do not embed secrets in source or fixtures. Fail closed on auth/config errors when calling the host for probe, disable, enable, or delete.
