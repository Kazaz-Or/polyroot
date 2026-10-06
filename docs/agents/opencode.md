# OpenCode

- **Tested version:** 1.18.21. Checked against opencode.ai docs and upstream
  `anomalyco/opencode` at `1ddb087` on 2026-10-02.
- **Required flags:** none. The adapter works through the environment.

## How Polyroot launches it

```text
cwd:  <primary repo>
env:  OPENCODE_CONFIG_CONTENT={"instructions":[...],"permission":{"external_directory":{"<repo>/*":"allow",...}}}
argv: opencode <your args>
```

| Concern | Mechanism |
|---|---|
| Primary working directory | process cwd (OpenCode's project directory) |
| Additional directories | OpenCode has no multi-root concept. Paths outside the project are "external directories" gated by `permission.external_directory`. Polyroot allows `<repo>/*` for each additional repository through inline config |
| Write permissions | an allowed external directory "inherits the same defaults as the current workspace", so edits follow your normal `edit` permission |
| Workspace context | `instructions: ["<run>/workspace.md"]` in inline config. Upstream `mergeConfigConcatArrays` **concatenates** `instructions` with your own config |
| Primary repo instructions | OpenCode's own `AGENTS.md` / `CLAUDE.md` discovery |
| Extra repo instructions | Polyroot adds each additional repo's `AGENTS.md` (else `CLAUDE.md`) to `instructions` |
| Generated files | `<cache>/run/<id>/workspace.md`, removed on exit |

`OPENCODE_CONFIG_CONTENT` ranks above global and project config in
OpenCode's precedence order and below managed config. It affects only the
launched process. Polyroot never writes `opencode.json`.

## Limitations

- If you already export `OPENCODE_CONFIG_CONTENT`, Polyroot refuses to
  launch rather than clobber it. `polyroot doctor` warns about this.
- Permission objects deep-merge. If your config sets `external_directory`
  to a plain string such as `"deny"`, the inline object replaces it, and
  other external paths then fall back to OpenCode's default (`ask`).
- OpenCode permission patterns treat `*` and `?` as wildcards. Repository
  paths that contain them are rejected.
- The agent may try to open the generated `workspace.md` with a tool. That
  hits an `external_directory` prompt because the run dir isn't allowed. The
  content is already in its instructions.
