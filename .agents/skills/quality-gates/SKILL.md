---
name: quality-gates
description: Use when a Koine CI job or `task check` fails, or before declaring work done. Maps each gate to its command and the usual fix.
---

# Quality gates

Run `task check:fast` while editing and `task check` before finishing. CI runs the same commands.

| Gate | Command | Usual fix |
|---|---|---|
| Go format | `task fmt:check` | `task fmt` |
| Go lint (golangci-lint incl. gosec) | `task lint` | Fix the finding. Real exclusions live in `.golangci.yml` with a reason; add one only with a reason |
| JS format / lint | `npm run format:check` / `npm run lint` | `npm run format` |
| Line and file length | `task size` | Wrap or split. Never raise the limit |
| Go tests, race | `task test:race` | Fix the test or the code |
| Go coverage ≥ 85%/package | `task cover:gate` | Add tests for the uncovered lines in `coverage.html` (`task cover`) |
| JS tests + coverage | `npm run test:cover` | Add tests |
| Docs consistency | `task docs:check` | Follow the message; see `spec-driven-change` |
| Dependency audit | `go mod verify`, `npm audit` | Update or replace the dependency |
| Vulnerabilities | `go run golang.org/x/vuln/cmd/govulncheck@latest ./...` | Upgrade the module or Go toolchain |
| Secrets | gitleaks (CI) | Remove the secret and rotate it |

Report the first failing gate and its output; do not claim success from a partial run.
