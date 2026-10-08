# The demo graphic

`docs/paceline-states.svg`, the animated status line at the top of the README, is generated, not drawn. So is `scenes.json` beside this file, which the SVG and the demo on rogadigital.com both read. Every status line in them is what paceline's own renderer prints for a fixed set of inputs, so the graphic can't drift from the real output.

## Regenerate

From the repository root:

```sh
go run ./tools/demo
```

That rebuilds both files. Run it whenever you change what the status line prints, and commit the result. `go test ./tools/demo` fails while either committed file differs from a fresh regeneration, so a format change can't ship with a stale graphic.

The output is byte-for-byte reproducible: every scene uses fixed times in UTC, so the machine's clock and time zone don't matter.

## How it works

`tools/demo` holds the scene script in Go (`scenes.go`). For each step of each scene it builds a Claude Code payload, a fixed `Now`, the default config, and today's-budget snapshot, then calls `render.Render`, the same function the `paceline` binary calls. It records the raw ANSI output, parses it into styled runs (`ansi.go`), and writes `scenes.json`. The SVG writer (`svg.go`) draws a mock Windows Terminal around those runs from `scenes.json`'s data and embeds a subset of Cascadia Mono.

The writer lays text on a fixed column grid using the font's advance width, and the tool fails if any line is wider than the terminal's 110 columns.

## The scenes

| id | Title | What it shows |
|---|---|---|
| `fresh-day` | Fresh day | Today's budget is set at the start of the day and starts full: `t 100% of 28%`. |
| `working-day` | Working through the day | Session, week, and today count down; today turns yellow below 30% left and red below 15%. |
| `over-budget` | Over budget | Past the budget, today shows how far over you are, in red: `t over 18% of 28%`. |
| `session-low` | Session running low | Below 30% left the session shows its reset time; `ctx` appears at 70% and turns red at 85%. |
| `cache-cold` | Cache went cold | `cache cold: 82k @ ~2x` appears once the prompt cache expires. |
| `agent-job` | Agent job | A `loop` progress bar advances step by step to `✓ done`, with the subagent list below. |
| `several-windows` | Several windows | Four windows share the same limits, each project in its own color. |
| `last-day` | Last day | On the final day, the budget is everything left in the week: `t 99% of 68%`. |

## scenes.json schema

Another repository reads this file, so its shape is versioned. `version` changes when a field is removed, renamed, or changes meaning; adding a field doesn't change it.

```jsonc
{
  "version": 1,
  "columns": 110,              // terminal width, in character cells
  "palette": {                 // every color the graphic uses, as #rrggbb
    "background": "#0c0c0c", "foreground": "#cccccc", "dim": "#767676",
    "black": "…", "red": "…", "green": "…", "yellow": "…",
    "blue": "…", "magenta": "…", "cyan": "…", "white": "…",
    "brightBlack": "…", "brightRed": "…", /* …the other bright colors… */ "brightWhite": "…",
    "reply": "…",              // the ● before the last reply
    "spinner": "…",            // ✻ and the spinner verb
    "promptRule": "…",         // the two rules around the prompt
    "modeLine": "…"            // the permissions mode line
  },
  "scenes": [
    {
      "id": "fresh-day",
      "title": "Fresh day",
      "caption": "One plain sentence about what the scene shows.",
      "layout": "terminal",    // or "windows": several stacked status lines
      "durationMs": 5000,      // the sum of its steps' durationMs
      "steps": [
        {
          "durationMs": 2500,
          "now": "2026-10-09T09:02:00Z",   // the render time, UTC
          "reply": "…",                     // last assistant line; omitted when empty
          "spinner": { "verb": "Crafting", "activity": "12s · ↓ 0.8k tokens" },  // omitted when idle
          "prompt": "…",                    // text after "> "; "terminal" layout only, may be ""
          "modeLine": "⏵⏵ bypass permissions on (shift+tab to cycle)",  // omitted in "windows" layout
          "subagents": [                    // omitted when the scene has no agents
            { "name": "main" },             // the first entry is always the main agent
            { "name": "ui-implementer", "task": "Fix round 1 #328 t2" }
          ],
          "statusLines": [                  // one in "terminal" layout, one per window in "windows"
            {
              "ansi": "\u001b[36mOpus 5.5\u001b[0m …",  // exactly what render.Render returned
              "plain": "Opus 5.5 · acme-portal (dev) · …", // the same line without escapes
              "runs": [
                { "text": "Opus 5.5", "color": "cyan" },
                { "text": " " },
                { "text": "·", "dim": true }
              ]
            }
          ]
        }
      ]
    }
  ]
}
```

A run's `color` is either a palette key (`cyan`, `green`, `yellow`, …) for the renderer's basic ANSI colors, or `#rrggbb` for its truecolor codes: the project colors and the muted red. A run with no `color` uses `foreground`. `dim` and `bold` are present only when true; draw `dim` text in the `dim` color. Concatenating a line's run texts gives `plain`.

The basic colors map to Windows Terminal's Campbell scheme, so the graphic matches what paceline looks like there. The four chrome colors (`reply`, `spinner`, `promptRule`, `modeLine`) approximate Claude Code's own UI; paceline doesn't print them.

Each step is on screen for its `durationMs`, steps play in order, and the scenes loop.

## Font

An SVG shown through an `<img>` tag can't load fonts from anywhere else, and fallback monospace faces have different widths, so the SVG embeds its own font as a WOFF data URI.

- **Font:** Cascadia Mono 2407.24, static Regular, from the [microsoft/cascadia-code v2407.24 release](https://github.com/microsoft/cascadia-code/releases/tag/v2407.24).
- **Licence:** SIL Open Font License 1.1. The licence text is in `tools/demo/font/OFL.txt`, beside the subset (`tools/demo/font/CascadiaMono-Regular-subset.woff`). Inside the SVG the face is named `paceline-demo-mono`, so an installed Cascadia Mono is never used in its place.
- **Not in the font:** `⏵` and `✻` appear only in Claude Code's chrome (the mode line and the spinner), and Cascadia Mono doesn't have them. The writer draws them as one-cell vector shapes.

The subset holds only the characters the graphic uses. It was made with [fontTools](https://github.com/fonttools/fonttools) in a throwaway virtual environment, not a repository dependency:

```sh
python -m venv fontvenv
fontvenv/bin/pip install fonttools          # fontvenv/Scripts/pip on Windows
fontvenv/bin/python -I -m fontTools.subset CascadiaMono-Regular.ttf \
  --unicodes="$U" --layout-features= --drop-tables+=GSUB,GPOS,GDEF,DSIG \
  --no-hinting --desubroutinize --name-IDs=0,1,2,3,4,5,6,14 \
  --flavor=woff --output-file=CascadiaMono-Regular-subset.woff
```

`$U` is the comma-separated list of code points. If a scene starts using a character the subset lacks, `go run ./tools/demo` fails, names the character, and prints the complete `--unicodes=` list to use. Re-run the command with it and copy the result over `tools/demo/font/CascadiaMono-Regular-subset.woff`.
