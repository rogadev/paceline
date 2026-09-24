# Lane contract

Every `/deep-review` reviewer agent (a "lane") follows this contract. It is what lets the orchestrator run many lanes in parallel, verify their findings, and turn them into one plain-language report. Lane authors embed sections 1 to 6 in the agent file; section 7 is guidance for writing a lane.

## 1. Read-only, one output file

- The lane may write exactly one file: the `Report file:` path in its brief. It never edits, creates, formats, or deletes anything in the repo.
- Bash, when a lane has it, is for reading only: `git diff`, `git log`, `git show`, `git blame`, `git grep`, `git ls-files`, `ls`, `cat`, `head`, `wc`. Nothing that changes git state, installs packages, or calls the network. (The gate lane is the one exception: it runs the repo's check-only scripts.)
- **The report file is the delivery.** The orchestrator never reads the chat reply. Write the complete report to the exact path with the Write tool, including when the result is `No findings.`, then reply with the single line `done <path>`. A lane that finishes without writing the file has done no work.
- Never print a secret value. Cite file and line and name the kind of credential.

## 2. The brief

The orchestrator sends a short brief. Lanes read the files it points to rather than expecting pasted content.

```
Goal: <one sentence: what the change is for>
App: paceline
App profile: <absolute path to references/apps/paceline.md>
Base: <what the diff is against>
Diff: <scratch>/diff.patch
Files: <scratch>/files.txt            (the lane's own file list when sharded)
Change map: <scratch>/change-map.md   (surfaces, size, blast radius, candidate URLs)
Intent: <scratch>/intent.md           (issue, spec, and commit context; may say "none found")
Renders: <scratch>/renders/summary.md (review-output only: rendered status lines; may say "not rendered")
Shard: <k/n and the area, or "none">
Lanes running: <list, so the lane stays out of the others' surface>
Focus: <optional: what triage saw that this lane should look at first>
Report file: <scratch>/<lane>.md
```

Read the project profile before the diff. It carries what a generic reviewer does not know: what paceline is, its packages, its hard rules (zero dependencies, ASCII source, injected side effects, output parity), sanctioned exceptions, and how to run it safely.

## 3. Scope and discipline

- **Review the change, not the codebase.** A finding is on a line the diff adds or changes, or on existing code the diff newly reaches (a new caller of a weak helper, a component newly shown on a page). Pre-existing problems go in the `Pre-existing` section only when the change interacts with them, and never count toward the verdict.
- **Stay in your lane.** The brief lists the lanes running. Leave their surface alone; the verifier merges genuine overlaps. When a lane that owns a surface is not running, its surface is yours only where it meets your own.
- **The repo's documented decisions beat your instincts.** `README.md`, `CONTRIBUTING.md`, `SECURITY.md`, and the profile's sanctioned decisions record intended behaviour. Do not re-litigate a documented decision on a diff that does not change it. If the profile contradicts the code in front of you, trust the code and note it under `Profile drift`.
- **Signal over volume.** No finding is a valid, valuable result. Never invent one to fill the report. Three real findings beat three real findings hidden among nine weak ones.
- **Evidence or it did not happen.** Every finding quotes the line or lines it is about (at most three), copied from the file you read. If you cannot quote it, you have not verified it.

## 4. Severity

Lanes rate each finding with one of three levels. The final report translates them for the reader.

| Level | Report label | Meaning | Test |
| --- | --- | --- | --- |
| **B** Blocker | Must fix before merge | A user or the business is hurt if this ships: data loss or corruption, a crash or broken core path, an authorization hole, a status line that prints garbage, a Go error, or nothing, a violated hard rule the repo enforces, a failing gate. | Would you page someone if this reached production? |
| **W** Warning | Should fix | A real defect with a bounded impact: a broken edge case, a missing error or empty state on a real path, a confusing flow, a maintainability trap that will cause the next bug. | Would a careful senior reviewer hold the PR for it? |
| **N** Nit | Polish | Worth doing, harmless to skip: naming, small inconsistencies, minor copy or spacing. | Would you mention it in passing and approve anyway? |

When unsure between two levels, pick the lower one. Inflated severity costs the reader's trust in every other finding.

## 5. Finding format

Write findings for two readers: the verifier, who re-reads the code, and the person who will get a plain-language summary. So state the consequence in plain words, not only the mechanism.

```
## <Lane name> findings
App: <app> | Base: <base> | Shard: <k/n or none>
Checked: <one line: what you examined, for example "4 components, 2 pages, 3 renders at 390/768/1440">
N findings (B x, W y, N z) | No findings.

### [B|W|N] <short title that names the problem, not the fix>
File: `path:line`
Evidence:
    <the quoted line or lines, at most three>
Impact: <who is affected and what they experience, in plain words: "a Windows user whose folder name contains an emoji sees a blank status line">
Problem: <the mechanism, one or two sentences>
Fix: <concrete, in this repo's idiom, naming the existing helper, token, or component to use>
Confidence: high | medium

### Pre-existing (only if the change interacts with it)
- `path:line` - one line, and how the change interacts

### Profile drift (only if the app profile no longer matches the code)
- one line each
```

- Order findings by severity, highest first.
- Nits may be one-liners under a `### Nits` heading: `` - `path:line` - problem; fix ``.
- Each lane states its own cap (usually 6 to 8 findings, at most 3 nits). If you have more, keep the strongest and say how many you dropped.
- `Confidence: medium` means you believe it but a fact outside the code (runtime data, a live upstream, the rendered page) could change the answer. Low-confidence hunches are not findings.

## 6. Orient, then review

1. Read the project profile, then the change map and intent file.
2. Read the diff. Classify which hunks are in your surface.
3. Read the full file for every hunk you review. Defects hide in the parts a hunk does not show.
4. Read the neighbours: how the surrounding, already-accepted code does the same thing is the strongest signal of the repo's convention.
5. Check your finding against the repo's rules and the profile's sanctioned exceptions before you write it.
6. Write the report file. Reply `done <path>`.

## 7. Writing a lane (for lane authors)

- Frontmatter: `name`, a `description` that starts "deep-review lane." and names the surface, `model`, `tools`. Reviewing lanes get `Read, Grep, Glob, Write`, plus `Bash` only when they need read-only git. Model per lane is set in `references/lanes.md`.
- Body order: role and failure modes (one paragraph), the contract (sections 1 to 6 condensed, keep every rule), what to look for (the checklist, grouped), a paceline section with concrete file paths, helpers, and rules, what not to report, severity examples for this lane, the cap, the output format.
- Be concrete. "Use the repo's tokens" is weak; "a folder name printed without `sanitize.Text` lets a repo named with an escape sequence rewrite the terminal title; wrap it as `render.go` does for the project segment" is strong.
- Keep an agent file under about 350 lines. Knowledge that every lane needs belongs in the project profile, not in each agent.
- Name the sanctioned exceptions so the lane does not flag them. A lane that flags a documented decision teaches the reader to ignore the report.
