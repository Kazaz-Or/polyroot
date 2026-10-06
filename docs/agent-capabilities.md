# Agent capability investigation

This is the research Polyroot v0.1 is built on. It was done on 2026-10-02.
Agents change often, so treat this as a snapshot. Each `docs/agents/<name>.md`
holds the per-agent details.

## Machine

| | |
|---|---|
| OS / arch | Linux 7.2 (Fedora 43), x86_64 |
| Go | 1.27.1 |
| Node | 24.20.0 (runtime for pi and Gemini CLI) |

## Installed agents

| Agent | Binary | Version | How it was inspected |
|---|---|---|---|
| Claude Code | `claude` | 2.1.287 | local `--help`, `--version`, official docs at code.claude.com |
| Codex CLI | `codex` | 0.144.5 | local `--help`, docs at learn.chatgpt.com, upstream source `openai/codex` @ `a20fe63` |
| OpenCode | `opencode` | 1.18.21 | local `--help`, docs at opencode.ai, upstream source `anomalyco/opencode` @ `1ddb087` |
| Pi | `pi` | 0.80.6 | local `--help`, bundled `README.md`, `docs/security.md`, `dist/` source |
| Gemini CLI | `gemini` | 0.62.0 | **not installed globally**. The npm package was installed into a scratch dir to read `--help` and the bundled source. Docs at geminicli.com |

The `--help` output for each agent is saved under `internal/agent/testdata/` as
test fixtures.

## Capability matrix

| | Claude Code | Codex CLI | Gemini CLI | OpenCode | Pi |
|---|---|---|---|---|---|
| Primary cwd | process cwd | process cwd (`-C` also exists) | process cwd | process cwd (`[project]` positional also exists) | process cwd |
| Extra roots | `--add-dir <dir>` (native, read+write) | `--add-dir <dir>` (native, **writable** roots) | `--include-directories=<dir>` (native) | none. Extra paths are "external directories" gated by the `permission.external_directory` config | none needed. Pi has **no sandbox**, so tools can reach any path |
| How Polyroot grants access | flags | flags | flags | inline config `OPENCODE_CONFIG_CONTENT` with `external_directory: {"<root>/*": "allow"}` | nothing to grant. The roots are listed in the context |
| Root limit | none documented | none | none, except the **macOS seatbelt sandbox** (`-s`), which mounts at most 5 include dirs (`MAX_INCLUDE_DIRS = 5` in source) | none | none |
| Workspace context | `--append-system-prompt-file <file>` | `-c developer_instructions="<toml string>"` | generated `GEMINI.md` in a Polyroot run dir, added as one more include dir (Gemini 0.62 loads `GEMINI.md` from every workspace dir of a trusted folder) | `instructions: ["<file>"]` in `OPENCODE_CONFIG_CONTENT` (arrays concatenate with user config) | `--append-system-prompt <file>` (reads the file if the path exists) |
| Primary repo instructions | `CLAUDE.md` from cwd and its parents. `AGENTS.md` when there's no `CLAUDE.md` (v2.1.277+) | `AGENTS.md` from git root down to cwd | `GEMINI.md` hierarchy | `AGENTS.md`/`CLAUDE.md` from cwd and its parents | `AGENTS.md`/`CLAUDE.md` from cwd and its parents |
| Extra-root instructions | native with env `CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD=1` (CLAUDE.md only, **not** AGENTS.md) | **not loaded** (source: discovery walks only the cwd chain). Polyroot lists the files in the workspace map | native: `GEMINI.md` from every include dir (trusted folders) | Polyroot adds each extra repo's `AGENTS.md` (or `CLAUDE.md`) to `instructions` | Polyroot appends each extra repo's `AGENTS.md` (or `CLAUDE.md`) with more `--append-system-prompt` flags |
| Agent-global config changed? | no | no (a `-c` override applies to this run only) | no | no (env-only, this run only) | no |

## Findings that shaped the design

1. **Every target agent has a usable native path.** None of them needs symlinks
   or copies. The fallback ladder (native, config, extension, adapter, error)
   ends at "native" or "config" for all five. Pi needs no extension because it
   doesn't restrict paths at all.
