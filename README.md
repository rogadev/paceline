# paceline

A Claude Code status line that paces your weekly usage limit across the days you have left.

```
Opus 5.5 · techcentral (dev) · 84% session · 96% week · ▲ 54% left of today's 28% budget · 16m
```

Your weekly limit resets on a fixed schedule, but the status line only tells you how much is left, not whether that's a lot or a little for the days remaining. paceline divides what's left by the days until the reset and gives you **today's budget**, then counts it down as you work. A light week shows a big budget and a ▲; a heavy one shows a small budget and a ▼. It's most useful on weekends, when you're deciding whether to go hard on side projects or save the rest for Monday.

## Install

Requires Node.js 22 or later.

```sh
npm install -g paceline
paceline install
```

`paceline install` adds paceline as the `statusLine` in `~/.claude/settings.json`. It backs up the file first, and it won't replace a status line you already have unless you pass `--force`. To go back:

```sh
paceline uninstall
```

That removes paceline and restores whatever status line it replaced.

## What each segment means

| Segment | Example | Shows |
|---|---|---|
| Model | `Opus 5.5` | The model, without the context-size suffix. |
| Effort | `high effort` | Only when effort is above `low` or `medium`. |
| Fast mode | `fast mode` | Only while fast mode is on. |
| Project | `techcentral (dev)` | The folder, in a color unique to the project, and the git branch. |
| Session | `18% session (resets 3:40pm)` | Five-hour limit left. The reset time appears once it drops below 30%. |
| Week | `96% week` | Weekly limit left. |
| Today | `▲ 54% left of today's 28% budget` | Today's share of the week, counting down. Turns into `% used` past 100%. |
| Context | `ctx 72%` | Only when the context window is 70% or more full. |
| Cache | `cache cold: 82k @ ~2x` | Only when the prompt cache has expired. |
| Duration | `16m` | Session wall-clock time. |

**Today's budget** is the weekly percentage left at the start of the day, divided by the days from midnight to the reset. It stays fixed all day, so you can watch it count down. Whatever you don't spend spreads over the remaining days, so a light week gives you bigger budgets later on. The arrow compares today's budget to an even pace of 100% ÷ 7 per day: ▲ means you have more than even pace, ▼ means less, and ● means about even. On the final day before the reset, the segment shows `⏳ last day, resets 9pm` instead.

**Project colors** come from a hash of the project folder name, mapped to one of 12 evenly spaced hues in the OKLCH color space. Every color is equally bright and easy to read on a dark terminal. Each project keeps its color everywhere, including in subfolders.

## Configuration

Create `~/.claude/paceline.json` (or `$CLAUDE_CONFIG_DIR/paceline.json`) to change the defaults. Every key is optional.

```json
{
  "segments": { "duration": false, "cache": false },
  "thresholds": { "headroomGreen": 30, "headroomYellow": 15, "contextWarn": 70, "contextCritical": 85 },
  "quietEfforts": ["low", "medium"],
  "projectSlots": { "website": 4 }
}
```

- `segments`: turn any segment off: `model`, `effort`, `fastMode`, `project`, `branch`, `session`, `week`, `today`, `context`, `cache`, `duration`.
- `thresholds`: percentages where colors change from green to yellow to red, and where the context warning appears.
- `quietEfforts`: effort levels that don't need a label.
- `projectSlots`: pin a project to a color slot from 0 to 11 when two projects you use together get the same color.

paceline respects [`NO_COLOR`](https://no-color.org).

## Security

paceline runs on every status line refresh, so it's built to do very little:

- No runtime dependencies. It uses only Node.js built-ins.
- It never starts processes. The git branch is read from `.git/HEAD` directly, so a repo's git config or hooks can't run code when paceline renders.
- No network access.
- It strips control characters from every folder name, branch name, and payload field before printing, so a hostile repo name can't send escape sequences to your terminal.
- The installer never overwrites a settings file it can't parse, and it writes through a temp file so a crash can't truncate your settings.

Releases are published from GitHub Actions with [npm provenance](https://docs.npmjs.com/generating-provenance-statements), so you can verify that each version was built from this repository. See [SECURITY.md](SECURITY.md) to report a vulnerability.

## License

MIT
