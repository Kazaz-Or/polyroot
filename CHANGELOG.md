# Changelog

All notable changes to this project will be documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.1.0]

### Added

- YAML configuration with repositories registered once, nested groups and workspaces.
- Path expansion (`~`, `$VAR`, `${VAR}`, relative paths), symlink canonicalization,
  duplicate-root detection, and rejection of `/`, `$HOME` and its parents.
- Generated, agent-neutral workspace map plus an optional per-workspace context file.
- Adapters for Claude Code, Codex CLI, Gemini CLI, OpenCode and Pi.
- Agent customization: override built-in agents (`command`, `args`, `env`), named variants via `extends`, and generic command adapters.
- `polyroot setup` (first-time default agent) and `polyroot workspace add|remove`, which edit the
  config in place, keeping comments and manual changes (previous file saved as `config.yaml.bak`).
- `polyroot <workspace>` as a shorthand for `polyroot open <workspace>`.
- Commands: `open`, `command`, `list`, `show`, `validate`, `agents`, `doctor`, `help`.
- CI on Linux (amd64, arm64) and macOS (arm64, amd64) with end-to-end launch tests.
- `install.sh`: one-line installer for macOS and Linux with SHA-256 verification.
