/**
 * Draws the README's two "where it lives" pictures: a Windows Terminal window
 * running Claude Code (docs/paceline-terminal.svg) and the Code tab of the
 * Claude desktop app (docs/paceline-desktop.svg), each mid-session with the
 * paceline line in the band above the prompt. The line comes from the mod's
 * own renderer in the compact layout, so it matches what the code draws; the
 * rest of each window is a mock.
 *
 * Run from anywhere with Node 23.6 or later, which runs TypeScript directly:
 *
 *   node docs/gallery/windows.mts
 *
 * This script is a docs tool; the mod never loads it.
 */

import { writeFileSync } from 'node:fs'

import { defaultConfig } from '../../src/config.ts'
import { decodePayload } from '../../src/payload.ts'
import { MUTED_RED, render, type Line, type RenderContext, type Tone } from '../../src/render.ts'

const sec = (iso: string) => Date.parse(iso) / 1000
const NOW = Date.parse('2026-10-07T09:00:00Z')
const RESET = sec('2026-10-10T00:00:00Z')

const DOT = '\u00b7'
const escape = (s: string) => s.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;')
const hex = (tone: readonly number[]) => '#' + tone.map(v => v.toString(16).padStart(2, '0')).join('')

/** Renders the line for a session in the api-server project on its dev branch. */
function line(week: number, start: number, minutes: number, more: Partial<RenderContext> = {}): Line {
  const payload = decodePayload({
    model: { display_name: 'Opus 5.5' },
    effort: { level: 'high' },
    fast_mode: false,
    workspace: { current_dir: '/r/api-server', project_dir: '/r/api-server' },
    rate_limits: {
      five_hour: { used_percentage: 22, resets_at: sec('2026-10-07T11:30:00Z') },
      seven_day: { used_percentage: week, resets_at: RESET },
    },
    context_window: { used_percentage: 30 },
    cost: { total_duration_ms: minutes * 60_000 },
  })!
  const snapshot = { date: '20261007', resetsAt: RESET, usedAtStart: start }
  return render(payload, {
    nowMs: NOW,
    zone: 'utc',
    config: defaultConfig(),
    readSnapshot: () => snapshot,
    saveSnapshot: () => {},
    branch: () => 'dev',
    compact: true,
    limits: { show: 'week' },
    ...more,
  })
}

// Where paceline sits gets a yellow outline and a label, as in the gallery.
const MARK = '#e8c468'

/** Outlines the band and labels it, on a dark pill so it reads on light pages. */
function callout(x: number, y: number, w: number, h: number, label: string, rx: number): string[] {
  const lw = 8 + label.length * 7.6
  return [
    `<rect x="${x}" y="${y}" width="${w}" height="${h}" rx="${rx}" fill="none" stroke="${MARK}" stroke-width="2"/>`,
    `<rect x="${x + 14}" y="${y - 19}" width="${lw}" height="22" rx="11" fill="#1a1a1a" stroke="${MARK}" stroke-width="1.5"/>`,
    `<text x="${x + 14 + lw / 2}" y="${y - 3.5}" text-anchor="middle" fill="${MARK}" style="font-family:'Segoe UI',Helvetica,Arial,sans-serif;font-size:12.5px;font-weight:600">${escape(label)}</text>`,
  ]
}

const close = (x: number, y: number, size: number, color: string) =>
  `<path d="M${x - size} ${y - size}L${x + size} ${y + size}M${x + size} ${y - size}L${x - size} ${y + size}" stroke="${color}" stroke-width="1.2" fill="none"/>`

/** Minimize, maximize, and close, ending at `right`, centered on `y`. */
function windowControls(right: number, y: number, color: string): string[] {
  const st = `stroke="${color}" stroke-width="1.1" fill="none"`
  const [a, b, c] = [right - 158, right - 98, right - 38]
  return [
    `<line x1="${a - 6}" x2="${a + 6}" y1="${y}" y2="${y}" ${st}/>`,
    `<rect x="${b - 6}" y="${y - 6}" width="12" height="12" rx="1.5" ${st}/>`,
    close(c, y, 6, color),
  ]
}

