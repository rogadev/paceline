# paceline

A Claude Code status line that paces your usage limits and shows, at a glance, where every session stands.

```
Opus 5.5 · techcentral (dev) · s 16% · w 4% · t 46% of 28% · 16m
```

Every number is how much you've used, so higher always means closer to a limit: `s` is your five-hour session, `w` your week, and `t` today. Prefer words? The verbose style reads `session 16% · week 4% · today 46% of 28% budget`.

Your weekly limit resets on a fixed schedule, but the status line only tells you how much is left, not whether that's a lot or a little for the days remaining. paceline divides what's left by the days until the reset and gives you **today's budget**, then shows how much of it you've used as you work. A light week gives you a big budget; a heavy one, a small budget. It's most useful on weekends, when you're deciding whether to go hard on side projects or save the rest for Monday.

The rest of the line tells your sessions apart and shows where each one stands: the model and effort, the project in its own color with its git branch, your session and weekly limits, and warnings when the context window fills up or the prompt cache goes cold.

## Install

paceline is a single binary with no dependencies, for macOS, Linux, and Windows on Intel and ARM.

**Download a release.** Get the archive for your platform from the [latest release](https://github.com/rogadev/paceline/releases/latest), extract `paceline` (`paceline.exe` on Windows), and put it somewhere permanent, such as `~/.local/bin`. Then run:

```sh
paceline install
```

**Or build it with Go** (1.26 or later):

```sh
go install github.com/rogadev/paceline/cmd/paceline@latest
paceline install
```

`paceline install` points Claude Code's `statusLine` in `~/.claude/settings.json` at the binary you ran. It backs up the file first, keeps your other settings and their order, and won't replace a status line you already have unless you pass `--force`. It also asks which label style you want, regular (`s 16%`) or verbose (`session 16%`); pass `--regular` or `--verbose` to skip the question. Change the style any time:

```sh
paceline style verbose     # or: paceline style regular
```

To go back:

```sh
paceline uninstall
```

That removes paceline and restores whatever status line it replaced. If you move the binary, run `paceline install` again from the new location.

### Update

Claude Code runs whatever binary is at the installed path, so updating is replacing that file. There's nothing to uninstall or reinstall, and your `paceline.json` config and today's budget carry over.

- **Built with Go:** run `go install github.com/rogadev/paceline/cmd/paceline@latest` again, and `go install github.com/rogadev/paceline/cmd/paceline-mcp@latest` if you use the progress tools.
- **Downloaded a release:** extract the new archive over the old binary.

The next status line refresh uses the new version. Run `paceline --version` to check which one you have. paceline never touches the network, so it can't tell you when a new release is out: watch the [releases page](https://github.com/rogadev/paceline/releases) for that. On Windows, if replacing the file fails because it's in use, try again; paceline only runs for a moment on each refresh.

### Verify a download

Every release archive has a signed build provenance attestation: proof that GitHub Actions built it from this repository. With the [GitHub CLI](https://cli.github.com):

```sh
gh attestation verify paceline_1.1.0_linux_amd64.tar.gz --repo rogadev/paceline
```

`checksums.txt` in each release lists the SHA-256 of every archive.

## What each segment means

| Segment | Example | Shows |
|---|---|---|
| Model | `Opus 5.5` | The model, without the context-size suffix. |
| Effort | `high effort` | Only when effort is above `low` or `medium`. |
| Fast mode | `fast mode` | Only while fast mode is on. |
| Project | `techcentral (dev)` | The folder, in a color unique to the project, and the git branch. |
| Session | `s 82% (resets 3:40pm)` | Five-hour limit used. The reset time appears once it passes 70%. Verbose: `session 82%`. |
| Week | `w 4%` | Weekly limit used. Verbose: `week 4%`. |
| Today | `t 46% of 28%` | How much of today's budget you've used, then the budget: today's share of the weekly limit, fixed for the day. Climbs past 100% when you go over. Verbose: `today 46% of 28% budget`. |
| Context | `ctx 72%` | Only when the context window is 70% or more full. |
| Cache | `cache cold: 82k @ ~2x` | Only when the prompt cache has expired. |
| Progress | `loop ▰▰▰▰▱ #44 3/5` | Only while an agent reports a long job's progress. See [Progress bar](#progress-bar). On a narrow pane, hide it with `"segments": {"progress": false}`. |
| Duration | `16m` | Session wall-clock time. Always last, at the right edge. |

**Today's budget** is the weekly percentage left at the start of the day, divided by the days from midnight to the reset. It stays fixed all day, and the today segment shows how much of it you've used. Whatever you don't spend spreads over the remaining days, so a light week gives you bigger budgets later on. The color is your pace advice: green while you have room, yellow from 70% of the budget, and red from 85% and once you go over. On the final day before the reset, the segment shows `⏳ last day, resets 9pm` instead.

**Project colors** come from a hash of the project folder name, mapped to one of 12 evenly spaced hues in the OKLCH color space. Every color is equally bright and easy to read on a dark terminal. Each project keeps its color everywhere, including in subfolders.

## Progress bar

When an agent works through a long job on its own, such as a batch of issues or a multi-task build, paceline can show how far along it is:

```
loop ▰▰▰▰▱ #44 3/5          one cell per step: done, active, skipped, blocked, pending
orc ▰▰▱▱ #42 review 55%     a planned run: weighted percentage, moving within a step
loop ▰▰▰ review 2/3         a status label the agent set
loop ▰▰▰ ✓ done             finished; hidden 30 minutes later
```

Green cells are done, cyan is the active step, dim is skipped, red is blocked, and hollow cells are still to come. A running job with no update in an hour shows `(paused)`, since whatever was driving it most likely stopped.

Each Claude Code window shows only its own run. Agents in four windows on the same repository get four separate bars, and a new window never shows a run that an earlier session left behind.

The bar comes from a small file in the git directory of the repository you're working in (in a worktree, the worktree's own git directory). paceline only reads it.

- **Agents** write it through the MCP server below, one file per Claude Code session: `.git/paceline/progress-<session id>.json`. Files from sessions that haven't written for a day are cleaned up when a new run starts.
- **Any other program** can write the shared file, `.git/paceline/progress.json`, using the format at the end of this section. Every window in the repository shows it when it has no run of its own, and it's hidden after 4 hours with no update.

### Let agents report progress

`paceline-mcp` is an MCP server that ships in the same release archive as `paceline`. It gives agents five tools: `progress_start`, `progress_step`, `progress_label`, `progress_add_steps`, and `progress_finish`. Register it once for every project:

```sh
claude mcp add --scope user paceline -- /path/to/paceline-mcp
```

If you build with Go, `go install github.com/rogadev/paceline/cmd/paceline-mcp@latest` puts it next to `paceline`.

The tools tell the agent when to use them, so a long job reports progress without extra instructions. To make it a habit, add a line to a skill or to `CLAUDE.md`:

> For work with three or more steps, plan the steps first, then report progress with the paceline progress tools.

**Plan first for an accurate bar.** Without a plan, the bar counts steps, so a big step and a small one move it equally. When the agent starts the run with each step's `weight` (its size relative to the others) and the `stages` it will pass through (for example, `design`, `build`, `review`, `commit`), paceline shows a weighted percentage that also moves as a step advances through its stages.

### Works with orc-pack

[orc-pack](https://github.com/rogadev/orc-pack), an autonomous orchestrator for Claude Code, reports its runs through these tools. With `paceline-mcp` registered, an `/orc` run or an `/orc-loop` batch plans its steps and shows up as a bar, such as `orc ▰▰▱▱ #42 review 55%`, with no extra setup.

### Progress file format

Any tool can write this file. Write it through a temp file and a rename, so paceline never reads half of it.

```json
{
  "version": 1,
  "name": "orc",
  "phase": "running",
  "label": "",
  "steps": [
    { "id": "41", "label": "#41", "status": "done", "weight": 3 },
    { "id": "42", "label": "#42", "status": "active", "weight": 5,
      "stages": ["design", "build", "review", "commit"], "stage": "review" },
    { "id": "43", "label": "#43", "status": "pending", "weight": 2 }
  ],
  "updatedAt": "2026-10-06T10:15:00Z"
}
```

| Field | Required | Meaning |
|---|---|---|
| `version` | yes | Always `1`. |
| `name` | yes | Short name shown before the bar, such as `loop` or `orc`. |
| `phase` | yes | `running`, `done`, or `halted`. |
| `label` | no | Replaces the active step's label in the status, such as `review 2/3` or `planning`. |
| `steps` | yes | 1 to 50 steps, in order. Each has a unique `id`, a `label`, and a `status`: `pending`, `active`, `done`, `skipped`, or `blocked`. |
| `steps[].weight` | no | The step's size relative to the others, 0 to 1000. Missing or 0 counts as 1. |
| `steps[].stages`, `steps[].stage` | no | Up to 12 stages the step passes through, and the one it's in now. An active step counts the stages before its current one as finished. |
| `updatedAt` | yes | When the file was last written, as an RFC 3339 time. paceline uses it rather than the file's modified time, so a copied file doesn't look fresh. |

Text fields are at most 40 characters. paceline ignores a file that breaks any of these rules or is larger than 32 KB, and it strips control characters from every label before printing it. The percentage leaves out skipped and blocked steps, since the run won't do them.

## Configuration

Create `~/.claude/paceline.json` (or `$CLAUDE_CONFIG_DIR/paceline.json`) to change the defaults. Every key is optional.

```json
{
  "segments": { "duration": false, "cache": false },
  "thresholds": { "headroomGreen": 30, "headroomYellow": 15, "contextWarn": 70, "contextCritical": 85 },
  "quietEfforts": ["low", "medium"],
  "projectSlots": { "website": 4 },
  "verbose": false
}
```

- `segments`: turn any segment off: `model`, `effort`, `fastMode`, `project`, `branch`, `session`, `week`, `today`, `context`, `cache`, `duration`, `progress`.
- `thresholds`: percentages where colors change from green to yellow to red, and where the context warning appears. `headroomGreen` and `headroomYellow` count what's left of a limit, so the defaults turn session, week, and today yellow at 70% used and red at 85%.
- `quietEfforts`: effort levels that don't need a label.
- `projectSlots`: pin a project to a color slot from 0 to 11 when two projects you use together get the same color.
- `verbose`: spell out the labels (`session 16%`) instead of one letter (`s 16%`). `paceline style` changes it for you.

paceline respects [`NO_COLOR`](https://no-color.org).

## Security

paceline runs on every status line refresh, so it's built to do very little:

- No dependencies. It uses only the Go standard library, and a test fails if that changes.
- It never starts processes or touches the network. A test fails the build if the shipped code imports `os/exec`, `net`, `syscall`, `unsafe`, or `plugin`. The git branch is read from `.git/HEAD` directly, so a repo's git config or hooks can't run code when paceline renders.
- It strips control characters, zero-width characters, and bidi overrides from every folder name, branch name, payload field, and progress label before printing, so a hostile repo name can't send escape sequences to your terminal.
- `paceline-mcp` follows the same import rules. Its only effect is writing the progress file in a repository's git directory.
- The installer never overwrites a settings file it can't parse, and it writes through a temp file so a crash can't truncate your settings.
- Releases are reproducible builds with signed provenance attestations.

See [SECURITY.md](SECURITY.md) to report a vulnerability.

## License

MIT
