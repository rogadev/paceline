/**
 * Builds paceline's line from a status payload.
 *
 * The line is a list of segments, each a list of colored spans, so the same
 * result can be drawn as elements in the band above the prompt (band.ts) or
 * as ANSI text (`toAnsi`). In the Go layout, the tests compare that text byte
 * for byte with what the Go paceline prints; the compact layout is the mod's
 * own.
 */

import { batchSegment, type BatchState } from './batch.ts'
import { headroomLevel, projectSlot, slotColor, type RGB } from './color.ts'
import type { Config } from './config.ts'
import { loopSegment, type LoopFile } from './loop.ts'
import { computeBudget, EVEN_PACE, type Budget, type Snapshot } from './pace.ts'
import type { Payload } from './payload.ts'
import {
  activeIndex,
  isPaused,
  isPlanned,
  isVisible,
  percent,
  settledCount,
  type Run,
  type Status,
} from './progress.ts'
import { clean } from './sanitize.ts'
import { clock, duration, floorInDay, startOfDay, weekday, type Zone } from './timefmt.ts'

const MIDDLE_DOT = '\u00b7'
const CELL_FULL = '\u25b0'
const CELL_EMPTY = '\u25b1'
const CHECK_MARK = '\u2713'
const ELLIPSIS = '\u2026'
const RIGHT_ARROW = '\u2192'
// Keeps a long run from pushing the rest of the line off-screen.
const MAX_CELLS = 20
// Today's bar: budget left (or overage), budget spent, the part of the daily
// base below a smaller budget, the base marker, and the bar's ends.
const BAR_FULL = '\u2588'
const BAR_DRAINED = '\u2591'
const BAR_BELOW = '\u2592'
const BAR_MARKER = '\u2502'
const BAR_LEFT = '\u2595'
const BAR_RIGHT = '\u258f'
// Twice the daily base, so a big carryover late in the week doesn't push the
// rest of the line off-screen.
const MAX_BAR_CELLS = 28
const MINUTE_MS = 60 * 1000
const HOUR_MS = 60 * MINUTE_MS
const DAY_MS = 24 * HOUR_MS
/**
 * A session's out time is shown to the quarter hour: a session always runs
 * out within five hours of now, so a finer time is worth showing.
 */
const SESSION_OUT_STEP_MS = 15 * MINUTE_MS
/** A week's out time is shown to the hour. */
const WEEK_OUT_STEP_MS = HOUR_MS

/** Windows Terminal's default red with 15% less chroma, easier on the eyes. */
export const MUTED_RED: RGB = [185, 47, 47]

/**
 * A darker, grayer MUTED_RED for the part of the daily base that earlier
 * overspending took from today's budget: where the day started, not a
 * warning.
 */
const FAINT_RED: RGB = [136, 66, 66]

/** How a span is colored. A named tone follows the terminal's own palette. */
export type Tone = 'plain' | 'cyan' | 'green' | 'yellow' | 'red' | 'dim' | RGB

export type Span = { text: string; tone: Tone; bold?: boolean }
export type Segment = Span[]
export type Line = Segment[]

/** Everything the renderer needs besides the payload. */
export type RenderContext = {
  nowMs: number
  zone: Zone
  config: Config
  readSnapshot?: () => Snapshot | null
  saveSnapshot?: (s: Snapshot) => void
  /** The git branch of the repository holding `dir`, or `''`. */
  branch?: (dir: string) => string
  /** The progress run of the repository holding `dir`, or null. */
  progress?: (dir: string) => Run | null
  /** The `/orc-loop` state file of the session's repository, or null. Only the compact layout draws it. */
  loop?: () => LoopFile | null
  /** Where the session's dev -> main batch stands in review, or null. Only the compact layout draws it. */
  batch?: () => BatchState | null
  /**
   * When set, the line uses the mod's compact layout: the project ahead of the
   * model, effort as the bare level, a limit's run-out time when the forecast
   * says it runs out before it resets, and the loop and batch segments before
   * the duration. Absent, the line is the Go paceline's, byte for byte.
   */
  compact?: boolean
  /**
   * Which usage limit the forecast picked. Only the compact layout reads it:
   * when the pick runs out before it resets, its segment says when ("out
   * ~Tue") in place of the week's days left or the session's reset time.
   */
  limits?: LimitPick
}