function svgFile(name: string, title: string, desc: string, w: number, h: number, style: string, body: string[]) {
  const out = new URL(`../${name}`, import.meta.url)
  const svg = [
    `<svg xmlns="http://www.w3.org/2000/svg" width="${w}" height="${h}" viewBox="0 0 ${w} ${h}" role="img" aria-labelledby="t d">`,
    `<title id="t">${escape(title)}</title>`,
    `<desc id="d">${escape(desc)}</desc>`,
    `<style>${style}</style>`,
    ...body,
    '</svg>',
  ]
  writeFileSync(out, svg.join('\n') + '\n')
  console.log(`wrote ${out.pathname}`)
}

// ---------------------------------------------------------------------------
// The terminal: Windows Terminal, Campbell scheme, Cascadia Mono.

const TERM = {
  bg: '#0c0c0c',
  tabRow: '#202020',
  fg: '#cccccc',
  bright: '#f2f2f2',
  dim: '#767676',
  rule: '#5a5a5a',
  claude: '#d77757',
  green: '#13a10e',
  blue: '#7aa2f7',
}

// Windows Terminal's Campbell palette for the line's named tones.
const CAMPBELL: Record<string, string> = {
  plain: TERM.fg,
  dim: TERM.dim,
  cyan: '#3a96dd',
  green: TERM.green,
  yellow: '#c19c00',
  red: hex(MUTED_RED),
}
const termTone = (tone: Tone) => (typeof tone === 'string' ? CAMPBELL[tone] : hex(tone))

const CELL = 8.4
const ROW = 21

type Cell = [text: string, color: string, bold?: boolean]

/** One terminal row at cell column `col`, each run stretched to its exact cells. */
function termRow(cells: Cell[], x: number, y: number): string[] {
  const out: string[] = []
  for (const [text, color, bold] of cells) {
    const w = CELL * [...text].length
    if (text.trim())
      out.push(
        `<text x="${x.toFixed(1)}" y="${y}" textLength="${w.toFixed(1)}" lengthAdjust="spacing" fill="${color}"${bold ? ' class="b"' : ''} xml:space="preserve">${escape(text)}</text>`,
      )
    x += w
  }
  return out
}

// Claude Code's mascot, as a grid of cells; 'h' is a half-width cell.
const MASCOT = ['.#######.', '.#.###.#.', '#########', '.#######.', '..h.h.h.h']

