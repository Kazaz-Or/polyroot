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
internal/configedit  comment-preserving edits of config.yaml (setup, workspace add/remove)
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

## Releases (maintainers)

Releases are cut from GitHub, never by pushing tags by hand:
**Actions → Release → Run workflow** on the default branch. Only the
repository owner can run it; the job is skipped for anyone else.

- `bump`: `patch`, `minor` or `major`, applied to the latest stable `v*` tag.
- `version`: an exact version instead, e.g. `v0.1.0` or `v0.2.0-rc.1`.
  Pre-release versions are marked as pre-releases.
- `draft`: leave the release unpublished so you can edit the notes first.

The workflow runs the tests, pushes the tag, builds macOS and Linux binaries
with GoReleaser, and publishes the release with `checksums.txt`. Update
`CHANGELOG.md` before running it. If a run fails after the tag was pushed,
delete the tag (`git push origin :refs/tags/vX.Y.Z`) and any draft release,
then run it again.

Please follow the [Code of Conduct](CODE_OF_CONDUCT.md).
