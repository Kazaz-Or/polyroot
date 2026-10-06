# Pi

- **Tested version:** 0.80.6 (`@earendil-works/pi-coding-agent`). Checked
  against the bundled `README.md`, `docs/security.md` and `dist/` source on
  2026-10-02.
- **Required flags:** `--append-system-prompt`.
- **Optional extensions:** none needed.

## How Polyroot launches it

```text
cwd:  <primary repo>
argv: pi --append-system-prompt <run>/workspace.md
         --append-system-prompt <other repo>/AGENTS.md ...   (one per repo that has one)
         <your args>
```

| Concern | Mechanism |
|---|---|
| Primary working directory | process cwd |
| Additional directories | none needed. Pi "does not include a built-in sandbox", and its tools can read and write any path the user can, so every repository is already reachable. Polyroot tells the model where they are |
| Write permissions | unrestricted (Pi's design). Use a container if you need isolation (see Pi's `docs/containerization.md`) |
| Workspace context | `--append-system-prompt <file>`. Pi's `resolvePromptInput` reads the file when the path exists |
| Primary repo instructions | Pi's own discovery: `AGENTS.md` (or `CLAUDE.md`) from cwd and its parents, plus `~/.pi/agent/AGENTS.md` |
| Extra repo instructions | Polyroot appends each additional repo's `AGENTS.md` (else `CLAUDE.md`) with another `--append-system-prompt` |
| Generated files | `<cache>/run/<id>/workspace.md`, removed on exit |

## Limitations

- "Multi-root" is advisory. Pi has no root concept, so the workspace map is
  what scopes the agent. That's also why no extension is needed.
- Project-trust prompts (for `.pi/` resources) apply only to the primary
  repository, since that's the cwd.
- Live verification wasn't possible on the development machine: `pi -p`
  hung even without Polyroot (no provider configured).
