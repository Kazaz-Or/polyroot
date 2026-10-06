# Codex CLI

- **Tested version:** codex-cli 0.144.5. Checked against learn.chatgpt.com and
  upstream `openai/codex` at `a20fe63` on 2026-10-02.
- **Required flags:** `--add-dir`, `--config`.

## How Polyroot launches it

```text
cwd:  <primary repo>
argv: codex --add-dir <repo> ... -c developer_instructions="<workspace map + context>" <your args>
```

| Concern | Mechanism |
|---|---|
| Primary working directory | process cwd. This is Codex's workspace root, and `AGENTS.md` discovery runs from it |
| Additional directories | `--add-dir`, one per repository: "Additional directories that should be writable alongside the primary workspace" |
| Write permissions | writable in `workspace-write`. **Ignored in `read-only`** (Codex prints "Ignoring --add-dir ... Switch to workspace-write or danger-full-access"). Reads outside the workspace follow Codex's own sandbox policy |
| Workspace context | `-c developer_instructions=...`, a per-run config override passed as an escaped TOML string, so any markdown is safe |
| Primary repo instructions | Codex's own discovery: `~/.codex/AGENTS.md`, then `AGENTS.override.md` / `AGENTS.md` from the git root down to cwd |
| Extra repo instructions | **not loaded by Codex** (upstream `core/src/agents_md.rs` walks only the cwd chain). The workspace map lists each repo's instruction files and tells the agent to read them |
| Generated files | none. Everything is in argv |

## Limitations

- `developer_instructions` is a single value. If your `~/.codex/config.toml`
  (or `$CODEX_HOME/config.toml`) sets it, Polyroot's override replaces yours
  for Polyroot launches. `polyroot doctor` warns about this.
- The workspace text sits in argv. That's fine for normal maps and contexts,
  but keep context files small: Linux caps a single argument at 128 KiB.
- Subcommands work as forwarded args, since options come first:
  `polyroot open ws --agent codex -- exec "fix the build"`.