function terminal() {
  const W = 1200
  const H = 640
  const PAD = 14 // room around the window for the callout's label
  const wx = PAD
  const wy = PAD
  const ww = W - PAD * 2
  const wh = H - PAD * 2
  const TAB = 42
  const left = wx + 18
  const right = wx + ww - 18
  const body: string[] = []

  body.push(`<clipPath id="win"><rect x="${wx}" y="${wy}" width="${ww}" height="${wh}" rx="8"/></clipPath>`)
  body.push('<g clip-path="url(#win)">')
  body.push(`<rect x="${wx}" y="${wy}" width="${ww}" height="${wh}" fill="${TERM.bg}"/>`)

  // The tab row: one Claude Code tab, then new-tab and the dropdown.
  body.push(`<rect x="${wx}" y="${wy}" width="${ww}" height="${TAB}" fill="${TERM.tabRow}"/>`)
  const tx = wx + 8
  const tw = 260
  body.push(
    `<path d="M${tx} ${wy + TAB}V${wy + 14}Q${tx} ${wy + 8} ${tx + 6} ${wy + 8}H${tx + tw - 6}Q${tx + tw} ${wy + 8} ${tx + tw} ${wy + 14}V${wy + TAB}Z" fill="${TERM.bg}"/>`,
  )
  body.push(`<rect x="${tx + 12}" y="${wy + 18}" width="16" height="14" rx="2" fill="#2f6fb8"/>`)
  body.push(
    `<path d="M${tx + 15} ${wy + 21.5}L${tx + 19.5} ${wy + 25}L${tx + 15} ${wy + 28.5}M${tx + 21} ${wy + 29}H${tx + 25}" stroke="#ffffff" stroke-width="1.4" fill="none"/>`,
  )
  // Claude Code puts a spinner glyph in the tab title while it works.
  body.push(
    `<text x="${tx + 38}" y="${wy + 30}" class="ui" fill="${TERM.bright}"><tspan fill="${TERM.claude}">\u2733</tspan> Add rate limiting</text>`,
  )
  body.push(close(tx + tw - 18, wy + 25, 5, '#9a9a9a'))
  const px = tx + tw + 22
  body.push(`<path d="M${px - 6} ${wy + 25}H${px + 6}M${px} ${wy + 19}V${wy + 31}" stroke="#9a9a9a" stroke-width="1.2" fill="none"/>`)
  body.push(`<path d="M${px + 28} ${wy + 23}L${px + 32} ${wy + 27}L${px + 36} ${wy + 23}" stroke="#9a9a9a" stroke-width="1.2" fill="none"/>`)
  body.push(...windowControls(wx + ww, wy + 21, '#cfcfcf'))

  // The welcome banner.
  let y = wy + TAB + 28
  const s = 11
  MASCOT.forEach((row, r) =>
    [...row].forEach((ch, c) => {
      if (ch === '.') return
      body.push(`<rect x="${left + c * s}" y="${y - 8 + r * s}" width="${ch === 'h' ? s / 2 : s}" height="${s}" fill="${TERM.claude}"/>`)
    }),
  )
  const bx = left + 9 * s + 24
  body.push(...termRow([['Claude Code', TERM.bright, true], [' v2.1.325', TERM.dim]], bx, y + 6))
  body.push(...termRow([[`Opus 5.5 with high effort ${DOT} Claude Max`, TERM.dim]], bx, y + 6 + ROW))
  body.push(...termRow([['~\\Documents\\GitHub\\api-server', TERM.dim]], bx, y + 6 + ROW * 2))
  y += ROW * 4 + 6

  // The conversation so far.
  const conversation: (Cell[] | 'gap' | { user: string })[] = [
    { user: 'Add rate limiting to the public endpoints, 100 requests a minute per API key.' },
    'gap',
    [['\u25cf ', TERM.fg], ["I'll add a token-bucket limiter as middleware and apply it to the public router.", TERM.fg]],
    'gap',
    [['\u25cf ', TERM.green], ['Update', TERM.fg, true], ['(src/middleware/rateLimit.ts)', TERM.fg]],
    [['  \u23bf  ', TERM.dim], ['Added 42 lines', TERM.dim]],
    'gap',
    [['\u25cf ', TERM.green], ['Update', TERM.fg, true], ['(src/routes/public.ts)', TERM.fg]],
    [['  \u23bf  ', TERM.dim], ['Added 2 lines, removed 1 line', TERM.dim]],
    'gap',
    [['\u25cf ', TERM.dim], ['Bash', TERM.fg, true], ['(npm test -- rateLimit)', TERM.fg]],
    [['  \u23bf  ', TERM.dim], ['Running\u2026', TERM.dim]],
    'gap',
    [['\u273b ', TERM.claude], ['Brewing\u2026 ', TERM.claude], ['(1m 12s \u00b7 \u2193 4.1k tokens \u00b7 esc to interrupt)', TERM.dim]],
  ]
  for (const item of conversation) {
    if (item === 'gap') {
      y += ROW / 2
      continue
    }
    if ('user' in item) {
      body.push(`<rect x="${left - 8}" y="${y - 15}" width="${right - left + 16}" height="${ROW}" fill="#ffffff" fill-opacity="0.08"/>`)
      body.push(...termRow([['> ', TERM.dim], [item.user, TERM.bright]], left, y))
    } else body.push(...termRow(item, left, y))
    y += ROW
  }

  // The bottom of the screen: paceline's band, then the prompt box.
  const footY = wy + wh - 18
  const promptY = footY - ROW * 1.6
  const ruleBottom = footY - ROW + 2
  const ruleTop = promptY - ROW + 2
  const bandY = ruleTop - 14

  // The band indents two cells, as Claude Code's own rows under the prompt do.
  let bx2 = left + CELL * 2
  const pace = line(63, 58, 47)
  pace.forEach((segment, i) => {
    const cells: Cell[] = []
    if (i > 0) cells.push([` ${DOT} `, TERM.dim])
    for (const span of segment) cells.push([span.text, termTone(span.tone), span.bold])
    body.push(...termRow(cells, bx2, bandY))
    bx2 += CELL * cells.reduce((n, [t]) => n + [...t].length, 0)
  })
  body.push(...termRow([['[-]', TERM.fg]], right - CELL * 3, bandY))

  body.push(`<line x1="${left}" x2="${right}" y1="${ruleTop}" y2="${ruleTop}" stroke="${TERM.rule}"/>`)
  body.push(...termRow([['> ', TERM.fg]], left, promptY))
  body.push(`<rect x="${left + CELL * 2}" y="${promptY - 14}" width="${CELL}" height="18" fill="${TERM.fg}" fill-opacity="0.75"/>`)
  body.push(`<line x1="${left}" x2="${right}" y1="${ruleBottom}" y2="${ruleBottom}" stroke="${TERM.rule}"/>`)
  body.push(...termRow([['  \u23f5\u23f5 accept edits on', TERM.claude], [' (shift+tab to cycle)', TERM.dim]], left, footY))
  body.push('</g>')
  body.push(`<rect x="${wx + 0.5}" y="${wy + 0.5}" width="${ww - 1}" height="${wh - 1}" rx="8" fill="none" stroke="#3c3c3c"/>`)

  body.push(...callout(left - 10, bandY - 19, right - left + 20, 28, 'paceline', 6))

  svgFile(
    'paceline-terminal.svg',
    'paceline in a Claude Code terminal window',
    'A Windows Terminal window running Claude Code on a project called api-server. Claude is partway through a task, and the paceline line sits in the band just above the prompt box: the project and branch, the model, effort, today\u2019s budget bar with 5 of 14 points spent, the week at 63 of 100 with 3 days left, and the session time.',
    W,
    H,
    'text{font-family:"Cascadia Mono",Consolas,"DejaVu Sans Mono",monospace;font-size:14px;white-space:pre}' +
      '.b{font-weight:bold}.ui{font-family:"Segoe UI",Helvetica,Arial,sans-serif;font-size:13px}',
    body,
  )
}