/** The usage limit the forecast picked, and when it is forecast to run out, if before it resets. */
export type LimitPick = {
  show: 'session' | 'week'
  /** Epoch seconds when the picked limit is forecast to run out. Absent when it lasts until it resets. */
  outAt?: number
}

/** The pieces of the line. Each layout draws them in its own order. */
type Part =
  | 'project'
  | 'model'
  | 'effort'
  | 'fastMode'
  | 'session'
  | 'today'
  | 'week'
  | 'context'
  | 'cache'
  | 'progress'
  | 'loop'
  | 'batch'
  | 'duration'

const GO_ORDER: readonly Part[] = [
  'model',
  'effort',
  'fastMode',
  'project',
  'session',
  'today',
  'week',
  'context',
  'cache',
  'progress',
  'duration',
]

const COMPACT_ORDER: readonly Part[] = [
  'project',
  'model',
  'effort',
  'fastMode',
  'session',
  'today',
  'week',
  'context',
  'cache',
  'progress',
  'loop',
  'batch',
  'duration',
]

const span = (text: string, tone: Tone = 'plain'): Span => ({ text, tone })
const spaced = (pieces: Span[][]) => pieces.flatMap((piece, i) => (i === 0 ? piece : [span(' '), ...piece]))
const present = (segment: Segment | null): Segment[] => (segment ? [segment] : [])

/** Rounds halves up, as the Go paceline (and its JavaScript 1.0) does. */
export const round = (v: number) => Math.floor(v + 0.5)

// Go's \s is ASCII whitespace only; JavaScript's would also match Unicode spaces.
const MODEL_SUFFIX = /[\t\n\f\r ]*\(.*\)$/

/** Gets the last path segment, for either separator style. */
function leaf(p: string): string {
  const s = p.replace(/[\\/]+$/, '').replaceAll('\\', '/')
  if (s === '') return '.'
  return s.slice(s.lastIndexOf('/') + 1)
}

function workDir(p: Payload): string {
  return p.workspace?.currentDir || p.cwd || ''
}

/** Picks the short label, or the long one when the config is verbose. */
function label(ctx: RenderContext, short: string, long: string): string {
  return ctx.config.verbose ? long : short
}

/** Colors `text` in the headroom tone for `left` percent left. */
function headroom(ctx: RenderContext, left: number, text: string): Span {
  const th = ctx.config.thresholds
  return span(text, headroomLevel(left, th.headroomGreen, th.headroomYellow))
}

/** Returns the line for `p`, in the Go layout unless `ctx.compact` is set. */
export function render(p: Payload, ctx: RenderContext): Line {
  const on = ctx.config.segments
  const pick = ctx.compact ? ctx.limits : undefined
  const sd = p.rateLimits?.sevenDay
  const r = (on.today || on.week) && sd ? budgetFor(sd, ctx) : null
  const parts: Record<Part, Segment[]> = {
    project: on.project ? present(project(p, ctx)) : [],
    model: on.model ? present(model(p)) : [],
    effort: on.effort ? present(effort(p, ctx)) : [],
    fastMode: on.fastMode && p.fastMode === true ? [[span('fast mode', 'yellow')]] : [],
    session: on.session ? session(p, ctx, runOut(pick, 'session', ctx)) : [],
    today: on.today && r ? [today(r, ctx)] : [],
    week: on.week && sd?.usedPercentage !== undefined ? week(sd.usedPercentage, r, ctx, runOut(pick, 'week', ctx)) : [],
    context: on.context ? present(contextWarning(p, ctx)) : [],
    cache: on.cache ? present(cacheWarning(p, ctx)) : [],
    progress: on.progress ? present(progress(p, ctx)) : [],
    loop: on.loop ? present(loop(ctx)) : [],
    batch: on.batch ? present(batch(ctx)) : [],
    duration: on.duration ? present(elapsed(p)) : [],
  }
  return (ctx.compact ? COMPACT_ORDER : GO_ORDER).flatMap(part => parts[part])
}

