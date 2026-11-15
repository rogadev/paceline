# Notes: the mods platform and how paceline uses it

What we learned while building v0.1.0 on Claude Code 2.1.296 (2026-10-11). The mods API is early access and changes between releases: when something here disagrees with the types the engine writes to `.claude-plugin/types/claude-code/index.d.ts`, the types win. Update this file when you find out.

## Why a mod, and why TypeScript

- A mod is a plugin whose `hooks/hooks.json` names a hooks module that exports `register(on)`. Claude Code runs it in its own process, in an environment with **no Node and no DOM**: only JavaScript or TypeScript, and everything outside the module goes through `$`. Go can't run inside a mod. The only way to reuse the Go binary would be to start it with `$.process.run` on every refresh, which would mean two installs and a process per redraw, so the logic was ported instead.
- Mods need Claude Code v2.1.287 or later. They draw in the terminal and in the Desktop app's Code tab. Hooks also run, without drawing, in the VS Code chat panel, `claude -p`, and the Agent SDK.
- A mod runs with the user's permissions and isn't sandboxed. That's unrelated to bypass-permissions mode: paceline needs no permission mode at all.

## Where paceline can draw

- **A plugin can't set the status line.** The manifest's `settings` key accepts only `agent` and `subagentStatusLine`, and the status line isn't a render site. The Go paceline keeps that slot, and the two can run side by side.
- **`$.ui.status(text)` doesn't work for the line.** Tested: it draws a plain-text notice under the prompt, shows ANSI escapes as garbage (`�[32m`), and the engine puts a warning glyph and the plugin's name in front. It's meant for short notices.
- **The `AbovePrompt` band works.** Tested: `Text` takes `color` as a hex string (`'#c792ea'`) or a theme key (`'warning'`, `'error'`). **`dimColor: true` and the `inactive` theme key both draw as plain text there**, far brighter than the Go line's ANSI dim, so `src/band.ts` draws dim parts in a fixed gray (`#767676`). It comes out slightly lighter than the Go line's dim; a darker gray is a one-line change. The terminal draws named colors (`'green'`, `'cyan'`, `'yellow'`) in its own palette, the same as the Go line's ANSI codes. The band shows a `[-]` collapse mark at its right edge (Ctrl+X Ctrl+A also collapses it), and other mods can draw there too. `src/band.ts` keeps what later mods draw by putting `await next(e)` under the line.
- A `Box` with `flexWrap: 'wrap'` around one `flexShrink: 0` Box per segment wraps whole segments onto the next row in a narrow window.
- Other places a mod can draw: a pane (`$.ui.open`, docked beside the transcript in a wide fullscreen terminal; a pane opened unasked waits for 144 columns), toasts (`$.ui.toast`), and restyling Claude Code's own sites (`Spinner`, `PromptHint`, messages, tool rows).

## Where each figure comes from

The Go paceline reads the status line JSON on stdin. The mod rebuilds the same payload shape from the API (`src/live.ts`), so `src/render.ts` runs unchanged:

