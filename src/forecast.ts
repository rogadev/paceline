/**
 * Forecasts which usage limit runs out first, the five-hour session or the
 * week. The compact line turns the pick into a warning only when it runs out
 * before it resets; otherwise it shows neither limit.
 *
 * The model:
 *
 * - Activity: each refresh marks the local hour of the day in which usage
 *   went up. Over the recorded days that gives, per hour of the day, the
 *   chance the user is working then: a 9-to-5 user is near 1 during work
 *   hours and near 0 at night, an agent running around the clock is near 1
 *   everywhere. With no history yet, every hour is an even chance.
 * - Burn rate: percentage points per active hour. The week uses today's
 *   average (points spent today over today's active hours), falling back to
 *   the week's average early in the day. The session uses its own window.
 * - Projection: walk forward from now to the limit's reset, adding the burn
 *   rate times the chance of working in each stretch, and note when the
 *   limit would reach 100%.
 *
 * Every function here is pure: the activity record lives in the mod's store,
 * and hooks/register.ts reads and writes it.
 */

import { dateKey, startOfDay, type Zone } from './timefmt.ts'
import type { Limit } from './payload.ts'
import type { LimitPick } from './render.ts'

const MINUTE = 60 * 1000
const HOUR = 60 * MINUTE
const SESSION_MS = 5 * HOUR
const WEEK_MS = 7 * 24 * HOUR
const STEP_MS = 15 * MINUTE
/** How many recorded days the activity profile keeps. */
const KEEP_DAYS = 14
/** Usage that rose this recently means the user is working now. */
const ACTIVE_NOW_MS = 20 * MINUTE
/** A rate needs at least this many active hours behind it to be trusted. */
const MIN_RATE_HOURS = 2
/** Neither limit runs out: switch only when the other one leads by this many points. */
const SWITCH_MARGIN = 5
/** Pseudo-days of evidence each way for every hour, before any history. */
const PRIOR = 0.25
/** With no history, a week's use is spread over at least this many hours. */
const MIN_FALLBACK_HOURS = 3

/** A day with this many active hours counts in full toward the burn rate. */
const RATE_FULL_HOURS = 4
/** The burn rate needs at least this much day weight behind it. */
const MIN_RATE_WEIGHT = 0.25
/** More weekly points than a day can spend; a day on record above it is corrupt. */
const MAX_DAY_SPENT = 100
/** Two readings whose reset times differ by less than this share a window. */
const SAME_WINDOW_MS = 10 * MINUTE

/**
 * Which hours of a day had readings, and which saw usage go up, as 24-bit
 * masks, plus the weekly points spent that day.
 */
export type DayActivity = { seen: number; active: number; spent?: number }

/** What the mod remembers between refreshes, in its store. */
export type Activity = {
  /** Keyed by `yyyymmdd` in local time. */
  days: Record<string, DayActivity>
  /**
   * The highest reading of each limit in its current window, to tell when
   * usage went up, with the reset time that names the window.
   */
  last?: {
    session?: number
    sessionResetsAt?: number
    week?: number
    weekResetsAt?: number
    changedAtMs?: number
  }
}

const bit = (hour: number) => 1 << hour

function hourOf(ms: number, zone: Zone): number {
  const d = new Date(ms)
  return zone === 'utc' ? d.getUTCHours() : d.getHours()
}

/** Reports whether `a` is a well-formed activity record from the store. */
export function isActivity(a: unknown): a is Activity {
  if (typeof a !== 'object' || a === null) return false
  const days = (a as Activity).days
  if (typeof days !== 'object' || days === null) return false
  return Object.values(days).every(
    d => typeof d === 'object' && d !== null && Number.isInteger(d.seen) && Number.isInteger(d.active),
  )
}

/**
 * Carries a record from builds before 0.4.1 over: the hours seen and active,
 * without the last readings or any day's points spent above what a day can
 * hold, since those builds counted an idle session's older reading as use.
 */
export function migrateActivity(old: unknown): Activity | null {
  if (!isActivity(old)) return null
  const days: Record<string, DayActivity> = {}
  for (const [key, d] of Object.entries(old.days)) {
    days[key] =
      d.spent !== undefined && d.spent <= MAX_DAY_SPENT
        ? { seen: d.seen, active: d.active, spent: d.spent }
        : { seen: d.seen, active: d.active }
  }
  return { days }
}

/**
 * Compares one limit's reading with the highest one on record. Every session
 * on the machine records into the same store, and a session that has been
 * idle still holds an older, lower reading, so a reading below the record
 * counts only when its reset time says a new window began. Readings without a
 * reset time fall back to taking any drop as a reset.
 */