// Folder, colored by project. The color comes from the project root, so a
// subfolder keeps its project's color.
function project(p: Payload, ctx: RenderContext): Segment | null {
  const dir = workDir(p)
  if (dir === '') return null
  const projectDir = p.workspace?.projectDir || dir
  const slot = projectSlot(leaf(projectDir), ctx.config.projectSlots)
  const segment: Segment = [span(clean(leaf(dir), 64), slotColor(slot))]
  if (ctx.config.segments.branch && ctx.branch) {
    const branch = clean(ctx.branch(dir), 40)
    if (branch !== '') segment.push(span(' '), span('(', 'dim'), span(branch), span(')', 'dim'))
  }
  return segment
}

// The model, minus any "(1M context)"-style suffix.
function model(p: Payload): Segment | null {
  if (!p.model) return null
  const name = clean(p.model.displayName, 64).replace(MODEL_SUFFIX, '')
  return name === '' ? null : [span(name, 'cyan')]
}

// Effort, only above the everyday levels. The compact layout shows the level
// alone ("high") unless the config is verbose.
function effort(p: Payload, ctx: RenderContext): Segment | null {
  if (!p.effort) return null
  const level = clean(p.effort.level, 16)
  if (level === '' || ctx.config.quietEfforts.includes(level)) return null
  const text = ctx.compact ? label(ctx, level, level + ' effort') : level + ' effort'
  return [span(text, 'dim')]
}

// The five-hour limit. It counts up what's used from 0%, or down what's left
// from 100% when its countDirection is "down". Color always follows what's
// left, so either way the segment turns red near the limit. A reading past
// 100% shows 100% used or 0% left, never more or negative. When the session
// runs out before it resets, `out` follows in place of the reset time.
function session(p: Payload, ctx: RenderContext, out: Span | null): Segment[] {
  const fh = p.rateLimits?.fiveHour
  if (fh?.usedPercentage === undefined) return []
  const used = round(fh.usedPercentage)
  const left = Math.max(0, 100 - used)
  const shown = ctx.config.countDown.session
    ? `${left}%${label(ctx, '', ' left')}`
    : `${Math.min(Math.max(used, 0), 100)}%${label(ctx, '', ' used')}`
  const name = label(ctx, 's', 'session')
  if (out) return [[headroom(ctx, left, `${name} ${shown}`)], [out]]
  const when =
    left < ctx.config.thresholds.headroomGreen && fh.resetsAt !== undefined
      ? ` (resets ${clock(fh.resetsAt, ctx.nowMs, ctx.zone)})`
      : ''
  return [[headroom(ctx, left, `${name} ${shown}${when}`)]]
}

// When the forecast says `limit` runs out before it resets, when: the weekday
// alone ("out ~Tue") in yellow while that is more than a day away, then the
// time ("out ~9pm") in red. A session is always within five hours, so it
// always shows the time. The time rounds down, to the hour for the week and
// the quarter hour for the session, so it never promises more time than the
// forecast gives, unless rounding would land before now: then it shows the
// exact minute instead of a time already passed. The "~" marks an estimate,
// as on today's last-day "out ~4pm".
function runOut(pick: LimitPick | undefined, limit: LimitPick['show'], ctx: RenderContext): Span | null {
  if (pick?.show !== limit || pick.outAt === undefined) return null
  const outMs = pick.outAt * 1000
  const isSession = limit === 'session'
  if (!isSession && outMs - ctx.nowMs > DAY_MS) return span(`out ~${weekday(outMs, ctx.zone)}`, 'yellow')
  const step = isSession ? SESSION_OUT_STEP_MS : WEEK_OUT_STEP_MS
  const rounded = floorInDay(outMs, step, startOfDay(outMs, ctx.zone))
  const at = rounded < ctx.nowMs ? outMs : rounded
  return span(`out ~${clock(at / 1000, ctx.nowMs, ctx.zone)}`, 'red')
}

