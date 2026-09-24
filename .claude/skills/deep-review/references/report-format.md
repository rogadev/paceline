# Report format

The report is for a busy person who has not read the code. They see the **last paragraph first** in chat, then scroll up for detail. So the report ends with the verdict and the action, and every finding reads as a problem and a solution in plain language before any file path appears.

`/deep-review` produces two versions from the same content:

- **Chat report**: returned to the caller and shown to the user. Short, scannable, ends with the action block.
- **Full report**: saved to `report.md` in the review's scratch directory (paceline has no gitignored `tmp/`, so it never goes in the repo). It adds the evidence quotes, alternatives considered, lane coverage, and discarded findings. The chat report links to it.

## Writing rules

- **Problem, then solution, in plain words.** Lead each finding with what goes wrong and for whom ("Your budget resets at 5pm instead of midnight"), not with the mechanism ("StartOfDay uses UTC"). The mechanism goes in one short clause after.
- **No jargon without a gloss.** Say "a repo can change your terminal's title by naming a branch with an escape sequence (terminal injection)", not "unsanitized OSC sequence". Name files and functions in backticks, but never make the reader open a file to understand the problem.
- **One recommended fix per problem.** State it as an instruction. Mention an alternative only when the trade-off is real, in one line.
- **Numbers over adjectives.** "Contrast is 3.1:1 on a light terminal; it needs 4.5:1", "reads the config file twice per refresh, about 40 extra reads a minute".
- **Short.** A finding in the chat report is at most five lines. Nits are one line each.
- **Only actionable items in the fix lists.** A finding that a later commit on the same branch already fixed (common when reviewing an old commit or range) is not a "Must fix" or "Should fix" item and does not count toward the verdict. Mention it in one line under "Already fixed later on this branch" in the full report only.
- **Polish stays short in chat.** List at most five polish items, the most useful first, then "The full report lists <n> more." The rest go in the full report.
- **Every skipped reviewer has a reason.** "Not run" alone is not a reason; say "no logging or config changed" or "dropped to stay within the tier budget".
- **Honest scope.** Say what was not checked (lanes skipped, the status line not rendered, a lane that timed out, gate checks that could not run locally). A clean report on a partial review must say it was partial.
- Google developer documentation voice: second person, present tense, active voice, sentence-case headings, serial comma.

## Verdicts

| Verdict | When | Action block |
| --- | --- | --- |
| **Ready to merge** | No Blockers, no Warnings, gate passed | "No action needed." plus any optional polish count |
| **Ready after small fixes** | No Blockers; Warnings exist; gate passed | "Fix the N items below before merging." |
| **Not ready** | Any Blocker, or the gate failed | "Don't merge yet." plus the must-fix list |
| **Review incomplete** | A lane that owns a risk surface in this change failed to report, or the gate could not run | "Review incomplete." plus what to re-run |

## Chat report template

Use this shape exactly. Omit a section when it is empty, except the action block, which always ends the report.

```markdown
## Code review: <branch> vs <base>

<One sentence: what the change does, in user terms.> <Size word: small | medium | large> change, <n> files. <n> reviewers ran: <short list>. <One clause on what was skipped and why, if anything.>

**Checks:** <gofmt, vet, tests, coverage: all passed | <what failed>; <which checks did not run locally>> | **Status line rendered:** <n sample payloads, <n> differ from <base> | not rendered (<reason>)>

### Must fix before merge

**1. <Problem in plain words, stated as what goes wrong>**
<One or two sentences: who is affected and what they experience.>
**Fix:** <the recommended fix, as an instruction>. <`path:line`>

### Should fix

**2. <Problem>**
<Impact sentence.>
**Fix:** <instruction>. <`path:line`>

### Polish (optional)

- <problem and fix in one line> (`path:line`)

### Recommendations

<Only when the advisor found a pattern across findings or a better approach worth a follow-up. One to three bullets, each: the pattern, the recommended change, the payoff. Say whether it belongs in this change or a follow-up issue.>

<Full report: `<scratch>/report.md`>

---

**<Action block, see below>**
```

## Action block

The last lines of the report. Bold. Nothing may follow it except the auto-mode block when `auto` was passed.

- Not ready:
  > **Don't merge yet: <n> problem(s) must be fixed first.** <Item 1 in five words>, <item 2 in five words>. <One sentence on the fastest path, for example "Both are one-line fixes; start with #1.">
- Ready after small fixes:
  > **Almost ready: fix <n> item(s) before merging.** <Short list.> <Optional: "The <n> polish items can wait.">
- Ready to merge:
  > **Ready to merge. No action needed.** <Optional: "<n> optional polish items are listed above.">
- Review incomplete:
  > **Review incomplete: <what did not run>.** <What to do, for example "Re-run `/deep-review`, or run `/deep-review -perf` to skip the lane that timed out.">

## Full report additions

The full report uses the same order, and adds under each finding:

```markdown
**What we found:** <the mechanism, two or three sentences>
**Evidence:** `path:line`
    <quoted code, at most three lines>
**Why this fix:** <one or two sentences; the alternatives considered and why they lost>
**Effort:** small (under 30 minutes) | medium (a few hours) | large (a day or more)
**Found by:** <lanes> | Verified: confirmed | downgraded from <X> | needs a human
```

Then these sections at the end:

- **Coverage**: the tier and why, each lane that ran and its result line, each lane skipped and why, shards, renders, and gate checks that did not run locally.
- **Needs a human**: findings the verifier could not settle from code, one line each with the question to answer.
- **Pre-existing issues this change touches**: one line each; never counted in the verdict.
- **Discarded**: findings the verifier dropped, one line each with the reason, so the user can see nothing was hidden.
- **Profile drift**: lines where the project profile no longer matched the code, so the suite can be corrected.

## Example (chat report)

```markdown
## Code review: feat/cost-segment vs origin/dev

Adds a "cost" segment that shows the session's spend in dollars. Medium change, 6 files. 5 reviewers ran: output, logic, tests, security, and intent. Security ran because the segment prints a value from the payload.

**Checks:** gofmt, vet, tests, and coverage (91.4%) all passed; golangci-lint and govulncheck are not installed locally, so CI runs them | **Status line rendered:** 9 sample payloads, 3 differ from origin/dev

### Must fix before merge

**1. A payload can print escape sequences through the new segment**
The currency label comes straight from the payload and is printed without cleaning, so any tool that writes the payload can clear the screen or rename the terminal tab.
**Fix:** pass it through `sanitize.Text(label, 8)` like every other payload string. `internal/render/render.go:212`

### Should fix

**2. The segment shows "$0.00" before any spend**
A new session starts with a zero-cost segment that takes space and says nothing.
**Fix:** hide the segment below one cent, as the context segment hides below its threshold. `internal/render/render.go:208`

### Polish (optional)

- The README segment table doesn't list the new `cost` key for `segments` (`README.md:58`)

### Recommendations

- Three segments now each decide when to hide. A small `visible(value, threshold)` helper would keep the rule in one place. Follow-up, not this change.

Full report: `C:/Users/you/AppData/Local/Temp/.../deep-review-142233/report.md`

---

**Don't merge yet: 1 problem must be fixed first.** The cost label reaches the terminal unsanitized. It is a one-line fix; the zero-cost rule can go in the same commit.
```
