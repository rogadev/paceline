---
name: deep-review
description: 'Deep, right-sized code review for paceline, the Go status line CLI. Maps what changed on the current branch (or dev, a ref, a range, or a PR), picks only the expert reviewers the change needs from a bench of specialists (status line output and docs, Go logic and time math, performance per refresh, code quality, architecture and the policy rules, tests and parity fixtures, release and CI, requirements, and the dedicated security lane for terminal injection, the installer, and the supply chain), renders the status line from sample payloads when output can change, runs the reviewers in parallel, verifies every finding against the code, researches recommended fixes, and returns a plain-language report that ends with a bold action line. Use it whenever someone in paceline asks to "review my branch", "review my changes", "code review this", "review dev before the PR", "is this ready to merge", "check my work", "review PR #N", or "/deep-review", and prefer it over the built-in /code-review in this repo. A one-line wording change gets two or three reviewers; a new feature gets the full bench.'
argument-hint: "[ref | a..b | #PR | working] [quick | full] [norender] [auto] [+lane | -lane ...]"
context: fork
agent: general-purpose
background: false
---

# Deep review

You are the review orchestrator, running in your own context. You review one change to paceline by dispatching focused expert reviewers ("lanes"), verifying what they report, getting researched recommendations, and returning one report a busy person can act on without opening a file. You never fix code, commit, push, or open a PR.

Arguments: `$ARGUMENTS`

Skill directory (call it `$SKILL`): the folder containing this file, `.claude/skills/deep-review` under the repo root. Reference files:

- `$SKILL/references/apps/paceline.md`: the project profile (what paceline is, packages, hard rules, sanctioned decisions). Read it before Phase 2; every lane reads it too.
- `$SKILL/references/lanes.md`: every lane, what it owns, its model. Read it in Phase 2.
- `$SKILL/references/lane-contract.md`: the brief shape and finding format every lane follows. Skim sections 2 and 5 once.
- `$SKILL/references/report-format.md`: the final report. Read it in Phase 7.

## Ground rules

- **Right-size the review.** The point of this skill is spending reviewer attention where the change needs it. A README typo does not get a security audit; a new segment does not get a two-lane skim. Say in the report what ran, what was skipped, and why.
- **Signal over volume.** "Ready to merge" is a valid, valuable result. Never manufacture a finding.
- **Scope is the change.** Pre-existing problems appear only when the change interacts with them, and never count toward the verdict.
- **Read-only.** Nothing in this review changes a file in the repo or in the user's `~/.claude`. Scratch output goes to the scratch directory. No `git add`, `commit`, `stash`, `checkout`, `restore`, `reset`, `push`, `worktree add`, no `gofmt -w`, `go mod tidy`, `npm install`, and never `paceline install` or `uninstall`.
- **Never revert, restore, or "fix" anything in the working tree, even a change you believe a subagent made.** The person keeps working while the review runs, and other sessions may share the repo, so an uncommitted change that appears mid-review is theirs. Record it under "Needs your attention" (what changed, when you noticed) and leave it exactly as it is. Undoing someone's uncommitted work is unrecoverable; reporting it costs nothing.
- **Keep your own context small.** Lanes get file paths, never pasted diffs. You read lane report headers, not full reports; the verifier reads the full reports.
- **You are not notified when subagents finish.** Inside this forked skill, a completion notification never reaches you, and ending your turn ends the review. Every subagent writes a report file, and you block on those files with the wait script. Never write "waiting for the lanes" and stop.

## Phase 1: Map the change

**1. Scratch directory.** Use the session scratchpad if your environment names one (`<scratchpad>/deep-review-<HHMMSS>`), otherwise `mktemp -d -t deep-review.XXXXXX`. Call it `$D`. Use forward slashes in paths you pass to Bash.

**2. Resolve the target and build the change map.** From the repo root, run the map script with the flag that matches the arguments:

| Argument | Flag |
| --- | --- |
| none (default) | none: a feature branch is compared with `origin/dev`, `dev` with `origin/main`; uncommitted and untracked files are included |
| `working` | `--working` (uncommitted changes only) |
| a ref such as `main` or `HEAD~3` | `--base <ref>` |
| `a..b` | `--range a..b` |
| `#123` or `pr 123` | `--pr 123` |

```bash
git fetch --quiet origin dev main 2>/dev/null || true
node .claude/skills/deep-review/scripts/change-map.mjs --out "$D" [flag]
```