// Today's budget, from the weekly limit, or null without a reset time or once
// it has passed. Reading it may re-anchor the day, so the snapshot is saved
// here, for a week-only line too: the week needs the day count, and computing
// it anchors the day.
function budgetFor(sd: { usedPercentage?: number; resetsAt?: number }, ctx: RenderContext): Budget | null {
  if (sd.usedPercentage === undefined || sd.resetsAt === undefined) return null
  const r = computeBudget(sd.usedPercentage, sd.resetsAt, ctx.nowMs, ctx.readSnapshot?.() ?? null, ctx.zone)
  if (r?.snapshotChanged) ctx.saveSnapshot?.(r.snapshot)
  return r
}

/** The even daily budget in whole weekly points, where today's bar puts its marker. */
const DAILY_BASE = round(EVEN_PACE)

// Today as "Today: <bar> 7/12": the bar, then the weekly points spent of
// today's budget. Counting down, the numbers show what's left ("5/12 left")
// or, past the budget, how far over ("3 over/12"). A budget above the even
// daily base adds what's left of that surplus ("+4 saved"). Over budget,
// tomorrow's budget follows ("-> 11 tmrw"), unless the week has nothing left
// for it or it is the last day, when the reset brings a fresh week. On the
// last day, the time the week runs out at today's pace follows instead, when
// that comes before the reset.
function today(r: Budget, ctx: RenderContext): Segment {
  const budget = round(r.budget)
  // A reading just past the budget can round to it; never show 12/12 in red.
  const spent = r.over ? Math.max(round(r.spent), budget + 1) : round(r.spent)
  const left = budget - spent
  const text = !ctx.config.countDown.today
    ? `${spent}/${budget}`
    : r.over
      ? `${-left} over/${budget}`
      : `${left}/${budget} left`
  const pieces: Span[][] = [
    [span('Today:')],
    todayBar(budget, spent),
    [r.over ? span(text, 'red') : headroom(ctx, round(r.pctLeft), text)],
  ]
  const tomorrow = round(r.tomorrow)
  if (left > DAILY_BASE) pieces.push([span(`+${left - DAILY_BASE} saved`, 'green')])
  else if (r.over && tomorrow > 0) pieces.push([span(`${RIGHT_ARROW} ${tomorrow} tmrw`, 'red')])
  // computeBudget sets runsOutAt only on the last day.
  if (r.runsOutAt > 0) pieces.push([span('out ~' + clock(r.runsOutAt, ctx.nowMs, ctx.zone), 'red')])
  return spaced(pieces)
}

