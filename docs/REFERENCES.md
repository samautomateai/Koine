# References and what we took from them

**Only upstream Pi is tracked** (`docs/UPSTREAM.md`). Everything else below is reference reading: ideas, not dependencies, and we do not follow their releases.

Koine is a clean implementation; nothing is copied from these projects. The
reference sources were read as untrusted data and never executed.

| Project                                                    | Idea adopted                                                                                                                                     | Idea deliberately not adopted (yet)                                                                                                                                                                    |
| ---------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **Pi** (earendil-works/pi) — the one tracked upstream      | The extension API, event names, skill/prompt/agent formats, `/trust` and the `project_trust` model, the 1.5.2 baseline                           | Its TUI, session tree, package manager                                                                                                                                                                 |
| **PiG** (MichaelKinsy/PiG) — strict Go port with SDKs      | Pinned baseline reported by `--version`; extension SDKs in several languages over one wire protocol; a rubric for choosing an extension language | Faithful reimplementation of Pi internals. **PiG is closer to "Pi in Go"; Koine is "a Go host that runs Pi, opencode and Claude-style extensions".** If you only want Pi parity, PiG is the better fit |
| **sky-valley/pi** — Go port with `chord`, schema, difftest | Treating upstream tracking as a first-class, tested artifact; provenance pin; the idea of a differential harness                                 | Per-commit sync pipeline (see `docs/UPSTREAM.md`)                                                                                                                                                      |
| **dimetron/pi-go**                                         | Skill audit before use (we reject hidden-Unicode skills), `.agents` discovery, a visible roadmap                                                 | ACP/A2A, OTEL, LSP servers, kanban, sandboxes — large, separate decisions                                                                                                                              |
| **Twigpine/zero**                                          | Project-trust gating and graceful degradation as a product principle                                                                             | Not reviewed in depth: no source was supplied                                                                                                                                                          |

## Roadmap (not built)

- Session persistence (JSONL) and context compaction
- Rendering streamed responses in each frontend (the providers stream; see REQ-LLM-003)
- Sandboxed bash. We will not ship path allow-lists for a shell tool that can bypass them; this needs real isolation
- MCP client, ACP server, LSP tools
- Extension SDKs: Python, Go, Rust helpers over the JSON-RPC protocol in `docs/SPEC.md` §3
- `package.json` `pi` manifest for installing Pi packages
- Pi events/fields tracked as `unsupported` in `compat/pi.json` (run `koine -compat`)
