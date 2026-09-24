# Lane catalog

The reviewers `/deep-review` can dispatch in paceline. Each lane owns one surface, so lanes can run in parallel without duplicating each other. The "owns" column is the boundary: when two lanes could both claim a defect, the lane that owns the consequence takes it.

All agent files live in `.claude/agents/` in this repo. They were adapted from a web-app review suite for a Go CLI with no web UI, no framework, no server, and no database, so the UX, framework, API, and data lanes from that suite do not exist here. `review-output` covers what the user sees: the status line, CLI messages, and the docs a stranger installs from.

## Families and lanes

### Checks

| Lane | Model | Owns | Runs when |
| --- | --- | --- | --- |
| `review-gate` | haiku | Running the repo's check-only commands: `gofmt -l`, `go vet`, `go test ./...`, `go test -race` where cgo is available, the 90% coverage floor, golangci-lint and govulncheck when installed, `npm test` when release tooling changed, commitlint on the reviewed commits. Reports facts, judges nothing. | Any code, config, workflow, or test change. |

### What the user sees

| Lane | Model | Owns | Runs when |
| --- | --- | --- | --- |
| `review-output` | sonnet | The rendered status line and every word a user reads: segment order and separators, glyphs and their fallback, colors and contrast on dark and light terminals, meaning never carried by color alone, `NO_COLOR`, width and truncation, wording and consistency with the README segment table, CLI help, install and uninstall messages, error messages (say what happened and what to do), and the accuracy of README, CONTRIBUTING, and SECURITY against the code. Uses the renders when available. | Changes to `internal/render`, `internal/color`, `internal/timefmt`, output strings in `cmd/`, `parity.json`, or user docs. |

### Code

| Lane | Model | Owns | Runs when |
| --- | --- | --- | --- |
| `review-logic` | sonnet (opus when triage flags time math or install state) | Correctness: logic errors, edge cases, the pace math (budget, rollover, last day, direction thresholds), time zones and DST, rounding parity with the 1.0 JavaScript version, JSON decoding of wrong or missing fields, config merging, error handling and propagation, file I/O failure modes (missing, unreadable, partial, concurrent refreshes writing the snapshot), install and uninstall state transitions (fresh, already installed, another status line, backup present, record missing), cross-platform paths. | Any non-test Go change. |
| `review-perf` | sonnet | Cost per refresh: startup work, file reads and their size caps, allocations and regexp compilation on the hot path, anything that grows with the payload, filesystem walks (git dir search), and binary size (new stdlib packages pulled into the binary). | Changes on the render path (`cmd/`, `internal/render`, `internal/gitinfo`, `internal/config`, `internal/pace`) that add loops, reads, or regexps; tier 2 and up. |
| `review-quality` | sonnet | Maintainability and idiomatic Go: readability, naming, function size, duplication (Rule of Three), dead code, error wrapping (`%w`, `errors.Is/As`), zero values and nil handling, exported API surface of `internal` packages, `//nolint` without a reason, comments that mislead. | Tier 2 and up with real code. |
| `review-architecture` | sonnet | Structure: package boundaries (`cmd` thin, logic in `internal/<concern>`), dependency direction between internal packages, side effects injected rather than reached for, the policy rules (no dependencies, forbidden imports, ASCII source), new code in the right package, a second helper duplicating an existing one, `tools/` versus shipped code. | New files or packages, moves, deletes, or a large change. |
| `review-tests` | sonnet | Test coverage and quality: changed logic has table tests, hostile input tests for anything printed, parity fixtures changed only on purpose, `t.TempDir` and injected clocks instead of real config and wall time, Windows and macOS path cases, tests not weakened to pass, the coverage floor not gamed. Reads tests; the gate runs them. | Logic changed, or test files changed. |

### Release and supply chain

