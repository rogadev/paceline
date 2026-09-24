# paceline project profile

Facts every `/deep-review` lane needs about paceline (`rogadev/paceline`, public, MIT). Read this before the diff. If the code in front of you contradicts this profile, trust the code and report the line under `Profile drift`.

## What it is and who uses it

- paceline is a Claude Code status line: a small CLI that Claude Code runs on every status line refresh. It reads one JSON payload on stdin and prints one line of text (with ANSI colors) on stdout.
- Its headline feature is **today's budget**: the weekly limit left at the start of the day, divided by the days until the weekly reset, counted down as the user works. It also shows the model, effort, fast mode, a per-project color, the git branch, session and week headroom, context use, cache state, and duration.
- It also has two commands that change the user's machine: `paceline install` (points `statusLine` in `~/.claude/settings.json` at this binary) and `paceline uninstall` (restores what it replaced).
- Users are Claude Code users on macOS, Linux, and Windows who download a release binary or run `go install`. Strangers install it, so a defect runs on other people's machines on every refresh.

## The "UI"

There is no web UI. The user-facing surfaces are:

1. **The status line itself**: one line in the Claude Code terminal, rendered by `internal/render`. Readers glance at it, on a dark terminal most of the time. Its look (order, separators, glyphs, colors, wording, width) is the product.
2. **CLI output**: `install`, `uninstall`, `--version`, `--help`, and error messages on stderr.
3. **Docs**: `README.md` (install, segment table, configuration), `CONTRIBUTING.md`, `SECURITY.md`. These are the only onboarding a stranger gets.

## Stack

- **Go** (module `github.com/rogadev/paceline`, `go 1.26.0` in `go.mod`). **Zero dependencies**: standard library only, no `go.sum`. `internal/policy` tests fail the build if a require appears or shipped code imports `os/exec`, `syscall`, `unsafe`, `plugin`, `net`, or any `net/...` package.
- **Go source is ASCII only** (`TestGoSourceIsASCII`). Non-ASCII glyphs in output (the arrows, the middle dot, the hourglass, the ellipsis) are written as backslash-u escapes in string literals, never as literal characters. This guards against "Trojan Source" (hidden bidi or zero-width characters).
- **Node** is release tooling only: `package.json` is private (`paceline-release-tooling`) with commitlint, semantic-release, `@semantic-release/exec`, and the conventionalcommits preset pinned to 9.3.1 (10.x needs a newer changelog writer than semantic-release bundles). `test/release-config.test.js` renders notes through the real plugins. No Node code ships to users.
- Lint: golangci-lint v2 (`.golangci.yml`: gosec, errorlint, misspell, unconvert, gocritic; errcheck ignores `fmt.Fprint*`; gosec excluded in tests and `tools/`). `gofmt`, `go vet`, `govulncheck`.

## Repo map

- `cmd/paceline/main.go`: the binary. `run(args, stdin, stdout, stderr) int` for testability; `renderFromStdin` caps stdin at 1 MiB and falls back to printing `Claude Code` on bad input; `buildVersion()` prefers the ldflags `main.version`, then build info, then `dev`; `executablePath()` refuses `go run` builds (paths containing `/go-build`); `var resolveExecutable = executablePath` is the test seam; `runInstall`, `runUninstall`.
- `internal/payload`: decodes Claude Code's JSON. Custom `Num`, `Str`, `Bool` types with `UnmarshalJSON` stay unset on a wrong JSON type (a plain Go pointer field would be allocated as zero, which would show "100% session"). `Decode` ignores field-level `UnmarshalTypeError`.
- `internal/render`: `Render(*payload.Payload, Context) string`. `Context{Now, Config, NoColor, ReadSnapshot, WriteSnapshot, GitBranch}` injects every side effect so tests are pure.
- `internal/pace`: today's budget. `Compute(usedPct, resetsAt, now, *Snapshot) Result` with `Kind` (None, LastDay, Budget) and `Direction` (Even, Up, Down: up at 1.25x or more of 100/7 per day, down at 0.8x or less). The daily snapshot `{date yyyymmdd, resetsAt, usedAtStart}` lives in `~/.claude/paceline-day.json`; `WriteSnapshot` writes a temp file and renames, mode 0600.
- `internal/timefmt`: `StartOfDay`, `DateKey`, `Clock` (for example `3:40pm`), `Duration`. Local time zone; DST matters.
- `internal/color`: headroom levels, OKLCH to sRGB with gamut stepping (L 0.80, chroma up to 0.14, hue 15 + 30 x slot), contrast math, `ProjectSlot` (md5 of the lowercased name, first byte mod 12), `SlotColor`.
- `internal/sanitize`: `Text(s, maxRunes)` strips C0 and C1 controls, zero-width characters, bidi overrides, BOM, and U+FFFD, and caps length with an ellipsis. Every string from outside (payload fields, folder names, branch names) must pass through it before printing.
- `internal/gitinfo`: finds `.git` walking up, reads `HEAD` directly (at most 512 bytes), follows worktree `gitdir:` files, never runs git. Branch truncated for display.
- `internal/config`: `~/.claude/paceline.json` (or `$CLAUDE_CONFIG_DIR/paceline.json`), 64 KiB cap. `Merge` validates each key on its own through raw JSON fields, so one bad key never discards the rest. Keys: `segments`, `thresholds`, `quietEfforts`, `projectSlots`.
- `internal/install`: `settings.go` is an order-preserving JSON object (`parseObject`, `get`, `set`, `remove`, `marshal`) so the user's `settings.json` keeps key order and untouched values; `install.go` has `BuildCommand` (normalizes slashes, rejects `"`, backtick, `$`, backslash, `%`, `!`, CR, LF; quotes the path), `IsPaceline`, `Install(claudeDir, exePath, force)`, `Uninstall`, a backup `settings.json.paceline-backup`, the record `paceline-install.json`, atomic writes that follow symlinks.
- `internal/policy`: repository rules tested like behavior (no deps, forbidden imports, ASCII source).
- `internal/render/testdata/parity.json`: 15 cases, 36 renders recorded from the 1.0 JavaScript version in UTC. `TestParityWithJavaScript` must reproduce every byte, including escape codes. `render_test.go` sets `time.Local = time.UTC` in `TestMain`.
- `tools/nextbump`: dev-only release preview (classifies conventional commits, prints the next version). Not shipped. Allowed to run git.
- `.github/workflows/ci.yml` (test matrix 3 OS x Go `1.26.x` and `stable`, race on Linux; quality job: gofmt, vet, golangci-lint, govulncheck, 90% coverage floor, GoReleaser snapshot; tooling job: npm test, audit, signatures; commits job: commitlint and the nextbump preview; `ci-ok` aggregates). `.github/workflows/release.yml` (on push to main: tests, semantic-release, which runs GoReleaser through the exec plugin, then build provenance attestations). `.goreleaser.yaml`, `release.config.js`, `.github/dependabot.yml`, `.githooks/commit-msg`, `commitlint.config.js`.