// ---------------------------------------------------------------------------
// The desktop app: the Code tab, dark theme, Segoe UI.

const APP = {
  bg: '#1a1a1a',
  bar: '#1f1f1f',
  panel: '#1c1c1c',
  border: '#2e2e2e',
  card: '#262626',
  input: '#2a2a2a',
  fg: '#e6e6e6',
  body: '#d4d4d4',
  muted: '#9b9b9b',
  claude: '#d97757',
  link: '#7aa7e8',
}

// Off the terminal, the band draws named tones in fixed colors that read on
// light and dark (src/band.ts), and dim as a mid gray.
const SURFACE: Record<string, string> = {
  plain: APP.fg,
  dim: '#767676',
  cyan: '#2f9fbf',
  green: '#2e9e44',
  yellow: '#c99a1a',
  red: hex(MUTED_RED),
}
const appTone = (tone: Tone) => (typeof tone === 'string' ? SURFACE[tone] : hex(tone))

// Approximate Segoe UI advance widths, in ems, so each run can be placed and
// then held to that width with textLength whichever font the viewer has.
const NARROW = new Set([...'iljI.,:;\'|!\u00b7 '])
const SEMI = new Set([...'frt()[]/-'])
const WIDE = new Set([...'mwMW%'])
function textWidth(text: string, size: number): number {
  let em = 0
  for (const ch of text) {
    if (NARROW.has(ch)) em += 0.26
    else if (SEMI.has(ch)) em += 0.34
    else if (WIDE.has(ch)) em += 0.84
    else if (ch >= 'A' && ch <= 'Z') em += 0.62
    else em += 0.53
  }
  return em * size
}

const BAR_CELLS = /^[\u2588\u2591\u2592\u2595\u258f\u2502]+$/
const BAR_CELL = 9

