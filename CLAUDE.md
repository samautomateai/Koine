@AGENTS.md

## Claude Code specifics

- Shared skills are in `.agents/skills/` (symlinked at `.claude/skills`); sub-agents in `.claude/agents/`; slash commands in `.claude/commands/`.
- `.claude/settings.json` pre-approves the `task` targets and read-only Go/Git commands, and formats files after you edit them.
- Use the `reviewer` sub-agent before finishing a change, and `/spec-check` to verify requirements, design and tests still line up.
