# Security Policy

## Supported versions

Only the latest release receives security fixes.

## Reporting a vulnerability

Please **do not open a public issue**. Report it privately through
[GitHub private vulnerability reporting](https://github.com/Kazaz-Or/polyroot/security/advisories/new).
You should get a response within a week.

## Scope

Polyroot launches other programs with access to your repositories. Issues in
scope include:

- command or argument injection (Polyroot must only ever execute argv arrays,
  never a shell string);
- granting an agent access to paths outside the configured repositories
  (for example `/` or `$HOME`);
- writing into repositories or into an agent's global configuration;
- generated files that are readable by other users or not cleaned up.

Behavior of the coding agents themselves (their sandboxes, permission prompts
and prompt-injection resistance) is out of scope; report those upstream.