/** paceline's line in the desktop band, starting at `x` on baseline `y`. */
function appLine(l: Line, x: number, y: number, size: number): string[] {
  const out: string[] = []
  const run = (text: string, color: string, bold?: boolean) => {
    if (BAR_CELLS.test(text)) {
      // Draw the bar's cells as shapes: block glyphs vary too much between
      // proportional fonts to line up.
      for (const ch of text) {
        const top = y - size + 2
        const h = size + 3
        if (ch === '\u2588') out.push(`<rect x="${x.toFixed(1)}" y="${top}" width="${BAR_CELL + 0.3}" height="${h}" fill="${color}"/>`)
        else if (ch === '\u2591') out.push(`<rect x="${x.toFixed(1)}" y="${top}" width="${BAR_CELL + 0.3}" height="${h}" fill="url(#drained)"/>`)
        else if (ch === '\u2592') out.push(`<rect x="${x.toFixed(1)}" y="${top}" width="${BAR_CELL + 0.3}" height="${h}" fill="url(#below)"/>`)
        else {
          // The edges and the even-share marker are thin rules.
          const at = ch === '\u2595' ? x + BAR_CELL - 1.5 : ch === '\u258f' ? x : x + BAR_CELL / 2 - 0.75
          out.push(`<rect x="${at.toFixed(1)}" y="${top - 1}" width="1.5" height="${h + 2}" fill="${color}"/>`)
        }
        x += BAR_CELL
      }
      return
    }
    const w = textWidth(text, size)
    if (text.trim())
      out.push(
        `<text x="${x.toFixed(1)}" y="${y}" textLength="${w.toFixed(1)}" lengthAdjust="spacingAndGlyphs" fill="${color}"${bold ? ' font-weight="600"' : ''} xml:space="preserve">${escape(text)}</text>`,
      )
    x += w
  }
  l.forEach((segment, i) => {
    if (i > 0) run(` ${DOT} `, SURFACE.dim)
    for (const span of segment) run(span.text, appTone(span.tone), span.bold)
  })
  return out
}

/** A run of UI text. */
const ui = (x: number, y: number, text: string, color: string, size = 15, extra = '') =>
  `<text x="${x}" y="${y}" fill="${color}" font-size="${size}"${extra}>${escape(text)}</text>`

