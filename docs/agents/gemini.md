# Gemini CLI

- **Tested version:** 0.62.0. It wasn't installed on the development machine:
  the npm package was installed into a scratch directory, and its `--help`
  and bundled source were inspected. Checked against geminicli.com docs on
  2026-10-02.
- **Required flags:** `--include-directories`.
- **Verification level:** Gemini accepts Polyroot's argv, but a model session
  wasn't run because no credentials were available. This is the least
  live-verified adapter.

## How Polyroot launches it

```text
cwd:  <primary repo>
argv: gemini --include-directories=<repo> ... --include-directories=<run>/gemini <your args>
file: <run>/gemini/GEMINI.md   (workspace map + context)
```

| Concern | Mechanism |
|---|---|
| Primary working directory | process cwd |
| Additional directories | `--include-directories=<dir>`, one per repository, in `=` form so the yargs array option can't swallow a forwarded prompt |
| Write permissions | include directories are full workspace directories (read and write, subject to approval mode). Gemini may ask you to trust newly included directories |
| Workspace context | Polyroot writes `GEMINI.md` into a run directory and includes that directory too. Gemini 0.62's `MemoryContextManager.refresh()` loads `GEMINI.md` from every workspace directory when the folder is trusted |
| Primary repo instructions | Gemini's own `GEMINI.md` discovery |
| Extra repo instructions | the same mechanism: `GEMINI.md` in each included repository (trusted folders) |
| Generated files | `<cache>/run/<id>/gemini/GEMINI.md`, removed on exit |

## Limitations

- **Untrusted folders load no project memory at all**, so the workspace
  context won't load either. Trust the primary repository in Gemini.
- Gemini reads memory files named by `context.fileName` (default `GEMINI.md`).
  If you changed that setting, the generated file isn't picked up.
- Paths containing `,` can't be passed, because Gemini splits the flag on
  commas. Polyroot fails with an error rather than drop the repository.
- The macOS seatbelt sandbox (`--sandbox` / `-s`) mounts at most **5** include
  directories (`MAX_INCLUDE_DIRS`). On macOS, Polyroot fails when you pass
  `--sandbox` with more, and warns otherwise. The context dir counts toward
  the limit.
- An earlier design set `context.loadMemoryFromIncludeDirectories` through
  `GEMINI_CLI_SYSTEM_DEFAULTS_PATH`. Gemini ignores system settings files that
  aren't owned by root, so the setting can't be injected without root access.