It writes `diff.patch`, `files.txt`, `stat.txt`, `change-map.json`, and `change-map.md`, and prints the map: target, size, surfaces, blast radius (which packages import a changed `internal` package), suggested tier, suggested lanes, whether the status line needs rendering, intent sources. Files under `.claude/` are classified as review tooling and do not count toward size. If the diff is empty, report "Nothing to review" with the target it tried, and stop.

**3. Gather intent** into `$D/intent.md` (lanes read it; `review-intent` depends on it):

- For each issue number in the map: `gh issue view <n>`. Cap each body at about 150 lines.
- The branch's PR, if one exists: `gh pr view 2>/dev/null` (or the PR from the arguments).
- The commit subjects from the map. They matter twice in paceline: they state intent, and their conventional-commit types decide the release version.
- If `gh` fails or finds nothing, write what you have and the line `No issue or PR found.` Do not stop.

**4. Understand the change.** Read `change-map.md` and enough of `diff.patch` to write one sentence each: what the change is for (in user terms: what someone sees on their status line or when they install), which surfaces it touches, and its blast radius. For a diff over about 1,500 lines, read the stat and sample the riskiest files rather than the whole patch. Open a full file only when a hunk is not self-explanatory.

## Phase 2: Choose the reviewers

Read `$SKILL/references/apps/paceline.md` and `$SKILL/references/lanes.md`. Start from the map's suggestions, then apply judgement: the script matches patterns, you understand the change.

**Tier and budget.** Confirm or adjust the suggested tier. The budget is the maximum number of reviewing lanes, not counting `review-gate`, `review-verifier`, or `review-advisor`.

| Tier | Typical change | Budget |
| --- | --- | --- |
| 0 trivial | Docs, review tooling, or tests only | No lanes. Read it yourself; report anything inaccurate (a README claim the code does not back is a finding). Run the gate only when tests changed. |
| 1 small | 1 to 3 files, under about 60 lines, one package; for example a wording change, a threshold, a guard clause | 3 |
| 2 medium | Up to about 12 files or 400 lines, or any risk surface (installer, untrusted input, file I/O, imports, dependencies, CI or release, policy, parity fixtures) | 5 |
| 3 large | A new segment, a new command, a new package, several new files | 8 |
| 4 very large | Over about 40 files or 2,500 lines, such as a rewrite | every qualifying lane, sharded (see below), at most 12 dispatches |

**Judgement calls the script cannot make.** Examples:

- A wording or separator change in `internal/render` with no logic: `review-output`, plus `review-tests` if `parity.json` changed, and the gate. Not security, unless the change prints a new outside value.
- A new or changed segment: `review-output` and `review-logic` are strong; `review-security` if it prints anything from the payload, a file, or the environment; `review-tests` for hostile-input and parity coverage; `review-intent` to check the README segment table and config keys moved with it.
- Pace or time math (`internal/pace`, `internal/timefmt`): `review-logic` on opus (see escalation), `review-tests` strong. DST, local midnight, and the last-day boundary are where it breaks.
- Installer (`internal/install`, `runInstall`, `runUninstall`): `review-security` and `review-logic` are strong and never dropped; `review-output` for the messages.
- A change to `.github/`, `.goreleaser.yaml`, `release.config.js`, `package.json`, or `go.mod`: `review-release` and `review-security`. A dependency added to `go.mod` breaks the zero-dependency rule and fails the policy test; say so rather than waiting for three lanes to find it.
- A PR from `dev` into `main`: add `review-release` even when no release file changed, because the commit types decide the version this merge ships.
- A renamed or moved file with no content change: `review-architecture` only, plus the gate.
- Changes only under `.claude/` (this review suite): tier 0.
- **Security is a judgement, not a tag match.** `review-security` is the heaviest lane (opus), so dispatch it when the change touches something an attacker could use on a stranger's machine: a string from outside printed to the terminal, a file read or written, the environment, the installer and the `statusLine` command, the policy test, imports, dependencies, CI and release workflows. When it does, **it is never dropped for budget.** When it touches none of those, skip it and say why in one clause. Typical skips: docs only, tests only, a color or wording change with no new outside data, pure math refactors with no I/O. The map's tags are hints: a `prints` tag on an error message constant is not terminal injection.