function desktop() {
  const W = 1240
  const H = 800
  const PAD = 14
  const wx = PAD
  const wy = PAD
  const ww = W - PAD * 2
  const wh = H - PAD * 2
  const BAR = 46
  const split = wx + 880 // where the side panel starts
  const body: string[] = []

  body.push(
    '<defs>' +
      '<pattern id="drained" width="3" height="3" patternUnits="userSpaceOnUse"><rect width="3" height="3" fill="#3a3a3a"/><rect width="1.5" height="1.5" fill="#6a6a6a"/></pattern>' +
      `<pattern id="below" width="3" height="3" patternUnits="userSpaceOnUse"><rect width="3" height="3" fill="#3a2424"/><rect width="1.5" height="1.5" fill="${hex(MUTED_RED)}"/></pattern>` +
      '</defs>',
  )
  body.push(`<clipPath id="app"><rect x="${wx}" y="${wy}" width="${ww}" height="${wh}" rx="8"/></clipPath>`)
  body.push('<g clip-path="url(#app)">')
  body.push(`<rect x="${wx}" y="${wy}" width="${ww}" height="${wh}" fill="${APP.bg}"/>`)

  // The title bar: menu, sidebar, back and forward, the chat title, the
  // view buttons, and the window controls.
  body.push(`<rect x="${wx}" y="${wy}" width="${ww}" height="${BAR}" fill="${APP.bar}"/>`)
  body.push(`<line x1="${wx}" x2="${wx + ww}" y1="${wy + BAR + 0.5}" y2="${wy + BAR + 0.5}" stroke="${APP.border}"/>`)
  const cy = wy + BAR / 2
  const icon = `stroke="${APP.muted}" stroke-width="1.4" fill="none" stroke-linecap="round"`
  body.push(`<path d="M${wx + 22} ${cy - 6}H${wx + 40}M${wx + 22} ${cy}H${wx + 36}M${wx + 22} ${cy + 6}H${wx + 40}" ${icon}/>`)
  body.push(`<rect x="${wx + 58}" y="${cy - 8}" width="20" height="16" rx="3" ${icon}/><line x1="${wx + 65}" x2="${wx + 65}" y1="${cy - 8}" y2="${cy + 8}" ${icon}/>`)
  body.push(`<path d="M${wx + 106} ${cy - 6}L${wx + 100} ${cy}L${wx + 106} ${cy + 6}M${wx + 136} ${cy - 6}L${wx + 142} ${cy}L${wx + 136} ${cy + 6}" ${icon}/>`)
  body.push(ui(wx + 168, cy + 6, 'Add rate limiting', APP.fg, 16))
  body.push(`<path d="M${wx + 306} ${cy - 2}L${wx + 311} ${cy + 3}L${wx + 316} ${cy - 2}" ${icon}/>`)
  const vx = split - 150
  body.push(`<rect x="${vx - 18}" y="${cy - 16}" width="36" height="32" rx="6" fill="#2c2c2c"/>`)
  body.push(`<path d="M${vx - 7} ${cy - 6}H${vx - 5}M${vx - 1} ${cy - 6}H${vx + 9}M${vx - 7} ${cy}H${vx - 5}M${vx - 1} ${cy}H${vx + 9}M${vx - 7} ${cy + 6}H${vx - 5}M${vx - 1} ${cy + 6}H${vx + 9}" ${icon}/>`)
  body.push(`<path d="M${vx + 38} ${cy - 6}L${vx + 44} ${cy}L${vx + 38} ${cy + 6}M${vx + 47} ${cy + 7}H${vx + 55}" ${icon}/>`)
  body.push(`<rect x="${vx + 80}" y="${cy - 8}" width="16" height="16" rx="2" ${icon}/><path d="M${vx + 88} ${cy - 4}V${cy + 4}M${vx + 84} ${cy}H${vx + 92}" ${icon}/>`)
  body.push(`<circle cx="${vx + 132}" cy="${cy}" r="8" ${icon}/><path d="M${vx + 124} ${cy}H${vx + 140}M${vx + 132} ${cy - 8}C${vx + 127} ${cy - 3} ${vx + 127} ${cy + 3} ${vx + 132} ${cy + 8}C${vx + 137} ${cy + 3} ${vx + 137} ${cy - 3} ${vx + 132} ${cy - 8}" ${icon}/>`)
  body.push(`<path d="M${vx + 166} ${cy - 7}H${vx + 173}L${vx + 175} ${cy - 4}H${vx + 184}V${cy + 7}H${vx + 166}Z" ${icon}/>`)
  body.push(`<path d="M${vx + 206} ${cy}h0.1M${vx + 212} ${cy}h0.1M${vx + 218} ${cy}h0.1" stroke="${APP.muted}" stroke-width="2.4" stroke-linecap="round"/>`)
  body.push(...windowControls(wx + ww, cy, '#cfcfcf'))

  // The chat column.
  const cx0 = wx + 90
  const cx1 = split - 90
  let y = wy + BAR + 40
  const userText = ['Add rate limiting to the public endpoints, 100 requests', 'a minute per API key.']
  const bubbleW = 470
  body.push(`<rect x="${cx1 - bubbleW}" y="${y}" width="${bubbleW}" height="${22 + userText.length * 24}" rx="14" fill="${APP.card}"/>`)
  userText.forEach((t, i) => body.push(ui(cx1 - bubbleW + 22, y + 32 + i * 24, t, APP.fg, 16)))
  y += 22 + userText.length * 24 + 46

  const chevron = (x: number, yy: number) => `<path d="M${x} ${yy - 9}L${x + 4} ${yy - 5}L${x} ${yy - 1}" ${icon}/>`
  body.push(ui(cx0, y, 'Read 3 files, searched for 2 patterns', APP.muted, 15.5))
  body.push(chevron(cx0 + 262, y))
  y += 42
  ;[
    "I'll add a token-bucket limiter as middleware and apply it only to the public",
    'router, so internal routes and health checks stay unlimited.',
  ].forEach(t => {
    body.push(ui(cx0, y, t, APP.body, 16))
    y += 26
  })
  y += 16
  body.push(ui(cx0, y, 'Edited 2 files', APP.muted, 15.5))
  body.push(ui(cx0 + 104, y, '+42', '#4caf6a', 15.5))
  body.push(ui(cx0 + 134, y, '\u22121', '#e5534b', 15.5))
  body.push(chevron(cx0 + 160, y))
  y += 32
  ;[
    ['src/middleware/rateLimit.ts', 'the limiter, keyed on the API key header'],
    ['src/routes/public.ts', 'applies it to every public route'],
  ].forEach(([file, what]) => {
    body.push(`<circle cx="${cx0 + 8}" cy="${y - 5}" r="2.6" fill="${APP.body}"/>`)
    body.push(`<text x="${cx0 + 24}" y="${y}" font-size="16"><tspan fill="${APP.fg}" font-family="Cascadia Mono, Consolas, monospace" font-size="14.5">${escape(file)}</tspan><tspan fill="${APP.body}">: ${escape(what)}</tspan></text>`)
    y += 30
  })
  y += 18

  // Claude is working: the spinner and the running tool.
  body.push(`<circle cx="${cx0 + 2}" cy="${y - 10}" r="2" fill="${APP.claude}"/><circle cx="${cx0 + 7}" cy="${y - 5}" r="2" fill="${APP.claude}" fill-opacity="0.7"/><circle cx="${cx0 + 2}" cy="${y}" r="2" fill="${APP.claude}" fill-opacity="0.45"/>`)
  body.push(`<text x="${cx0 + 24}" y="${y}" font-size="15.5" fill="${APP.muted}">Running <tspan font-family="Cascadia Mono, Consolas, monospace" font-size="14">npm test -- rateLimit</tspan> ${DOT} 12s</text>`)

  // The bottom: paceline's band, the prompt box, and the model row.
  const modelY = wy + wh - 22
  const inputH = 62
  const inputY = modelY - 32 - inputH
  const bandH = 50
  const bandY = inputY - 10 - bandH
  const bx0 = wx + 48
  const bx1 = split - 48
  body.push(`<rect x="${bx0}" y="${bandY}" width="${bx1 - bx0}" height="${bandH}" rx="12" fill="${APP.card}"/>`)
  body.push(...appLine(line(67, 58, 38, { limits: { show: 'week', outAt: sec('2026-10-09T15:00:00Z') } }), bx0 + 16, bandY + 31, 16))

  body.push(`<rect x="${bx0}" y="${inputY}" width="${bx1 - bx0}" height="${inputH}" rx="16" fill="${APP.input}" stroke="#3a3a3a"/>`)
  const iy = inputY + inputH / 2
  body.push(`<path d="M${bx0 + 24} ${iy}H${bx0 + 42}M${bx0 + 33} ${iy - 9}V${iy + 9}" stroke="${APP.fg}" stroke-width="1.6" stroke-linecap="round"/>`)
  body.push(ui(bx0 + 64, iy + 6, 'Enter to send, Ctrl+Enter to queue for later', '#8a8a8a', 16.5))
  body.push(`<rect x="${bx1 - 98}" y="${iy - 8}" width="16" height="16" rx="3" fill="${APP.fg}"/>`)
  body.push(`<rect x="${bx1 - 42}" y="${iy - 12}" width="10" height="16" rx="5" stroke="${APP.fg}" stroke-width="1.6" fill="none"/><path d="M${bx1 - 44} ${iy - 2}A7 7 0 0 0 ${bx1 - 30} ${iy - 2}M${bx1 - 37} ${iy + 5}V${iy + 10}" stroke="${APP.fg}" stroke-width="1.6" fill="none" stroke-linecap="round"/>`)

  body.push(`<text x="${bx1 - 34}" y="${modelY}" text-anchor="end" font-size="15.5"><tspan fill="${APP.fg}">Opus 5.5</tspan><tspan fill="${APP.muted}"> High</tspan><tspan fill="${APP.fg}" dx="22">Auto</tspan></text>`)
  body.push(`<path d="M${bx1 - 11} ${modelY - 13}A8 8 0 1 1 ${bx1 - 19} ${modelY - 5}" stroke="#3b82f6" stroke-width="2.2" fill="none" stroke-linecap="round"/>`)

  // The side panel: the session's environment, context, and changes.
  body.push(`<rect x="${split}" y="${wy + BAR + 1}" width="${wx + ww - split}" height="${wh - BAR - 1}" fill="${APP.panel}"/>`)
  body.push(`<line x1="${split + 0.5}" x2="${split + 0.5}" y1="${wy + BAR + 1}" y2="${wy + wh}" stroke="${APP.border}"/>`)
  const sx = split + 24
  const sx1 = wx + ww - 24
  let sy = wy + BAR + 40
  body.push(ui(sx, sy, 'Environment', APP.fg, 15.5))
  sy += 44
  const rows: [(sy: number) => string, string][] = [
    [sy => `<rect x="${sx}" y="${sy - 13}" width="18" height="11" rx="2" ${icon}/><path d="M${sx - 3} ${sy + 1}H${sx + 21}" ${icon}/>`, 'api-server'],
    [sy => `<path d="M${sx} ${sy - 12}H${sx + 7}L${sx + 9} ${sy - 9}H${sx + 18}V${sy + 1}H${sx}Z" ${icon}/>`, 'api-server \u00b7 C:\\Users\\dev\\GitHub'],
    [sy => `<circle cx="${sx + 4}" cy="${sy - 11}" r="2.5" ${icon}/><circle cx="${sx + 4}" cy="${sy + 1}" r="2.5" ${icon}/><circle cx="${sx + 14}" cy="${sy - 7}" r="2.5" ${icon}/><path d="M${sx + 4} ${sy - 8}V${sy - 2}M${sx + 14} ${sy - 4}Q${sx + 14} ${sy - 1} ${sx + 6} ${sy}" ${icon}/>`, 'dev'],
  ]
  for (const [glyph, text] of rows) {
    body.push(glyph(sy))
    body.push(ui(sx + 36, sy, text, APP.fg, 15.5))
    sy += 46
  }
  body.push(`<rect x="${sx}" y="${sy - 13}" width="18" height="13" rx="2" ${icon}/><path d="M${sx + 5} ${sy + 3}H${sx + 13}" ${icon}/>`)
  body.push(ui(sx + 36, sy, 'Remote Control', APP.fg, 15.5))
  body.push(`<rect x="${sx1 - 40}" y="${sy - 16}" width="40" height="22" rx="11" fill="#2e2e2e" stroke="#444"/><circle cx="${sx1 - 29}" cy="${sy - 5}" r="7" fill="${APP.fg}"/>`)
  sy += 30
  body.push(`<line x1="${sx}" x2="${sx1}" y1="${sy}" y2="${sy}" stroke="${APP.border}"/>`)
  sy += 40
  body.push(ui(sx, sy, 'Context', APP.fg, 15.5))
  body.push(chevron(sx + 68, sy))
  body.push(ui(sx1 - 46, sy, '18%', APP.muted, 15.5, ' text-anchor="end"'))
  body.push(`<rect x="${sx1 - 34}" y="${sy - 10}" width="34" height="8" rx="4" fill="#2e2e2e"/><rect x="${sx1 - 34}" y="${sy - 10}" width="8" height="8" rx="4" fill="#3b82f6"/>`)
  sy += 30
  body.push(`<line x1="${sx}" x2="${sx1}" y1="${sy}" y2="${sy}" stroke="${APP.border}"/>`)
  sy += 40
  body.push(ui(sx, sy, 'Changes', APP.fg, 15.5))
  sy += 46
  body.push(`<rect x="${sx}" y="${sy - 15}" width="18" height="18" rx="3" ${icon}/><path d="M${sx + 9} ${sy - 11}V${sy - 3}M${sx + 5} ${sy - 7}H${sx + 13}M${sx + 5} ${sy}H${sx + 13}" ${icon}/>`)
  body.push(`<text x="${sx + 36}" y="${sy}" font-size="15"><tspan fill="#4caf6a">+42</tspan><tspan fill="#e5534b" dx="8">\u22121</tspan></text>`)
  body.push(`<rect x="${sx1 - 104}" y="${sy - 22}" width="104" height="32" rx="8" fill="#2c2c2c"/>`)
  body.push(ui(sx1 - 52, sy, 'Create PR', APP.fg, 15, ' text-anchor="middle"'))

  body.push('</g>')
  body.push(`<rect x="${wx + 0.5}" y="${wy + 0.5}" width="${ww - 1}" height="${wh - 1}" rx="8" fill="none" stroke="#3c3c3c"/>`)

  body.push(...callout(bx0 - 6, bandY - 6, bx1 - bx0 + 12, bandH + 12, 'paceline', 16))

  svgFile(
    'paceline-desktop.svg',
    'paceline in the Claude desktop app',
    'The Code tab of the Claude desktop app, with Claude partway through a task on a project called api-server. The paceline line sits in a band just above the prompt box: the project and branch, the model, effort, today\u2019s budget bar, the week at 67 of 100, and the warning out ~Fri because the week is forecast to run out before it resets.',
    W,
    H,
    'text{font-family:"Segoe UI",Helvetica,Arial,sans-serif;white-space:pre}',
    body,
  )
}

terminal()
desktop()
