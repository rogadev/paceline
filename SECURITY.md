# Security policy

## Reporting a vulnerability

Please report security issues privately through [GitHub's private vulnerability reporting](https://github.com/rogadev/paceline/security/advisories/new), not in a public issue. You'll get a response within a few days. Fixes are released as patch versions.

## Supported versions

Only the latest release gets security fixes.

## Scope

paceline runs on every Claude Code status line refresh and edits `~/.claude/settings.json` during `install` and `uninstall`. The issues that matter most:

- **Terminal injection.** Any way that a folder name, git branch name, or status payload field can get an escape sequence or control character into the output.
- **Code execution.** Any way that a repository, config file, or state file can make paceline run a command.
- **Settings damage.** Any way that `install` or `uninstall` can corrupt or lose the contents of `settings.json`, or write a `statusLine` command that runs something other than paceline.
- **Supply chain.** Anything that could make the published package differ from this repository.

## What the project does

- No runtime dependencies. Tests fail if one is added, or if shipped code imports `child_process`, a network module, `eval`, or `new Function`.
- All output from outside sources is stripped of C0 and C1 controls, zero-width characters, and bidi overrides, with length caps. The tests try real escape-sequence attacks against every field.
- Git data is read from files, never by running `git`, and reads are limited in size.
- Config and state files are validated by type and size. Unknown keys are ignored, and the config merge can't reach an object prototype.
- The installer refuses to overwrite unparseable settings or another tool's status line (unless `--force`), backs up first, writes atomically, and rejects install paths containing shell metacharacters.
- CI pins every GitHub Action to a commit SHA, installs with `--ignore-scripts`, runs `npm audit` and `npm audit signatures`, and publishes with npm provenance.