| Payload field | Mod source | Notes |
|---|---|---|
| `model.display_name` | `$.session.model()` | Returns the raw ID (`claude-opus-5-5`), not the display name. `src/model.ts` maps it. |
| `effort.level` | `$.env.get('CLAUDE_EFFORT')`, then `e.effort.level` on `classic.PostToolUse` and `classic.Stop` (skipping events that carry `agent_id`), then `effortLevel` from `$.settings.read()` | No API reading. Claude Code keeps `CLAUDE_EFFORT` current for the session. See [Effort lags a `/effort` change](#effort-lags-a-effort-change). |
| `fast_mode` | `$.settings.read().fastMode` | Unverified: no API exposes fast mode, and this key may not be where Claude Code keeps it. |
| `workspace.current_dir`, `project_dir` | `$.session.cwd()`, `$.session.root()` | |
| `rate_limits` | `$.session.usage().rateLimits` | `{ kind: 'five_hour' | 'seven_day', percentUsed, resetsAt }`, with `resetsAt` as an ISO string. Empty off a subscription. The test showed these match the Go line exactly. |
| `context_window.used_percentage` | `$.session.usage().context.percent` | |
| `cost.total_duration_ms` | `$.clock.now() - usage.startedAt` | `startedAt` is the first launch for a resumed session. |
| `prompt_cache` | not available | No `cache cold` segment yet. The `model.switch` event carries `cache_ttl` (`'5m'` or `'1h'`) and `prompt_cache_warm`, and `classic.SessionStart` on resume carries `prompt_cache_likely_expired`, so an estimate (last response time plus TTL) may be possible. |
| git branch | `.git/HEAD` read with `$.fs` | `$.session.repo()` has no branch field. |
| HEAD and origin/main shas | the ref files, then `packed-refs`, read with `$.fs` | In a linked worktree, branches, remotes, and `packed-refs` live in the common git dir that the worktree's `commondir` file names; `HEAD` stays in the worktree's own git dir. `src/gitinfo.ts` follows both. |
| `/orc-loop` state | `<git dir>/orc-loop/state.json` read with `$.fs` | The worktree's own git dir, where orc-loop writes it. Stat first, capped at 256 KiB. |
| review batch | `git` through `$.process.run`, and `tmp/reviews/ledger.jsonl` | See [Running git for the batch segment](#running-git-for-the-batch-segment). |

`session.measure` fires after each main-thread turn and whenever a rate-limit window moves a whole point. That plus a 15-second timer drives refreshes.

### Effort lags a `/effort` change

- **`CLAUDE_EFFORT` comes first (0.5.4).** Claude Code sets it to the session's live level, and `$.env.get` reads it. On 2026-11-04 a long-running interactive session at `medium` drew `high` from its hook events while `CLAUDE_EFFORT` and a fresh session's events both said `medium`; the cause of the stale event level wasn't found. A `command.run` hook on `effort` redraws the band as soon as `/effort` runs. The bullets below describe the fallback.
- **Subagent events are ignored.** An event with an `agent_id` comes from a subagent, and its `effort.level` is the subagent's own (orc dispatches agents at `low` or `high`), not the main thread's. `turnEffort` in `src/live.ts` returns the cached level for those events and for events with no `effort`.
- **`classic.PreToolUse` can't serve as a source.** The settings-hook input for `PreToolUse` carries `effort`, but a module hook's event there is `ToolCallEnvelope` (`ClassicEventOf` in the engine's types), which has no `effort` and no `agent_id`. So the level changes only when a main-thread tool finishes or a turn stops, and a `/effort` change shows after the next tool completes.
- **#10 closed.** Whether `classic.PreToolUse`'s runtime `e` carries `effort` despite its type was never checked, and no longer matters: `CLAUDE_EFFORT` outranks any hook event's level.

## Validator and runtime rules

- `claude plugin validate` follows `$` only into functions declared at the top level of the hooks module: never across an import, and never into an arbitrary closure. This is why every `$` call is in `hooks/register.ts`.
- A hook on an event that can refuse something (`tool.call`, `classic.*`, and others) is listed as a gating hook. Watch-only hooks there get `.catch(($, e, next) => next(e))`.
- `$.fs.read` rejects files over 4 MiB. Stat first and cap smaller files yourself.
- `$.store` is one JSON file per plugin under `~/.claude/plugins/store/`, shared by every session on the machine; a get then set isn't atomic. `$.state` holds values for the session, survives hot reloads, redraws its readers when written, and resets on `/clear`, `/resume`, and `/branch`.
- Test environment: `claude plugin test` runs `*.test.ts` files with `test`, `expect`, and `describe` from `claude-code/testing`. Local time and `Intl` work there; fs, network, and processes don't.

## Running git for the batch segment

Decided for #2 (2026-10-19). Everything else in the mod reads files and starts no process. The batch segment does run git: the commit and issue counts and the "review stale (+2 commits)" check need history, which ref files alone can't give. The exception is kept narrow:

- **Gated.** git runs only when `segments.batch` is on, the host environment sets neither `GIT_DIR` nor `GIT_WORK_TREE`, the work tree has `.claude/skills/deep-review/scripts/ledger.mjs` (checked first, so a repository without deep-review costs one stat), HEAD is on a branch other than `main` that has a commit, and `refs/remotes/origin/main` resolves. Every check is a file read or an environment read. Outside such repos, no process starts.
- **Fixed argument lists, shas only.** `git -C <work tree> --no-lazy-fetch -c core.fsmonitor=false`, then `merge-base <origin/main> <HEAD>`, `log --no-show-signature --max-count=999 --format=%x00%B <base>..<HEAD>`, or `rev-list --count <reviewed>^{commit}..<HEAD>`. Every revision is a sha that passed a hex check, so nothing from the repository can read as an option or a revision expression. `--no-show-signature` keeps a repository's `log.showSignature` from starting its `gpg.program`. The fsmonitor pin is defense in depth, because none of these commands read the index. The argument lists, and the rules for what each result means, are built in `src/batch.ts` and tested there.
- **git finds the repository itself.** The mod never sets `GIT_DIR` or `GIT_WORK_TREE`, because an explicit `GIT_DIR` skips git's `safe.directory` ownership check: a `.git` planted by another user above a folder that isn't a repository would otherwise have its config loaded. With `-C <work tree>`, git runs its own discovery and ownership check, so such a repository exits 128 and the segment shows nothing. A host that sets either variable skips the segment, since git would follow the variable instead of the work tree. Other host git variables, such as `GIT_OBJECT_DIRECTORY`, still pass through, because `env` adds to the host environment and can't unset a variable. They are only ever set on purpose, and git only reads here.
- **A working folder the repository doesn't control.** git runs with its working folder set to the Claude config folder, not the work tree, so a `git` executable committed to the repository can't be found ahead of the real one on Windows. If the config folder isn't an absolute path, the segment is skipped. A drive-letter (`C:/`) or UNC (`//server/share`) path counts on any host. A path rooted at `/` counts only off Windows, because on Windows it is relative to the current drive. The engine doesn't report the platform, so a set `USERPROFILE` stands in for Windows (`batchAllowed` in `src/batch.ts`).
- **No lazy fetches.** A partial clone's `git log` could otherwise fetch missing objects over the network, a path to running code. The global `--no-lazy-fetch` option (git 2.45 and later, per its release notes) stops it, and a git too old to know the option exits 129, so the segment fails closed. `GIT_NO_LAZY_FETCH=1` is also set.
- **Hooks off, short timeout.** `$.process.run` runs git with repository hooks off. Each call gets 1.5 seconds. A `git log` whose output passes the 4 MiB stdout cap counts as no batch.
- **Cached, and never in the way.** The results are cached in module state under a key of the work tree, the HEAD and origin/main shas, and the ledger's mtime and size, all read without a process. git runs again only when that key changes. The git calls never hold a refresh: a refresh draws the cached batch (or none) and starts a background update. The update redraws when it settles, and it keeps its result only if its key is still the latest one asked for. A non-zero exit is kept until the key changes. A timeout, or a git that won't start, is tried again after a minute.
- **Per session.** The cache isn't in `$.store`. Each open session in a repo runs these calls once per key change, which is cheap, and keeping the store free of per-repository entries avoids its non-atomic get-then-set. A hot reload empties the cache, which costs one recompute.

## The limit forecast

The line warns about at most one of `s` and `w`, chosen in `src/forecast.ts`. The user asked for it on 2026-10-11: determine which limit runs out first and show only that one, to cut noise, keeping in mind 9-to-5 users, agents running 24/7, API-key users, and Max-plan users staying under their limits.

- **Activity profile.** Each refresh marks the local hour as seen, and as active if either limit's reading beats the highest on record in the same window, which is the one with the same reset time, within 10 minutes. A lower reading in that window is stale, from another session still holding an older value, not a reset. Only a later reset time starts a new window. Before 0.4.1 any drop counted as a reset, so two open sessions flip-flopped and logged thousands of spent points a day. Per hour of the day, the chance of working is `(active days + 0.25) / (completed days + 0.5)`: 0.5 with no history, about 0.97 or 0.03 after a week of steady use. Weekdays and weekends share one profile in v1.
- **Rates.** Week, the first that applies:
  1. **Recent days**: each day's weekly points spent over its active hours, averaged with a weight that halves for each day back and is scaled down for a day with fewer than 4 active hours. Today's points come from the day snapshot, scaled to the share of today's hours the mod saw. Days above 100 points are corrupt and skipped, and the rate needs a total weight of at least 0.25. Added on 2026-10-18, so a relaxed day after hard ones no longer projects the hard days' pace to the reset.
  2. **Today**: points spent today over today's active hours, once there are 2 or more.
  3. **Week average**: the week's points over the week's active hours.
  4. **Fallback**: the week's points over half its elapsed hours (at least 3).

  The debug tool reports which one applied as `rateSource`. Session: its points over the active hours in its five-hour window, or its elapsed time when none are recorded.
- **Projection.** Step 15 minutes at a time from now to each reset, burning `rate × chance`. The next hour counts as certain while usage rose in the last 20 minutes.
- **Pick.** The limit that runs out first; if neither does, the one forecast to end its window fuller, keeping the previous pick when the two are within 5 points.
- **No five-hour limit.** On 2026-10-30 Anthropic renamed Max 20x to Ultra and dropped the five-hour limit for verified Ultra accounts; the Max plan keeps it. Such accounts report only `seven_day`, so the forecast runs on the week alone. On the owner's Ultra account the last five-hour window on record reset on 2026-10-30. The mod keys on the missing reading, not on the plan: the plan name is only in undocumented `~/.claude.json` fields, `$.session.identity()` gives it only to mods that managed settings install, and an Ultra account that isn't verified yet still has the limit.
- **Display.** The pick shows only when it has an out time, that is, when it runs out before it resets. The warning replaces the week's days left or the session's reset time: `out ~Tue` while the week runs out more than a day away, then `out ~9pm`, and `out ~3:45pm` for the session. A pick without an out time changes nothing. The out time rounds down, to the hour for the week and to the quarter hour for the session, with `floorInDay` in `src/timefmt.ts`, which counts steps from the local start of the day so a half-hour-offset zone keeps its own hours. If the rounded time is already before now, the exact minute shows instead. A week warning is yellow until it's within 24 hours of running out, then red; a session warning is always red. The exception is the compact layout on the week's last day, that is, the last local day before the reset (the day before, for a reset at midnight): there the run-out time is colored by the clock time it names, red before 5 p.m., yellow from 5 p.m. to before 8 p.m., and green from 8 p.m. The today segment's last-day `out ~` time follows the same colors in compact. The thresholds are provisional constants in `src/render.ts`, `LAST_DAY_YELLOW_FROM` and `LAST_DAY_GREEN_FROM`. With `"limits": "both"` there is no pick, and the line shows both percentages after the today bar.
- **Gaps.** Usage on another device while the mod isn't running lands in the hour of the next reading. Time zones with half-hour offsets bucket hours on UTC hour boundaries. API-key users have no rate limits, so there's nothing to forecast; a cost forecast for them is a separate idea.

## Budget and progress tools

Built for #7 (0.6.0): the mod serves the Go `paceline-mcp` server's seven tools (`progress_start`, `progress_step`, `progress_label`, `progress_add_steps`, `progress_finish`, `get_usage`, and `get_today_budget`) plus `get_forecast_debug`, so the Go server can be uninstalled. A plugin named `paceline` gets tool names `mcp__paceline__<name>`, the same as the MCP server's, so agents and orc-pack keep working. The specs in `src/tools.ts` are pinned to the Go server's `tools/list` by `test/tools.test.ts`.

- **Registered in `session.start`, each guarded.** Each `$.tool.register` call sits in its own `try/catch`, after `/paceline-debug`. When an MCP server named `paceline` is configured, it owns the names, registration throws, and that tool's `tool.call` hook passes the call on, so the Go server answers it. The band and `/paceline-debug` are unaffected.
- **Live figures, not the feed.** `get_usage` and `get_today_budget` answer from the session's own usage figures (`src/usage.ts`), so `ageSeconds` is always 0 and `stale` always false; the fields stay so callers keep the same shape. The Go stale lead lines and its answers for a missing feed are dropped. `get_today_budget` reads the day snapshot the band uses and never writes `$.store` `day` or `paceline-day.json`.
- **Two Go sentences reworded.** They named the feed file, which the mod doesn't have: "no reading in the feed" is now "no reading from Claude Code", and "The feed has no weekly reading..." is now "There is no weekly reading from Claude Code...". The rest of the text and every JSON field match the Go answers.
- **No `instructions` text.** The Go server's MCP `instructions` (when to call each tool) has no place in the mods tool spec, so it isn't carried over. The tool descriptions still carry their own guidance.
- **No pruning.** `$.fs` has no delete, so the mod doesn't remove other sessions' day-old progress files as the Go server does.
- **Not atomic, and links are followed.** `$.fs` has no rename, so the write isn't Go's temp file and rename; a reader can catch a half-written file. `$.fs.write` follows links, so a link at the progress path is followed (#16).

## Ideas not built yet

- **Toast when today's budget runs out**, from `session.measure`. Only a mod can do this.
- **`/paceline` command** to change the style, segments, or count direction without editing `paceline.json`. Register it in `session.start` with `$.command.register`.
- **A history pane**: day-by-day usage kept in `$.store`, drawn with `Raster` for a sparkline in the terminal.
- **The prompt-cache warning**, from the events above.
- **CI** that installs Claude Code and runs `validate` and `test`.

## Sources

- [Mods overview](https://code.claude.com/docs/en/plugins/mods/overview)
- [Create a mod](https://code.claude.com/docs/en/plugins/mods/create)
- [Draw in the interface](https://code.claude.com/docs/en/plugins/mods/interface)
- [Plugin manifest reference](https://code.claude.com/docs/en/plugins-reference)
- [Built-in mod source](https://github.com/anthropics/claude-code/tree/main/mods) and [sample mods](https://github.com/anthropics/claude-code-playground/tree/main/claude-code/mods); `token-weather` draws in the same band
- The `plugin-authoring` skill bundled with Claude Code: its `reference.md`, examples, and `types/claude-code.d.ts`
