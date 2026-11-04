/**
 * The answers to the `get_usage` and `get_today_budget` tools, ported from the
 * Go paceline-mcp. The Go server read a feed file the status line wrote; the
 * mod answers from the same figures the band draws from (the payload's rate
 * limits and the day snapshot `chooseSnapshot` picks), so the tools and the
 * band agree. The reading is live, so every answer is zero seconds old.
 */

import { computeBudget, type Budget, type Direction, type Snapshot } from './pace.ts'
import type { Limit, Payload } from './payload.ts'
import { round } from './render.ts'
import { clock, rfc3339, type Zone } from './timefmt.ts'

/** A tool's answer: the summary for the reader and the fields for the JSON line. */
export type ToolAnswer<F> = { summary: string; fields: F }

// The Go answers say how old the feed's reading is. The mod's reading is
// live, so it is never old, and the fields stay for callers that read them.
type Live = { ageSeconds: 0; stale: false }

const LIVE: Live = { ageSeconds: 0, stale: false }

// One limit as get_usage reports it, rounded the way the band shows it. The
// field order is the Go answer's, which JSON.stringify keeps.
type WindowAnswer = {
  usedPct: number
  /** Never below 0, though `usedPct` can pass 100. */
  leftPct: number
  resetsAt: string
  resetsClock: string
  resetPassed: boolean
}

type UsageFields = { session: WindowAnswer | null; week: WindowAnswer | null } & Live

type BudgetFields = {
  kind: 'budget'
  /** Weekly percentage points available today. */
  budgetPct: number
  /** Weekly percentage points spent today. */
  spentPct: number
  /** Percent of today's budget used. */
  usedPct: number
  /** Percent of today's budget left. */
  leftPct: number
  over: boolean
  pace: Direction
  weekResetsAt: string
  weekResetsClock: string
} & Live

type TodayBudgetFields = BudgetFields | ({ kind: 'none' } & Live)

// A limit with both of its figures.
type Reading = { usedPercentage: number; resetsAt: number }

// Today's budget compared to an even share of the week. "Up" means the budget
// is generous, not that spending is fast.
const PACE_PHRASES: Record<Direction, string> = {
  up: 'larger than an even share of the week (100%/7 a day)',
  down: 'smaller than an even share of the week (100%/7 a day)',
  even: 'about an even share of the week (100%/7 a day)',
}

/**
 * Builds `get_usage`'s answer: how much of the five-hour session and the week
 * is used and left, and when each resets. A window with no reading is `null`
 * in the fields, as on an account without a five-hour limit.
 */
export function usageAnswer(rateLimits: Payload['rateLimits'], nowMs: number, zone: Zone): ToolAnswer<UsageFields> {
  const session = windowFor(rateLimits?.fiveHour, nowMs, zone)
  const week = windowFor(rateLimits?.sevenDay, nowMs, zone)
  return {
    summary: windowSummary('Session', session) + ' ' + windowSummary('Week', week),
    fields: { session, week, ...LIVE },
  }
}

/**
 * Builds `get_today_budget`'s answer from the weekly limit and today's
 * snapshot, with the same math as the band's today segment.
 *
 * It only reads: when the snapshot is stale or missing, the answer re-anchors
 * today at current usage but returns nothing to save, so a tool call never
 * fixes today's starting point. `snapshot` is not modified.
 */
export function todayBudgetAnswer(
  week: Limit | undefined,
  snapshot: Snapshot | null,
  nowMs: number,
  zone: Zone,
): ToolAnswer<TodayBudgetFields> {
  const reading = readingOf(week)
  if (reading === null) {
    return noBudget("There is no weekly reading from Claude Code, so today's budget is unknown.")
  }
  const resetsClock = clock(reading.resetsAt, nowMs, zone)
  // computeBudget is null only for a reset at or before today's midnight,
  // which has passed too, so one branch answers both.
  const passed = reading.resetsAt <= nowSeconds(nowMs)
  const r = passed ? null : computeBudget(reading.usedPercentage, reading.resetsAt, nowMs, snapshot, zone)
  if (r === null) {
    return noBudget(
      `The weekly limit reset at ${resetsClock}, so this reading is out of date and today's budget is unknown until Claude Code reports new usage.`,
    )
  }
  const fields: BudgetFields = {
    kind: 'budget',
    budgetPct: round(r.budget),
    spentPct: round(r.spent),
    // Over budget, a reading just past the budget rounds to 100, which would
    // contradict `over`; the band shows at least 101 too.
    usedPct: r.over ? Math.max(round(r.pctUsed), 101) : round(r.pctUsed),
    // Rounded from what's left rather than taken from the rounded used
    // figure, so the tool and the band agree at a half-point tie.
    leftPct: r.budget > 0 && !r.over ? round(r.pctLeft) : 0,
    over: r.over,
    pace: r.pace,
    weekResetsAt: rfc3339(reading.resetsAt, zone),
    weekResetsClock: resetsClock,
    ...LIVE,
  }
  return { summary: budgetSummary(fields, r), fields }
}

/** Joins an answer's summary and its fields as one JSON line, the text paceline-mcp answers with. */
export function answerText(answer: ToolAnswer<unknown>): string {
  return answer.summary + '\n' + JSON.stringify(answer.fields)
}

const nowSeconds = (nowMs: number) => Math.floor(nowMs / 1000)

// A limit missing either figure, such as a reset time that didn't parse, is
// no reading, as the band treats it.
function readingOf(limit: Limit | undefined): Reading | null {
  if (limit?.usedPercentage === undefined || limit.resetsAt === undefined) return null
  return { usedPercentage: limit.usedPercentage, resetsAt: limit.resetsAt }
}

function windowFor(limit: Limit | undefined, nowMs: number, zone: Zone): WindowAnswer | null {
  const reading = readingOf(limit)
  if (reading === null) return null
  const used = round(reading.usedPercentage)
  return {
    usedPct: used,
    leftPct: Math.max(0, 100 - used),
    resetsAt: rfc3339(reading.resetsAt, zone),
    resetsClock: clock(reading.resetsAt, nowMs, zone),
    resetPassed: reading.resetsAt <= nowSeconds(nowMs),
  }
}

function windowSummary(name: string, w: WindowAnswer | null): string {
  if (w === null) return `${name}: no reading from Claude Code.`
  if (w.resetPassed) {
    return `${name}: reset at ${w.resetsClock}, so this reading (${w.usedPct}% used) is out of date until Claude Code reports new usage.`
  }
  return `${name}: ${w.leftPct}% left (${w.usedPct}% used), resets ${w.resetsClock}.`
}

function noBudget(summary: string): ToolAnswer<TodayBudgetFields> {
  return { summary, fields: { kind: 'none', ...LIVE } }
}

function budgetSummary(a: BudgetFields, r: Budget): string {
  if (r.budget <= 0) {
    return `No budget today: the weekly limit was used up at the start of the day; the week resets ${a.weekResetsClock}.`
  }
  const detail =
    ` Today's budget is ${a.budgetPct}% of the week, and you have spent ${a.spentPct}% of the week today.` +
    ` That budget is ${PACE_PHRASES[a.pace]}; the week resets ${a.weekResetsClock}.`
  if (a.over) return `Over today's budget: ${a.usedPct}% used.` + detail
  return `${a.leftPct}% of today's budget left (${a.usedPct}% used).` + detail
}
