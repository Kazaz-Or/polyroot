# Claude Code

- **Tested version:** 2.1.287 (Linux x86_64), checked against the docs at
  code.claude.com on 2026-10-02.
- **Required flags:** `--add-dir`, `--append-system-prompt` (checked against
  `claude --help` by `polyroot agents` and `polyroot doctor`).

## How Polyroot launches it

```text
cwd:  <primary repo>
env:  CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD=1   (only if you haven't set it)
argv: claude --add-dir <repo> ... --append-system-prompt-file <run>/workspace.md <your args>
```

| Concern | Mechanism |
|---|---|
| Primary working directory | process cwd |
| Additional directories | `--add-dir`, one flag per repository |
| Write permissions | `--add-dir` grants read and edit access, subject to Claude's normal permission mode |
| Workspace context | `--append-system-prompt-file` with the generated map plus your context file. This appends to the default system prompt and doesn't replace it |
| Primary repo instructions | Claude's own discovery: `CLAUDE.md`, `.claude/CLAUDE.md` and `CLAUDE.local.md` from cwd upward. `AGENTS.md` loads when there's no `CLAUDE.md` (v2.1.277+) |
| Extra repo instructions | `CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD=1` makes Claude load `CLAUDE.md`, `.claude/CLAUDE.md`, `.claude/rules/*.md` and `CLAUDE.local.md` from each `--add-dir` |
| Generated files | `<cache>/run/<id>/workspace.md`, removed when Claude exits |

## Limitations

- `AGENTS.md` in an additional repository is **not** loaded by Claude (docs:
  "Their `AGENTS.md` doesn't load"). The workspace map lists it, so Claude can
  read it on demand.
- Claude doesn't discover most `.claude/` configuration (skills, settings) in
  `--add-dir` directories. That's Claude's design.
- The system prompt is recorded on a conversation's first request. If you
  resume a conversation (`-- --continue`) after the workspace changed, Claude
  keeps the old map until compaction. See `--system-prompt-snapshot`.
- `--add-dir` is variadic. Polyroot always ends its own arguments with
  `--append-system-prompt-file`, so a forwarded prompt is never parsed as a
  directory.