function compare(
  used: number | undefined,
  resetsAt: number | undefined,
  highest: number | undefined,
  highestResetsAt: number | undefined,
): { gained: number; highest?: number; resetsAt?: number } {
  if (used === undefined) return { gained: 0, highest, resetsAt: highestResetsAt }
  if (highest === undefined) return { gained: 0, highest: used, resetsAt }
  if (resetsAt !== undefined && highestResetsAt !== undefined) {
    const shift = (resetsAt - highestResetsAt) * 1000
    // A window that ended already: a stale reading.
    if (shift <= -SAME_WINDOW_MS) return { gained: 0, highest, resetsAt: highestResetsAt }
    // A new window starts its record over.
    if (shift >= SAME_WINDOW_MS) return { gained: 0, highest: used, resetsAt }
    return { gained: Math.max(0, used - highest), highest: Math.max(used, highest), resetsAt: highestResetsAt }
  }
  return used < highest ? { gained: 0, highest: used, resetsAt } : { gained: used - highest, highest: used, resetsAt }
}

/**
 * Records one reading: marks this hour as seen, and as active when either
 * limit's usage went up since the last reading. Returns the new record, and
 * whether it changed, so the store is written only when it must be.
 */
export function recordActivity(
  prev: Activity | null,
  nowMs: number,
  zone: Zone,
  session: Limit | undefined,
  week: Limit | undefined,
): { activity: Activity; changed: boolean } {
  const days: Record<string, DayActivity> = { ...(prev?.days ?? {}) }
  const key = dateKey(nowMs, zone)
  const h = bit(hourOf(nowMs, zone))
  const last = prev?.last
  const s = compare(session?.usedPercentage, session?.resetsAt, last?.session, last?.sessionResetsAt)
  const w = compare(week?.usedPercentage, week?.resetsAt, last?.week, last?.weekResetsAt)
  const rose = s.gained > 0 || w.gained > 0

  const day = days[key] ?? { seen: 0, active: 0 }
  const next: DayActivity = { seen: day.seen | h, active: rose ? day.active | h : day.active }
  // A total from before stale readings were ignored can't be trusted; start the day's count over.
  const spent = day.spent !== undefined && day.spent <= MAX_DAY_SPENT ? day.spent : undefined
  if (spent !== undefined || w.gained > 0) next.spent = (spent ?? 0) + w.gained
  days[key] = next

  // Keep only the newest days.
  const keys = Object.keys(days).sort()
  for (const old of keys.slice(0, Math.max(0, keys.length - KEEP_DAYS))) delete days[old]

  const changedAtMs = rose ? nowMs : last?.changedAtMs
  const activity: Activity = {
    days,
    last: {
      session: s.highest,
      sessionResetsAt: s.resetsAt,
      week: w.highest,
      weekResetsAt: w.resetsAt,
      changedAtMs,
    },
  }
  const changed =
    !prev ||
    next.seen !== day.seen ||
    next.active !== day.active ||
    next.spent !== day.spent ||
    s.highest !== last?.session ||
    s.resetsAt !== last?.sessionResetsAt ||
    w.highest !== last?.week ||
    w.resetsAt !== last?.weekResetsAt ||
    keys.length > KEEP_DAYS
  return { activity, changed }
}

/**
 * Estimates the chance of working in each hour of the day, from the completed
 * days on record (today is still in progress). A light prior (a quarter of a
 * day each way) keeps a new install at an even chance and never rules an hour
 * out, while a week of steady use moves an hour close to 0 or 1.
 */
export function hourProfile(a: Activity | null, nowMs: number, zone: Zone): number[] {
  const today = dateKey(nowMs, zone)
  const past = Object.entries(a?.days ?? {}).filter(([key]) => key < today)
  return Array.from({ length: 24 }, (_, hour) => {
    const active = past.filter(([, d]) => (d.active & bit(hour)) !== 0).length
    return (active + PRIOR) / (past.length + 2 * PRIOR)
  })
}

/** Counts the whole hours on record between two times that saw usage rise. */
function activeHoursBetween(a: Activity | null, fromMs: number, toMs: number, zone: Zone): number {
  let hours = 0
  // Step hour by hour from the hour that holds fromMs.
  for (let t = fromMs - (fromMs % HOUR); t < toMs; t += HOUR) {
    const day = a?.days[dateKey(t, zone)]
    if (day && (day.active & bit(hourOf(t, zone))) !== 0) hours++
  }
  return hours
}

const countBits = (mask: number) => {
  let n = 0
  for (let m = mask >>> 0; m !== 0; m &= m - 1) n++
  return n
}

