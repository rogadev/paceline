/**
 * Turns what a session reports through the mods API into the line.
 *
 * The Go paceline reads the status JSON Claude Code pipes to it; a mod asks
 * the session instead. These functions turn those answers into the same
 * payload shape, so render.ts runs unchanged against the parity fixtures and
 * live. The calls on `$` themselves live in hooks/register.ts, because the
 * engine only follows `$` within the hooks module.
 */

import type { SessionRateLimit, SessionUsage } from 'claude-code'

import type { BatchState } from './batch.ts'
import type { Config } from './config.ts'
import { explainForecast, type Activity, type ForecastTrace } from './forecast.ts'
import type { LoopFile } from './loop.ts'
import { modelName } from './model.ts'
import { isValidSnapshot, type Snapshot } from './pace.ts'
import type { Limit, Payload } from './payload.ts'
import type { Run } from './progress.ts'
import { render, type Line, type LimitPick } from './render.ts'

/** Where this mod keeps today's budget snapshot in its `$.store`. */
export const SNAPSHOT_KEY = 'day'
/**
 * Where this mod keeps its record of active hours, for the forecast. Builds
 * before 0.4.1 used `activity` and counted an idle session's older reading
 * as use; they may still be running, so this build leaves that key to them
 * and keeps its own.
 */
export const ACTIVITY_KEY = 'activity-v2'
/** The key builds before 0.4.1 wrote, read once to carry the hour history over. */
export const LEGACY_ACTIVITY_KEY = 'activity'

/** What the session reported for one refresh. */
export type Figures = {
  usage: SessionUsage
  modelId: string
  cwd: string
  root: string
  sessionId: string
  nowMs: number
  settings: Readonly<Record<string, unknown>>
  /** The effort level of the latest turn, from a classic hook event. */
  effort?: string
  /** The session's live effort level, from the CLAUDE_EFFORT variable Claude Code sets. */
  envEffort?: string
}

/** Gets Claude Code's config directory, honoring CLAUDE_CONFIG_DIR. */
export function configDirFrom(env: {
  claudeConfigDir?: string
  userProfile?: string
  home?: string
}): string {
  const slash = (p: string) => p.replaceAll('\\', '/').replace(/\/+$/, '')
  if (env.claudeConfigDir) return slash(env.claudeConfigDir)
  // USERPROFILE first: on Windows, HOME can hold a Git Bash path like /c/Users/me.
  const home = env.userProfile || env.home
  return home ? `${slash(home)}/.claude` : '.claude'
}

/** Converts one rate-limit window to the payload's shape: epoch seconds for the reset. */
export function toLimit(r: SessionRateLimit | undefined): Limit | undefined {
  if (!r) return undefined
  const resetsMs = r.resetsAt === undefined ? NaN : Date.parse(r.resetsAt)
  return {
    usedPercentage: r.percentUsed,
    resetsAt: Number.isFinite(resetsMs) ? Math.floor(resetsMs / 1000) : undefined,
  }
}

/**
 * Returns the effort level the band should draw after a classic hook event:
 * the event's level, or `cached` when the event has none.
 *
 * An event that carries `agent_id` fired inside a subagent, whose effort is its
 * own and not the main thread's, so it never changes the level.
 */
export function turnEffort(
  cached: string | undefined,
  event: { effort?: { level?: string }; agent_id?: string },
): string | undefined {
  if (event.agent_id) return cached
  return event.effort?.level || cached
}

/**
 * Builds the status payload from a session's figures. The effort level comes
 * from CLAUDE_EFFORT first: Claude Code keeps it current, `/effort` included.
 * A hook event's level is next, since it lags a `/effort` change, then the
 * settings, which hold the default for new sessions rather than this one's.
 */
export function toPayload(f: Figures): Payload {
  const perModel = (f.settings.modelSettings as Record<string, { effortLevel?: unknown } | undefined> | undefined)?.[
    f.modelId
  ]?.effortLevel
  const configured = typeof perModel === 'string' ? perModel : f.settings.effortLevel
  const effort = f.envEffort?.trim() || f.effort || (typeof configured === 'string' ? configured : '')
  return {
    model: { displayName: modelName(f.modelId) },
    effort: effort ? { level: effort } : undefined,
    fastMode: f.settings.fastMode === true,
    cwd: f.cwd,
    sessionId: f.sessionId,
    workspace: { currentDir: f.cwd, projectDir: f.root },
    rateLimits: {
      fiveHour: toLimit(f.usage.rateLimits.find(r => r.kind === 'five_hour')),
      sevenDay: toLimit(f.usage.rateLimits.find(r => r.kind === 'seven_day')),
    },
    contextWindow: { usedPercentage: f.usage.context.percent },
    cost: { totalDurationMs: f.nowMs - f.usage.startedAt },
  }
}