2. **Variadic flag parsing is a real hazard.**
   - Claude's `--add-dir <directories...>` (commander) and Gemini's
     `--include-directories` (yargs array) are greedy. A user prompt forwarded
     after them could be swallowed as a directory.
   - Mitigations:
     - Claude: Polyroot always ends its own args with a non-variadic flag
       (`--append-system-prompt-file`).
     - Gemini: Polyroot uses the `--flag=value` form.
     - Codex: clap takes one value per flag.
3. **Gemini splits `--include-directories` on commas.** Repository paths that
   contain `,` can't be represented, so the adapter rejects them with an error
   and never drops them.
4. **Codex `--add-dir` is ignored in a `read-only` sandbox.** Codex prints its
   own warning. Polyroot documents this and doesn't override the sandbox.
5. **Codex `developer_instructions` is a single config value.** Polyroot's `-c`
   override shadows any `developer_instructions` in the user's
   `~/.codex/config.toml` for that run. `polyroot doctor` detects this and
   warns.
6. **OpenCode's `OPENCODE_CONFIG_CONTENT` is one env var.** If the user already
   exports one, Polyroot refuses to launch rather than clobber it.
7. **Claude's `CLAUDE.md` loading for extra dirs skips `AGENTS.md`.** Polyroot's
   generated workspace map lists every instruction file it finds in every repo,
   so any agent can be told where they are.
8. **Gemini's include-dir memory loading** comes from bundled source (0.62.0):
   `MemoryContextManager.refresh()` walks every workspace directory of a
   trusted folder. The `context.loadMemoryFromIncludeDirectories` setting only
   affects `/directory add`. The first design injected that setting through
   `GEMINI_CLI_SYSTEM_DEFAULTS_PATH`. Gemini rejects that because it refuses
   system settings files that aren't owned by root, which a live test showed.
   No Gemini credentials were available, so the model-side result isn't
   verified live. Polyroot's argv is accepted.

## Live verification (2026-10-02)

`polyroot open demo --agent X -- <headless prompt>` against a 4-repo fixture,
with one path containing a space:

| Agent | Result |
|---|---|
| Claude Code 2.1.287 | ✓ named the workspace and all repos from the injected context, and read a file in an `--add-dir` repo. Run dir removed afterwards |
| OpenCode 1.18.21 | ✓ read files in extra repos with no `external_directory` prompt, and loaded the instruction files |
| Codex 0.144.5 | argv accepted. The model call was refused (the account's configured model needs a newer CLI), and Polyroot passed the exit code through |
| Pi 0.80.6 | not verifiable: `pi -p` hangs on this machine even without Polyroot (no provider configured) |
| Gemini 0.62.0 | argv accepted, stopped at "set an Auth method" |

## v0.1 architecture

```text
cmd/polyroot/main.go        entry point
internal/config             YAML schema, XDG dirs, path expansion and canonicalization, structural checks
internal/workspace          resolve workspace -> ordered, deduplicated repo set; generate workspace map
internal/agent              Adapter interface, detection, LaunchSpec, built-in adapters, custom adapters
internal/cli                cobra commands: open, command, list, show, validate, agents, doctor
```

- The **resolver** knows nothing about agents. It outputs a `workspace.Resolved`
  (name, primary, ordered repos with group provenance, context file).
- An **adapter** is a static description (binary, required help flags,
  capabilities) plus `Build(resolved, userArgs, runDir) -> LaunchSpec`. It also
  has `Check(detection)`, which matches required flags against `--help` and
  inspects the environment for conflicts.
- A **LaunchSpec** holds the executable, argv, cwd, env additions, generated
  files, run dir and warnings.
- The **runner** writes the generated files (0600) into
  `$XDG_CACHE_HOME/polyroot/run/<id>/` and runs the agent as a child process
  with an argv array (no shell). It forwards signals, removes the run dir on
  exit and exits with the child's code. `polyroot command` prints the same
  spec without writing or running anything.
- **Agent customization** in config can override a built-in (`agents.claude:
  {command, args, env}`), add a named variant that `extends` a built-in, or
  describe a generic command (`command`, `args`, `addDirFlag`, `contextFlag`,
  `env`). Every flag and value becomes its own argv element, so there's no
  templating and no shell.