/**
 * Computes the points per active hour, averaged over the recorded days with
 * the newest counting most: a day's weight halves for each day back, and a
 * day with few active hours counts for less. One hard day followed by a
 * relaxed one then pulls the rate down instead of projecting the hard day
 * forward. Returns undefined until a day on record has both points spent and
 * active hours.
 */
function recentRate(
  a: Activity | null,
  nowMs: number,
  zone: Zone,
  spentToday: number | undefined,
): number | undefined {
  const today = dateKey(nowMs, zone)
  let weighted = 0
  let weight = 0
  for (const [key, d] of Object.entries(a?.days ?? {})) {
    const spent = key === today && spentToday !== undefined ? spentToday : d.spent
    const hours = countBits(d.active)
    if (spent === undefined || spent <= 0 || spent > MAX_DAY_SPENT || hours === 0) continue
    const ageDays = Math.max(0, Math.round((startOfDay(nowMs, zone) - dayStartOf(key, zone)) / (24 * HOUR)))
    const w = 0.5 ** ageDays * Math.min(hours, RATE_FULL_HOURS) / RATE_FULL_HOURS
    weighted += (spent / hours) * w
    weight += w
  }
  return weight >= MIN_RATE_WEIGHT ? weighted / weight : undefined
}

/** Returns the epoch ms of the start of the day a `yyyymmdd` key names. */
function dayStartOf(key: string, zone: Zone): number {
  const y = Number(key.slice(0, 4))
  const m = Number(key.slice(4, 6)) - 1
  const d = Number(key.slice(6, 8))
  return zone === 'utc' ? Date.UTC(y, m, d) : new Date(y, m, d).getTime()
}

export type Projection = {
  /** Epoch seconds when the limit reaches 100%, or undefined if not before its reset. */
  outAt?: number
  /** The usage forecast at the reset, uncapped. */
  atReset: number
}

/** Where the week's burn rate came from, in the order the forecast tries them. */
export type WeekRateSource = 'recent-days' | 'today' | 'week-average' | 'fallback'

/** Every figure `forecastLimits` worked from, for the debug tool. */
export type ForecastTrace = {
  /** The chance of working in each local hour, 0 to 23, to two decimals. */
  profile: number[]
  workingNow: boolean
  /** Absent when no five-hour limit was reported. */
  session?: { used: number; resetsAt: number; activeHours: number; rate: number; projection: Projection }
  week: {
    used: number
    resetsAt: number
    rateSource: WeekRateSource
    rate: number
    /** Points spent today after scaling to the hours the mod saw, if the day snapshot is known. */
    spentToday?: number
    /** Share of today's elapsed hours the mod has readings for, 0 to 1. */
    coverage: number
    todayActiveHours: number
    weekActiveHours: number
    projection: Projection
  }
}

/**
 * Walks from now to the reset in short steps, burning `rate` points per hour
 * in proportion to the chance of working in each step.
 */
function project(
  used: number,
  rate: number,
  nowMs: number,
  resetMs: number,
  chance: (ms: number) => number,
): Projection {
  let total = used
  let outAt: number | undefined
  for (let t = nowMs; t < resetMs; t += STEP_MS) {
    const stepHours = Math.min(STEP_MS, resetMs - t) / HOUR
    const burn = rate * chance(t) * stepHours
    if (outAt === undefined && total < 100 && total + burn >= 100) {
      const fraction = burn > 0 ? (100 - total) / burn : 0
      outAt = Math.floor((t + fraction * stepHours * HOUR) / 1000)
    }
    total += burn
  }
  if (used >= 100) outAt = Math.floor(nowMs / 1000)
  return { outAt, atReset: total }
}

/** What `forecastLimits` needs. */
export type ForecastInput = {
  nowMs: number
  zone: Zone
  session?: Limit
  week?: Limit
  activity: Activity | null
  /** Weekly usage at the start of today, from the day snapshot, if known. */
  weekAtDayStart?: number
  /** The previous pick, so a near tie doesn't flip the line back and forth. */
  previous?: LimitPick['show']
}

/**
 * Picks the limit worth showing: the one forecast to run out first; if
 * neither runs out before it resets, the one forecast to end its window
 * closer to 100%. With no five-hour limit reported (a plan without one, such
 * as a verified Ultra account), it forecasts the week alone. Returns null
 * when the week isn't reported.
 */
export function forecastLimits(input: ForecastInput): LimitPick | null {
  return explainForecast(input)?.pick ?? null
}