function parseSnapshot(text: string | null): Snapshot | null {
  if (text === null) return null
  try {
    const value: unknown = JSON.parse(text)
    return isValidSnapshot(value) ? value : null
  } catch {
    return null
  }
}

/**
 * Picks today's snapshot. When the Go paceline has already anchored this same
 * day and week in paceline-day.json (`goText`), its anchor wins, so both lines
 * show the same budget. Otherwise the mod's own snapshot from `$.store`
 * (`stored`). The mod never writes the Go file.
 */
export function chooseSnapshot(
  goText: string | null,
  stored: unknown,
  today: string,
  week: Limit | undefined,
): Snapshot | null {
  const fromGo = parseSnapshot(goText)
  if (
    fromGo !== null &&
    fromGo.date === today &&
    fromGo.resetsAt === week?.resetsAt &&
    (week.usedPercentage ?? -1) >= fromGo.usedAtStart
  ) {
    return fromGo
  }
  return isValidSnapshot(stored) ? stored : null
}

/** Everything a live render reads besides the payload and config. */
export type LiveState = {
  snapshot: Snapshot | null
  branch: string
  run: Run | null
  /** The repository's `/orc-loop` state file, or null. */
  loop: LoopFile | null
  /** The repository's dev -> main review batch, or null when the segment doesn't apply or isn't known yet. */
  batch: BatchState | null
  activity: Activity | null
  nowMs: number
  /** The limit the forecast picked last time, so a near tie doesn't flip it. */
  previous?: LimitPick['show']
}

/**
 * Renders the line in the compact layout and returns it with the limit the
 * forecast picked and the snapshot to save when today's anchor changed.
 * Unless the config asks for both limits, the line warns only when the picked
 * limit is forecast to run out before it resets.
 */
export function renderLive(
  payload: Payload,
  config: Config,
  state: LiveState,
): { line: Line; pick: LimitPick | null; trace: ForecastTrace | null; save?: Snapshot } {
  const forecast =
    config.limits === 'auto'
      ? explainForecast({
          nowMs: state.nowMs,
          zone: 'local',
          session: payload.rateLimits?.fiveHour,
          week: payload.rateLimits?.sevenDay,
          activity: state.activity,
          weekAtDayStart: state.snapshot?.usedAtStart,
          previous: state.previous,
        })
      : null
  const pick = forecast?.pick ?? null
  const changed: { save?: Snapshot } = {}
  const line = render(payload, {
    nowMs: state.nowMs,
    zone: 'local',
    config,
    readSnapshot: () => state.snapshot,
    saveSnapshot: s => {
      changed.save = s
    },
    branch: () => state.branch,
    progress: () => state.run,
    loop: () => state.loop,
    batch: () => state.batch,
    compact: true,
    limits: pick ?? undefined,
  })
  return { line, pick, trace: forecast?.trace ?? null, ...changed }
}

/** What `debugReport` needs besides the render's own result. */
export type DebugInput = {
  nowMs: number
  payload: Payload
  config: Config
  state: LiveState
  /** Which source the day snapshot came from. */
  snapshotSource: 'go-file' | 'store' | 'none'
  result: { pick: LimitPick | null; trace: ForecastTrace | null }
}

/**
 * Gathers everything behind the line's forecast as plain data, for the debug
 * tool: the readings, the day snapshot and where it came from, the stored
 * activity, and the forecast's own figures.
 */
export function debugReport(d: DebugInput) {
  const iso = (sec: number | undefined) => (sec === undefined ? undefined : new Date(sec * 1000).toISOString())
  const limit = (l: Limit | undefined) =>
    l && { usedPercentage: l.usedPercentage, resetsAt: iso(l.resetsAt), resetsAtEpoch: l.resetsAt }
  const pick = d.result.pick
  return {
    now: new Date(d.nowMs).toISOString(),
    utcOffsetMinutes: -new Date(d.nowMs).getTimezoneOffset(),
    limitsMode: d.config.limits,
    readings: {
      session: limit(d.payload.rateLimits?.fiveHour),
      week: limit(d.payload.rateLimits?.sevenDay),
    },
    daySnapshot: { source: d.snapshotSource, value: d.state.snapshot },
    activity: d.state.activity,
    previousPick: d.state.previous,
    pick: pick && { ...pick, outAtIso: iso(pick.outAt) },
    forecast: d.result.trace,
    note: d.result.trace
      ? undefined
      : 'No forecast: the config limits mode is not auto, or the week reading is missing.',
  }
}
