# paceline

paceline as a Claude Code mod: a TypeScript plugin that draws paceline's usage line in the band above the prompt. It is a port of paceline's original Go status line. The Go status line was retired on 2026-10-16 in favor of this mod, and its repository was deleted on 2026-11-15, when this repo took over the `rogadev/paceline` name (it was `rogadev/paceline-mod`). Nothing refreshes `~/.claude/paceline-day.json` any more. Since 0.5.0 the band also draws the review-batch and `/orc-loop` segments (#2), which a personal `statusLine` wrapper (`~/.claude/statusline/statusline.mjs`) used to show under the prompt; that wrapper can be switched off once the band shows them. paceline-tray is retired and kept only for posterity.

Read `docs/notes.md` before changing how the mod talks to Claude Code: it holds what the mods platform can and can't do, the drawing test's results, and the API facts this design rests on.

## Layout

```
.claude-plugin/plugin.json       manifest; the plugin's name is `paceline`
.claude-plugin/marketplace.json  makes this repo a marketplace (`/plugin marketplace add rogadev/paceline`, then `/plugin install paceline@paceline`)
hooks/hooks.json                 names the hooks module
hooks/register.ts                the hooks module: events, refresh loop, and EVERY call on `$`
src/                             pure logic, no `$`: pace, render, color, md5, config, payload, progress, forecast, loop, batch, gitinfo, sanitize, timefmt, model, live, band, usage, tools
test/                            *.test.ts, run by `claude plugin test`
test/fixtures/                   the deleted Go repo's parity.json and progress.json, copied as TypeScript; frozen
.claude-plugin/types/            written by the engine when it loads the mod; gitignored
.claude/CLAUDE.md                this file (a CLAUDE.md at the plugin root draws a validate warning)
docs/notes.md                    platform research and design notes
```

## Commands

```sh
claude plugin validate .   # what the module hooks and calls; anything the engine would refuse. Must pass with no warnings.
claude plugin test .       # every *.test.ts
tsc -p .                   # type check; needs .claude-plugin/types (see below)
```

`.claude-plugin/types/` appears once an interactive session loads the mod from a folder it watches: `claude --plugin-dir .`, or a link to this repo in the session's dev-mods folder (see Live testing). Without it, type-check with a scratch tsconfig whose `files` names the `claude-code.d.ts` the `plugin-authoring` skill writes, keeping `allowImportingTsExtensions` and the rest of this repo's options.

Run all three before every commit.

## Rules that are easy to break

- **Every call on `$` lives in `hooks/register.ts`, inside a function declared at the top level of that file.** `claude plugin validate` follows `$` only into functions declared in the hooks module itself. Passing `$` to an imported function, or to a function that isn't declared at top level, fails validation. `src/` takes plain data and returns plain data; `register.ts` does the reads and passes results in. Keep it that way so `src/` stays testable without an engine.
- **Observer hooks on gating events get `.catch(($, e, next) => next(e))`.** `tool.call` and the `classic.*` hooks here only watch. The catch lets the event go on if the hook throws, and replays `next` if the hook had already called it.
- **Never write `~/.claude/paceline-day.json`.** That's the Go paceline's day anchor. The mod reads it (and prefers it when it matches the same day and week) but keeps its own anchor in `$.store` under the key `day`. A bad write there once reset the user's budget for the day.
- **The forecast's history lives in `$.store` under `activity-v2`**: per local day, 24-bit masks of the hours seen and the hours usage rose, plus the weekly points spent that day, 14 days kept, plus each limit's highest reading in its current window and that window's reset time. Write it only when `recordActivity` says it changed; every session on the machine refreshes every 15 seconds.
  - Every session writes the same record, and an idle session still holds an older, lower reading. A reading counts as use only when it beats the record in the same window (same reset time); a lower one is stale, not a reset. Before 0.4.1 a drop was taken as a reset, so two open sessions flip-flopped and logged thousands of "spent" points a day.
  - `activity` is the pre-0.4.1 key. Older builds may still be running and writing it, so the mod only reads it once, through `migrateActivity`, when `activity-v2` is empty. If the record's shape or meaning changes again, move to a new key the same way.
- **Segments must match the Go paceline byte for byte.** `test/parity.test.ts` renders every case in the Go fixtures through `toAnsi` with the Go layout: no `compact` and no `limits` pick, with the session segment forced on as the Go parity test does. The mod diverges on purpose through the `compact` option on `RenderContext`, switched on in `renderLive` and nowhere else: the project ahead of the model, effort as the bare level, and, when `src/forecast.ts` says a limit runs out before it resets, `out ~Tue` in place of the week's days left or the session's reset time. The `loop` and `batch` segments exist only in `COMPACT_ORDER`; the Go line has neither. Otherwise, today and the week stay the Go segments in both layouts. Without `compact`, `render` must keep printing the Go line. The Go repo is gone, so the parity cases are now the spec: if you change what a segment shows in the Go layout, change the parity test on purpose and say so in the README.
- **Don't edit the fixtures by hand.** They were copied from the Go repo's `internal/render/testdata/` at the commits named in each file's header. That repo was deleted, so they're frozen: a change to the Go layout's output belongs in a new test, not an edited fixture.
- **Source files are ASCII.** Write non-ASCII characters as `\uXXXX` escapes in `.ts` files (the line's glyphs, the middle dot, ellipses). The file tools have turned escapes into raw characters before, so scan with Python after writing anything with escapes. Markdown is exempt.
- **Imports name the `.ts` extension** (`'./render.ts'`). The engine loads `.ts` directly; `tsconfig.json` sets `allowImportingTsExtensions`.
- **Tests can't read files.** `claude plugin test` runs in an environment with no fs, network, or process, and the engine loads only code files, so fixtures are `.ts` modules that export data.
- **Calendar math takes a `Zone`.** Live code passes `'local'`; tests pass `'utc'`, because the fixtures were recorded in UTC.
- **Module variables reset on every hot reload.** The first refresh refills `line`. Anything that must survive a reload belongs in `$.state` (resets on `/clear`) or `$.store` (persists, shared by every session on the machine, and a get-then-set isn't atomic).

## Live testing

The user judges changes by seeing them, so try every change live before committing:

1. Load the repo in a session, either with `claude --plugin-dir ~/Documents/GitHub/paceline`, or by linking the repo into the current session's mods folder (Windows: `New-Item -ItemType Junction -Path <dev-mods>/<session>/paceline -Target <repo>`). The `plugin-authoring` skill names that folder.
   - A link does nothing until the `plugin-authoring` skill has loaded in that session and the user has answered "Enable for this session" to its hot-reloading question. Load the skill first, then make the link.
   - If the session also has `paceline@paceline` installed from the marketplace, it loads the mod twice and can draw two bands. Disable the installed copy for that session, or test in one without it.
2. With hot reloading on, the mod reloads when a turn that edited it ends. A failed hook or a refused tree shows as a dim line in the transcript naming `paceline`.
3. Show the user, and commit only after they approve.

## Git and release

- Work on `dev`. `main` and `dev` both start at the first commit. Push to `dev`, then stop: don't watch CI or open a PR unless asked.
- Conventional commit subjects (`feat:`, `fix:`, `docs:`, `test:`) that name the change, not the process. No attribution lines.
- The version is in `.claude-plugin/plugin.json`. A user on a marketplace install gets a new version only when it changes, so bump it with every release.
- CI (`.github/workflows/ci.yml`) runs `claude plugin validate` and `claude plugin test`, and on a pull request fails when `src/`, `hooks/`, or the manifests changed without a new version. It skips `tsc`, which needs the engine's generated types. The `ci-ok` job is the one check to require on `main`.
- Releasing: merge to `main` with a bumped version. `.github/workflows/release.yml` tags `v<version>` and creates a GitHub release with generated notes when that tag doesn't exist yet. Marketplace installs see the update from the version in `plugin.json`, not from the tag.