| Lane | Model | Owns | Runs when |
| --- | --- | --- | --- |
| `review-release` | sonnet | Release correctness and CI: commit types versus the change (a `fix:` that adds a feature, a missing `!` on a breaking change, per the breaking rules in the profile), `release.config.js`, `.goreleaser.yaml` (targets, ldflags version stamping, archives, checksums), workflow logic (triggers, job needs, the `ci-ok` aggregate, permissions scoped per job, conditions), Dependabot config, the commit-msg hook, `package.json` tooling pins, and install instructions matching what a release actually ships. Security consequences of these stay with `review-security`. | `.github/`, `.goreleaser.yaml`, `release.config.js`, `package.json`, `package-lock.json`, `commitlint.config.js`, `.githooks/`, `go.mod`, `tools/nextbump`, or any PR into `main`. |
| `review-intent` | sonnet | Alignment with requirements: does the change do what its issue, PR description, or commit messages say; acceptance criteria met or visibly missing; unrequested scope; docs updated in the same change when behavior changed. | Tier 2 and up where an issue, PR body, or descriptive commits exist. |

### Security

| Lane | Model | Owns | Runs when |
| --- | --- | --- | --- |
| `review-security` | opus | Exploitable issues on a stranger's machine: terminal escape injection through any printed string, code execution (a repo, config, or state file that makes paceline run something), `settings.json` damage or a `statusLine` command that runs something else, path traversal and symlink tricks in the files paceline reads and writes, file permissions, resource exhaustion on the render path, and supply chain (dependencies, action pinning, workflow permissions and triggers, release integrity and attestations). | Any change that prints outside data, reads or writes a file, touches install/uninstall, `go.mod`, `package.json`, `.github/`, `.goreleaser.yaml`, or `internal/policy`. Never dropped for budget when the change touches one of these; skipped (with a stated reason) for docs, pure refactors of math with no I/O, and test-only changes. |

### After the lanes

| Agent | Model | Job |
| --- | --- | --- |
| `review-verifier` | opus | Re-reads every finding against the code. Confirms, downgrades, marks pre-existing, discards, or flags needs-human. Merges duplicates across lanes. Adds nothing new except up to three incidentals. |
| `review-advisor` | opus | Turns the verified list into recommendations: groups findings by root cause, researches fix options in this repo, the Go standard library docs, and official docs, recommends one option per problem with the trade-off, and writes each problem and solution in plain language. |

## Boundaries that are easy to blur

- **Printed strings**: `review-security` owns whether outside data can smuggle control or escape sequences into the terminal (missing `sanitize.Text`). `review-output` owns how the text looks and reads. `review-logic` owns a wrong value.
- **Colors**: `review-output` owns contrast and whether color is the only signal. `review-logic` owns a threshold compared the wrong way. `review-quality` does not re-report either.
- **Install and uninstall**: `review-logic` owns wrong state transitions and lost data on the normal paths. `review-security` owns an attacker-controlled path, a command that runs something else, and permission problems. `review-output` owns the messages.
- **The pace snapshot file**: `review-logic` owns concurrent refreshes and rollover correctness. `review-security` owns symlink and permission attacks on it. `review-perf` owns read and write cost per refresh.
- **Parity fixtures**: `review-tests` owns whether a changed `parity.json` was changed on purpose and still proves something. `review-output` owns whether the new output is better.
- **CI and release**: `review-release` owns whether it works and ships the right version. `review-security` owns permissions, pinning, untrusted triggers, and anything that lets a PR or a dependency tamper with a release.
- **Docs**: `review-output` owns user docs' accuracy and clarity. `review-release` owns CONTRIBUTING's release and branch instructions. `review-intent` reports docs missing from a behavior change once.
- **Placement and duplication**: `review-architecture` owns a second helper doing what an existing one does. `review-quality` owns copy-pasted code within the change.
- **Tests for security controls**: `review-tests` owns them; a missing test on a sanitize or install guard is a Blocker there.
- **Error context**: `review-quality` owns a swallowed or unwrapped error. `review-logic` owns the failure-mode consequence (a crash or a wrong line on refresh).
- **Commit types**: `review-release` owns whether a commit type matches its effect on the version. `review-intent` owns whether the change matches its stated goal.
