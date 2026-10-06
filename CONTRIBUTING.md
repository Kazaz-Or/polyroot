# Contributing

Thanks for helping. Polyroot is deliberately small: it maps a logical
multi-repo workspace onto coding agents' native features. Before opening a PR
for a new feature, check the non-goals in the README.

## Setup

Requires Go 1.24 or newer.

```bash
go test ./...                 # unit + end-to-end tests (fake agents); real agents not required
go test -short ./...          # also skip the optional installed-agent checks
go vet ./...
gofmt -l .                    # must print nothing
golangci-lint run             # what CI runs
go build ./cmd/polyroot
```

## Layout

```text
cmd/polyroot         main
internal/config      YAML schema, path expansion, structural validation
internal/workspace   resolution (repos, groups, order, dedup) and the workspace map
internal/agent       Adapter interface, detection, runner, one file per agent
internal/cli         cobra commands
docs/agents          one page per agent: mechanism, versions, limitations
```

The resolver (`internal/workspace`) must never learn about a specific agent.

## Adding an agent

1. **Research first.** Install the agent, save its `--help` output to
   `internal/agent/testdata/<name>-help.txt`, and read its current docs and
   source. Don't rely on blog posts or memory.
2. Pick the most native mechanism, in this order: a multi-root flag, then
   external-reference config, then a documented extension, then a safe
   Polyroot strategy, then an explicit unsupported error. Never use symlinks
   or copies, and never edit the agent's global config.
3. Implement `agent.Adapter` in `internal/agent/<name>.go`:
   - `Info()`: binary, capability labels, and the `RequiredFlags` that must
     appear in `--help`;
   - `Build()`: return a `LaunchSpec` (argv, cwd, env, generated files). Fail
     rather than drop a repository. Watch for variadic flags that could
     swallow forwarded arguments;
   - `Check()`: environment conflicts worth reporting in `polyroot doctor`.
4. Add it to `Builtins()` and add unit tests for the exact argv/env.
5. Write `docs/agents/<name>.md` and update the matrices in
   `docs/agent-capabilities.md` and the README.

## Pull requests

- Keep changes focused, and add tests for behavior changes.
- Update `CHANGELOG.md` under "Unreleased".
- By contributing you agree your work is licensed under the MIT license.

Please follow the [Code of Conduct](CODE_OF_CONDUCT.md).