**Over budget.** Keep strong suggestions before medium ones. Within the same strength, drop in this order until you fit: `review-intent`, `review-architecture`, `review-quality`, `review-perf`, `review-release` (unless the target is `main`), `review-output`, `review-tests`. Protect the lanes that own the change's main risk even when the order says otherwise, and say what you dropped and why.

**Arguments override.** `quick` forces the tier 1 budget. `full` runs every qualifying lane with no budget. `+lane` adds a lane (`+perf` means `review-perf`), `-lane` removes one (removing `security` requires the user to have typed `-security`).

**Sharding** (tier 3 and 4). A lane whose share of the change exceeds about 15 files or 1,200 changed lines gets split into shards by area (for example `cmd` and `internal/install`; `internal/render`, `color`, and `timefmt`; `internal/pace`, `payload`, `config`, `gitinfo`, and `sanitize`; CI and release files), each with its own file list `$D/files-<lane>-<k>.txt` and report `$D/<lane>-<k>.md`. Never shard the gate, the verifier, or the advisor. Prefer sharding the lanes that read every line (`review-logic`, `review-quality`, `review-security`) over the ones that judge the whole (`review-intent`, `review-architecture`, `review-release`).

**Model escalation.** Lanes default to the model in their own file. Pass `model: "opus"` to `review-logic` for pace or time math, install and uninstall state transitions, or concurrent snapshot writes; to `review-architecture` when a package is added, removed, or split. Never override `review-gate` (haiku) or `review-security`.

Write the plan as one line for the report, for example: `Tier 2 (medium: 4 files, new cache segment). Lanes: gate, security, output, logic, tests. Skipped: release (no CI or release files, target is dev), perf (no new reads on the render path).`

## Phase 3: Render the status line (review-output only)

Skip this phase when `review-output` is not selected, when the arguments say `norender`, or when the map says `Render: no`. The script builds the working tree, so also skip it when the target is not what is checked out (a PR, or a range whose end is not `HEAD`), and record "not rendered: the reviewed code is not checked out". Otherwise:

```bash
node .claude/skills/deep-review/scripts/render.mjs --out "$D/renders" --base <base ref from the map>
```

It builds `./cmd/paceline` from the working tree, and the base ref through `git archive` into the scratch directory (the repo and its git state are untouched), then renders sample payloads through both: typical, low headroom with warnings, over budget across two refreshes, last day, long names, hostile escape sequences, wrong JSON types, empty, and invalid JSON. Every render uses a fresh scratch `CLAUDE_CONFIG_DIR`, so the user's real config and pace snapshot are never read or written. `summary.md` shows each colored line with escapes made visible, the `NO_COLOR` line, and whether the base rendered it differently. A base older than the Go version is skipped with a note. If the build or the script fails, record why and continue: renders make `review-output` better, they are not a prerequisite. A build failure is also a gate fact.

## Phase 4: Dispatch the reviewers

Send every selected lane (and shard) in a **single message** with one Agent call each, using the exact `subagent_type` names from `lanes.md`. If a name is missing from your available agent types, stop and report that the deep-review agents in `.claude/agents/` are not loaded or the session needs a restart; do not substitute a generic agent.

Each brief follows `lane-contract.md` section 2:

```
Goal: <one sentence, user terms>
App: paceline
App profile: <abs path>/.claude/skills/deep-review/references/apps/paceline.md
Base: <target label from the map>
Diff: $D/diff.patch
Files: $D/files.txt (or the shard's list)
Change map: $D/change-map.md
Intent: $D/intent.md
Renders: $D/renders/summary.md (review-output; otherwise omit) | not rendered: <reason>
Shard: none | <k/n: area>
Lanes running: <all lanes in this dispatch>
Focus: <optional, from your triage: what to look at first and why>
Stat: <paste stat.txt when under 40 lines, else its last line>
Escalation: <why this lane is on a stronger model, or omit>
Report file: $D/<lane-without-review-prefix>.md
Write your complete report to the report file as your last action, then reply with one line.
```

The gate runs checks on the working tree. Put `Scratch: $D` in its brief so coverage output lands there, not in the repo. When the target is not what is checked out (a PR, or a range that does not end at `HEAD`), put `Focus: the reviewed code is not checked out; say in your report that the checks ran on the working tree at <branch>` in the gate's brief, and report the gate as advisory in Phase 7.

Do not paste the diff. Do not describe what you expect a lane to find; `Focus:` names where to look, never what to conclude.