/** Picks a limit like `forecastLimits` and returns the figures behind the pick too. */
export function explainForecast(input: ForecastInput): { pick: LimitPick; trace: ForecastTrace } | null {
  const { nowMs, zone, activity } = input
  const s = input.session
  const w = input.week
  const hasS = s?.usedPercentage !== undefined && s.resetsAt !== undefined
  const hasW = w?.usedPercentage !== undefined && w.resetsAt !== undefined
  if (!hasW) return null

  const profile = hourProfile(activity, nowMs, zone)
  const workingNow =
    activity?.last?.changedAtMs !== undefined && nowMs - activity.last.changedAtMs <= ACTIVE_NOW_MS
  // Right now, recent use says more than the profile does.
  const chance = (ms: number) => (workingNow && ms - nowMs < HOUR ? 1 : profile[hourOf(ms, zone)])

  // The week: today's average when today has enough behind it, else the week's.
  const weekReset = w.resetsAt! * 1000
  const weekStart = weekReset - WEEK_MS
  const weekUsed = w.usedPercentage!
  const todayStart = startOfDay(nowMs, zone)
  const todayActive = activeHoursBetween(activity, todayStart, nowMs, zone)
  // The day snapshot counts everything spent today, but the active hours only
  // cover the time the mod was running. Scale the points to that coverage so a
  // fresh install doesn't divide a whole day's use by an hour or two.
  const seenToday = countBits(activity?.days[dateKey(nowMs, zone)]?.seen ?? 0)
  const coverage = Math.min(1, seenToday / Math.max((nowMs - todayStart) / HOUR, 1))
  const spentToday =
    input.weekAtDayStart === undefined ? undefined : (weekUsed - input.weekAtDayStart) * coverage
  const weekActive = activeHoursBetween(activity, Math.max(weekStart, nowMs - WEEK_MS), nowMs, zone)
  const recent = recentRate(activity, nowMs, zone, spentToday)
  let weekRate: number
  let rateSource: WeekRateSource
  if (recent !== undefined) {
    weekRate = recent
    rateSource = 'recent-days'
  } else if (spentToday !== undefined && spentToday > 0 && todayActive >= MIN_RATE_HOURS) {
    weekRate = spentToday / todayActive
    rateSource = 'today'
  } else if (weekActive >= MIN_RATE_HOURS) {
    weekRate = weekUsed / weekActive
    rateSource = 'week-average'
  } else {
    rateSource = 'fallback'
    // No usable history: spread the week's use over its elapsed hours at an even chance.
    weekRate = weekUsed / Math.max(((nowMs - weekStart) / HOUR) * 0.5, MIN_FALLBACK_HOURS)
  }

  const wp = project(weekUsed, weekRate, nowMs, weekReset, chance)

  const round = (n: number) => Math.round(n * 100) / 100
  const trace: ForecastTrace = {
    profile: profile.map(round),
    workingNow,
    week: {
      used: weekUsed,
      resetsAt: w.resetsAt!,
      rateSource,
      rate: round(weekRate),
      spentToday: spentToday === undefined ? undefined : round(spentToday),
      coverage: round(coverage),
      todayActiveHours: todayActive,
      weekActiveHours: weekActive,
      projection: wp,
    },
  }

  if (!hasS) return { pick: wp.outAt === undefined ? { show: 'week' } : { show: 'week', outAt: wp.outAt }, trace }

  // The session: points per active hour since its window opened.
  const sessionReset = s.resetsAt! * 1000
  const sessionStart = sessionReset - SESSION_MS
  const sessionUsed = s.usedPercentage!
  const sessionActive = activeHoursBetween(activity, sessionStart, nowMs, zone)
  const sessionElapsed = Math.max((nowMs - sessionStart) / HOUR, 0.25)
  const sessionRate = sessionUsed / Math.max(sessionActive || sessionElapsed, 0.5)
  const sp = project(sessionUsed, sessionRate, nowMs, sessionReset, chance)
  trace.session = {
    used: sessionUsed,
    resetsAt: s.resetsAt!,
    activeHours: sessionActive,
    rate: round(sessionRate),
    projection: sp,
  }

  if (sp.outAt !== undefined || wp.outAt !== undefined) {
    const sessionFirst = wp.outAt === undefined || (sp.outAt !== undefined && sp.outAt <= wp.outAt)
    return { pick: sessionFirst ? { show: 'session', outAt: sp.outAt } : { show: 'week', outAt: wp.outAt }, trace }
  }
  const lead: LimitPick['show'] = sp.atReset >= wp.atReset ? 'session' : 'week'
  if (input.previous && input.previous !== lead && Math.abs(sp.atReset - wp.atReset) < SWITCH_MARGIN) {
    return { pick: { show: input.previous }, trace }
  }
  return { pick: { show: lead }, trace }
}