// Today's budget at one cell per weekly point, against a marker at the even
// daily base. Full cells on the left are the budget left, drained cells the
// rest of the base, and a faint red stretch the part of the base below a
// smaller budget. A budget above the base starts with full cells past the
// marker, and spending removes those first, so the bar shortens. Over budget,
// the same full cells turn red and count the overage instead. A bar longer
// than MAX_BAR_CELLS scales down to fit, marker included.
function todayBar(budget: number, spent: number): Span[] {
  const over = spent > budget
  const full = over ? spent - budget : budget - spent
  const length = Math.max(DAILY_BASE, full)
  const scale = Math.min(1, MAX_BAR_CELLS / length)
  const cellCount = round(length * scale)
  const marker = round(DAILY_BASE * scale)

  // Classifies a cell by the weekly point at its middle.
  const kind = (i: number) => {
    const point = (i + 0.5) / scale
    if (point < full) return BAR_FULL
    if (point < Math.min(budget, DAILY_BASE)) return BAR_DRAINED
    return BAR_BELOW
  }
  const tone = (k: string): Tone =>
    k === BAR_FULL ? (over ? 'red' : 'cyan') : k === BAR_BELOW ? FAINT_RED : 'dim'

  const out: Span[] = [span(BAR_LEFT, 'dim')]
  // Neighbors of the same kind share one span; the marker splits a run.
  for (let i = 0; i < cellCount; ) {
    let j = i + 1
    while (j < cellCount && j !== marker && kind(j) === kind(i)) j++
    out.push(span(kind(i).repeat(j - i), tone(kind(i))))
    if (j === marker) out.push(span(BAR_MARKER))
    i = j
  }
  // When nothing extends past the base, the marker closes the bar; an end cap
  // right after it would read as a leftover sliver of surplus.
  if (cellCount > marker) out.push(span(BAR_RIGHT, 'dim'))
  return out
}

// The week as "w 58/100", then the days left until the reset, or `out` when
// the week runs out before it resets. From three days out the week figure
// turns bold: late in the week a single day absorbs all the slack or debt, so
// today's budget can swing, and the week is the steadier reference.
function week(usedPct: number, r: Budget | null, ctx: RenderContext, out: Span | null): Segment[] {
  const used = Math.min(Math.max(round(usedPct), 0), 100)
  const left = 100 - used
  const name = label(ctx, 'w', 'week')
  const figure = headroom(ctx, left, ctx.config.countDown.week ? `${name} ${left}/100 left` : `${name} ${used}/100`)
  if (r !== null && round(r.daysLeft) <= 3) figure.bold = true
  if (out) return [[figure], [out]]
  if (r === null) return [[figure]]
  return [[figure], [span(r.lastDay ? 'last day' : `${round(r.daysLeft)}d left`, 'dim')]]
}

// Context-window warning, silent while there is headroom.
function contextWarning(p: Payload, ctx: RenderContext): Segment | null {
  const ctxUsed = p.contextWindow?.usedPercentage
  if (ctxUsed === undefined) return null
  const th = ctx.config.thresholds
  const used = round(ctxUsed)
  if (used >= th.contextCritical) return [span(`ctx ${used}%`, 'red')]
  if (used >= th.contextWarn) return [span(`ctx ${used}%`, 'yellow')]
  return null
}

// Prompt-cache warning, only once the cache has gone cold.
function cacheWarning(p: Payload, ctx: RenderContext): Segment | null {
  const pc = p.promptCache
  if (pc?.expiresAt === undefined || pc.expiresAt > Math.floor(ctx.nowMs / 1000)) return null
  const text =
    pc.recacheTokensIfCold !== undefined
      ? `cache cold: ${round(pc.recacheTokensIfCold / 1000)}k @ ~2x`
      : 'cache cold: ~2x'
  return [span(text, 'red')]
}

function progress(p: Payload, ctx: RenderContext): Segment | null {
  const dir = workDir(p)
  if (dir === '' || !ctx.progress) return null
  const run = ctx.progress(dir)
  return run ? progressSegment(run, ctx.nowMs) : null
}

function loop(ctx: RenderContext): Segment | null {
  const file = ctx.loop?.()
  return file ? loopSegment(file.state, file.mtimeMs, ctx.nowMs) : null
}

function batch(ctx: RenderContext): Segment | null {
  const state = ctx.batch?.()
  return state ? batchSegment(state) : null
}

// Duration comes last in both layouts, so it sits at the right edge.
function elapsed(p: Payload): Segment | null {
  const ms = p.cost?.totalDurationMs
  return ms !== undefined && ms > 0 ? [span(duration(ms), 'dim')] : null
}

/**
 * Draws a run as "name cells status": one cell per step, colored by its
 * status, then the active step and how far the run has come.
 */
