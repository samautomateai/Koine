# Documentation map

| Document                           | Purpose                                                                 | Kept honest by                          |
| ---------------------------------- | ----------------------------------------------------------------------- | --------------------------------------- |
| [requirements.md](requirements.md) | What Koine must do, with IDs, status and the tests that prove it        | `task docs:check`                       |
| [design.md](design.md)             | How it is built, which requirements each part serves, where the code is | `task docs:check`                       |
| [adr/](adr/)                       | Why we chose X over Y                                                   | `task docs:check` (status, date, links) |
| [SPEC.md](SPEC.md)                 | User-visible behaviour and the extension wire protocol                  | tests named in requirements; review     |
| [ENGINEERING.md](ENGINEERING.md)   | Rules for code, tests, security and changes                             | CI gates                                |
| [UPSTREAM.md](UPSTREAM.md)         | How we track upstream Pi                                                | `compat/` tests                         |
| [REFERENCES.md](REFERENCES.md)     | Other projects we read for ideas (not tracked)                          | review                                  |
| [../CHANGELOG.md](../CHANGELOG.md) | What changed                                                            | review                                  |

## Lifecycle

Docs change in the same pull request as the code. A requirement moves `planned` → `implemented` when its tests exist;
a design section is added when structure is added; an ADR is added when a decision has alternatives. CI fails when they drift.