## Conventions and hard rules

- **Branches**: work on `dev`; `main` changes only by PR from `dev` with green `ci-ok`, merged with a merge commit. A merge to `main` releases automatically.
- **Conventional commits decide the version.** `feat` minor, `fix`/`perf` patch, `!` or `BREAKING CHANGE` major, everything else no release. Breaking for paceline means: removing or renaming a config key or segment name, changing install/uninstall behavior for existing users, or raising the minimum Go version. A commit whose type understates its effect ships the wrong version; that is a real finding.
- **Output stability**: a change to rendered output must update `parity.json` deliberately (and say why), never by regenerating it to make a test pass.
- **Side effects are injected.** Render and pace logic take time, config, and file access through parameters or `Context`; a direct `time.Now()`, `os.Getenv`, or file read inside `internal/render` or `internal/pace` logic breaks test isolation.
- **Fail quiet on refresh, loud on install.** The render path must never crash, hang, or print a Go error to the status line: bad input degrades to a shorter line or `Claude Code`. `install` and `uninstall` must refuse and explain rather than guess.
- **Cross-platform**: paths via `filepath`, Windows drive letters and backslashes, macOS `/private/var` symlinks, `RUNNER~1` short names, CRLF in files a user edits. Tests compare canonical paths (`filepath.EvalSymlinks`).
- **Security posture** is documented in `SECURITY.md` and README "Security". Treat those promises as requirements: a change that breaks one is a Blocker.
- **Style**: prose follows the Google developer documentation style (second person, present tense, sentence-case headings, serial comma). No smart quotes in source. Comments are sparse and explain why.

## Sanctioned decisions (do not flag)

- `fmt.Fprint*` return values ignored (errcheck exclusion): a failed terminal write has nowhere to be reported.
- `tools/nextbump` runs `git` through `os/exec`: it is a dev tool, excluded from the policy test and gosec.
- md5 in `color.ProjectSlot`: a stable, non-security hash for color choice.
- No `go.sum`, no dependencies, no vendoring.
- Node and `package-lock.json` exist only for release tooling; do not suggest moving the release to Go tooling or GoReleaser's own changelog.
- `@semantic-release/github` creates the release and GoReleaser uploads into it (`release.mode: keep-existing`, changelog disabled). This order is intentional.
- The version lives only in git tags; nothing is committed back to the repo on release.
- Output glyphs as backslash-u escapes in Go source (the ASCII rule).

## Local runs and renders

- Build: `go build -o <scratch>/paceline ./cmd/paceline`. Render: pipe a payload JSON into it. `NO_COLOR=1` gives plain text. Set `CLAUDE_CONFIG_DIR` to a scratch folder so the render never reads or writes the user's real `~/.claude` (the pace snapshot is a real write).
- `go run` builds refuse `install`; never run `install` or `uninstall` against the real config during a review.