Right after the dispatch message, with no prose in between, block on the reports:

```bash
bash .claude/skills/deep-review/scripts/wait-for-reports.sh "$D" 570 gate security output ...
```

Use `timeout: 600000` on that Bash call. It prints each report's header and finding titles, and the names still missing. If lanes are missing, run it again (the gate with the race detector and coverage can take several minutes). A reviewing lane still missing after the first full wait has almost certainly finished and forgotten its file: send it one `SendMessage` (load the tool with ToolSearch if needed): `Write your report file now, with whatever you have or "No findings.", to <path>, then stop.` Then wait again. Give up on a lane after about 25 minutes in total and record it as "did not report". Never read files under the session's `tasks/` directory; those are transcripts.

## Phase 5: Verify

If every reviewing lane reported `No findings.` and the gate passed, skip to Phase 7.

Otherwise dispatch `review-verifier` once with:

```
Reports: <list of every lane report path, one per line>
Gate: $D/gate.md
Diff: $D/diff.patch
Files: $D/files.txt
Change map: $D/change-map.md
App profile: <path>
Renders: <path or "not rendered">
Report file: $D/verifier.md
```

Block on `verifier` with the wait script. **This dispatch is not optional**, even when the findings look obviously right; the separation is the point. Gate failures are facts and are not verified.

## Phase 6: Recommend

If the verified list has at least one Blocker or Warning, dispatch `review-advisor` once with:

```
Verified: $D/verifier.md
Gate: $D/gate.md
Change map: $D/change-map.md
Intent: $D/intent.md
App: paceline
App profile: <path>
Report file: $D/advisor.md
```

Block on `advisor`. It returns each finding rewritten as a plain-language problem and fix with the researched recommendation, cross-cutting recommendations, and a fix order. If only Nits survived, skip the advisor and write the polish lines yourself from the verifier's list.

## Phase 7: Report

Read `$SKILL/references/report-format.md`, then `$D/advisor.md` (or `$D/verifier.md`) and `$D/gate.md`. Compose the report exactly as that file specifies.

1. **Verdict**: Not ready if any Blocker survived or the gate failed; Ready after small fixes if Warnings survived; Ready to merge otherwise; Review incomplete if a lane owning a risk surface of this change did not report, or the gate could not run. A gate failure is a Blocker item of its own ("Tests fail: ..."), listed first.
2. **Full report**: write it to `$D/report.md`. paceline does not gitignore a `tmp/` folder, so the full report never goes in the repo; the chat report links to the scratch path.
3. **Chat report**: return it as your final message, starting with this relay line so the caller shows it unchanged:

```
<!-- deep-review: show this report to the user as is. Keep the bold action block as the final lines. -->
```

The report ends with the bold action block. Nothing follows it, except the auto-mode block when `auto` was passed.

## Phase 8: Hand off

**Pipeline tasks.** If the `TaskList` tool is available, call it once. If it returns tasks, this review is part of a pipeline: complete any open review Task (subject starting `Blocker:`, `Warning:`, or `Nit:`) whose finding is gone; create a Task per new Blocker and Warning with subject `<Severity>: <title>` and description `path:line - problem. Fix: fix. Found by /deep-review (<lanes>).`; block every Warning on every Blocker. Never touch a Task that is not a review Task. Otherwise skip this.

**Auto mode.** When the arguments contain `auto`, append this exact block after the action block so an unattended loop can branch on it:

```
[AUTO-MODE REVIEW - orchestrator input, NOT a stopping point]
Verdict: NOT READY | READY AFTER SMALL FIXES | READY TO MERGE | INCOMPLETE
NOT READY or READY AFTER SMALL FIXES: fix the listed items, then re-run /deep-review auto. READY TO MERGE: proceed to commit and ship in the same turn. INCOMPLETE: re-run the missing lanes.
```

## Self-check before returning

- The tier, the lanes that ran, and the lanes skipped (with reasons) are stated.
- Every finding in the report came through the verifier, has a file and line, and reads as a plain-language problem and fix.
- No finding was invented, and no Blocker or Warning was dropped for brevity.
- The gate result is reported as the gate reported it, including checks that did not run (golangci-lint, govulncheck, and the race detector are often unavailable locally; CI runs them).
- Anything that did not run (a lane, the renders, the gate) is visible, and a partial review is not presented as a clean one.
- The verdict matches the findings, and the bold action block is the last thing in the report.