function progressSegment(r: Run, nowMs: number): Segment | null {
  if (!isVisible(r, nowMs)) return null
  const parts: Span[][] = [[span(clean(r.name, 16))], cells(r)]
  if (r.phase === 'halted') parts.push([span('halted', 'red')])
  else if (r.phase === 'done') parts.push([span(CHECK_MARK + ' done', 'green')])
  else {
    const status = [span(progressStatus(r))]
    if (isPaused(r, nowMs)) status.push(span(' (paused)', 'dim'))
    parts.push(status)
  }
  return spaced(parts.filter(part => part.some(s => s.text !== '')))
}

const CELL_TONE: Record<Status, Tone> = {
  done: 'green',
  active: 'cyan',
  skipped: 'dim',
  blocked: 'red',
  pending: 'dim',
}

// Up to MAX_CELLS cells. A longer run shows a window around the active (or
// first pending) step, with an ellipsis on each side that was cut.
function cells(r: Run): Span[] {
  const steps = r.steps
  let start = 0
  let end = steps.length
  if (steps.length > MAX_CELLS) {
    let focus = activeIndex(r)
    if (focus < 0) focus = steps.findIndex(s => s.status === 'pending')
    if (focus < 0) focus = steps.length - 1
    start = Math.min(Math.max(focus - Math.trunc(MAX_CELLS / 2), 0), steps.length - MAX_CELLS)
    end = start + MAX_CELLS
  }
  const out: Span[] = []
  if (start > 0) out.push(span(ELLIPSIS, 'dim'))
  // Neighbors with the same status share one span.
  for (let i = start; i < end; ) {
    let j = i + 1
    while (j < end && steps[j].status === steps[i].status) j++
    const glyph = steps[i].status === 'pending' ? CELL_EMPTY : CELL_FULL
    out.push(span(glyph.repeat(j - i), CELL_TONE[steps[i].status]))
    i = j
  }
  if (end < steps.length) out.push(span(ELLIPSIS, 'dim'))
  return out
}

// The writer's label, or the active step and its stage. A planned run adds
// its weighted percentage, which moves within a step; any other run counts
// settled steps instead, unless the writer set a label of its own.
function progressStatus(r: Run): string {
  let text = clean(r.label, 24)
  if (text === '') {
    const i = activeIndex(r)
    if (i >= 0) {
      const step = r.steps[i]
      text = (clean(step.label, 24) + ' ' + clean(step.stage, 16)).trim()
    }
  }
  // Never claim 100% while work remains.
  if (isPlanned(r)) text += ` ${Math.min(Math.trunc(percent(r)), 99)}%`
  else if (r.label === '') text += ` ${settledCount(r)}/${r.steps.length}`
  return text.trim()
}

const ANSI: Record<string, string> = { green: '32', yellow: '33', cyan: '36', dim: '2' }

function sgr(tone: Tone): string {
  if (tone === 'plain') return ''
  const rgb = tone === 'red' ? MUTED_RED : typeof tone === 'string' ? null : tone
  return rgb ? `38;2;${rgb[0]};${rgb[1]};${rgb[2]}` : ANSI[tone as string]
}

/**
 * Renders the line as ANSI text, segments joined by a dim middle dot, exactly
 * as the Go paceline prints it. With `noColor`, the same text with no escapes.
 */
export function toAnsi(line: Line, noColor = false): string {
  const wrap = (code: string, text: string) => (noColor || code === '' ? text : `\x1b[${code}m${text}\x1b[0m`)
  // Bold sits inside the color, as the Go paceline nests them.
  const draw = (s: Span) => wrap(sgr(s.tone), s.bold ? wrap('1', s.text) : s.text)
  const separator = ' ' + draw(span(MIDDLE_DOT, 'dim')) + ' '
  return line.map(segment => segment.map(draw).join('')).join(separator)
}
