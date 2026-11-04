import { describe, expect, test } from 'claude-code/testing'

import type { BatchState } from '../src/batch.ts'
import { defaultConfig, type Config } from '../src/config.ts'
import { decodeLoopState, type LoopFile } from '../src/loop.ts'
import type { Snapshot } from '../src/pace.ts'
import { decodePayload } from '../src/payload.ts'
import type { Run } from '../src/progress.ts'
import { render, toAnsi, type LimitPick, type RenderContext } from '../src/render.ts'

const DOT = ' \u00b7 '
const FULL = '\u25b0'
const EMPTY = '\u25b1'
const BAR = '\u2588'
const sec = (iso: string) => Date.parse(iso) / 1000

// Wednesday noon. The week resets two days from midnight and stood at 40%
// when the day began, so today's budget is 30 points of the week, 16 above
// the daily base of 14.
const NOW = Date.parse('2026-10-07T12:00:00Z')
const WEEK_RESET = sec('2026-10-09T00:00:00Z')
const SNAPSHOT: Snapshot = { date: '20261007', resetsAt: WEEK_RESET, usedAtStart: 40 }

// At the default 46% of the week, 6 points are spent: 24 left, 10 of them
// past the base marker.
const TODAY = 'Today: \u2595' + BAR.repeat(14) + '\u2502' + BAR.repeat(10) + '\u258f 6/30 +10 saved'
const WEEK = ['w 46/100', '2d left']
const HEAD = ['paceline (dev)', 'Opus 5.5', 'high']

type Figures = {
  week?: number
  session?: number
  effort?: string
  fast?: boolean
  context?: number
  cacheCold?: boolean
}

function payload(f: Figures = {}) {
  return decodePayload({
    model: { display_name: 'Opus 5.5' },
    effort: { level: f.effort ?? 'high' },
    fast_mode: f.fast ?? false,
    workspace: { current_dir: '/r/paceline', project_dir: '/r/paceline' },
    rate_limits: {
      five_hour: { used_percentage: f.session ?? 4, resets_at: sec('2026-10-07T14:30:00Z') },
      seven_day: { used_percentage: f.week ?? 46, resets_at: WEEK_RESET },
    },
    context_window: { used_percentage: f.context ?? 30 },
    prompt_cache: f.cacheCold ? { expires_at: sec('2026-10-07T11:00:00Z') } : undefined,
    cost: { total_duration_ms: 59 * 60_000 },
  })!
}

function compact(
  f: Figures,
  config: Config = defaultConfig(),
  limits?: LimitPick,
  more: Partial<RenderContext> = {},
) {
  return render(payload(f), {
    nowMs: NOW,
    zone: 'utc',
    config,
    readSnapshot: () => SNAPSHOT,
    branch: () => 'dev',
    compact: true,
    limits,
    ...more,
  })
}

const text = (line: ReturnType<typeof compact>) => toAnsi(line, true)

/** A default config with the session segment on. */
function withSession(): Config {
  const c = defaultConfig()
  c.segments.session = true
  return c
}

// Where the week's days left land: after the project, model, effort, today,
// and the week figure.
const DAYS = 5

describe('compact layout', () => {
  test('project, model, and effort lead, then today and the week', () => {
    const quiet: LimitPick = { show: 'week' }
    expect(text(compact({}, defaultConfig(), quiet))).toBe([...HEAD, TODAY, ...WEEK, '59m'].join(DOT))
    expect(text(compact({ fast: true, context: 80, cacheCold: true }, defaultConfig(), quiet))).toBe(
      [...HEAD, 'fast mode', TODAY, ...WEEK, 'ctx 80%', 'cache cold: ~2x', '59m'].join(DOT),
    )
  })

  test('today and the week are the Go segments', () => {
    const go = render(payload(), { nowMs: NOW, zone: 'utc', config: defaultConfig(), readSnapshot: () => SNAPSHOT })
    const line = compact({}, defaultConfig(), { show: 'week' })
    expect(line.slice(3, 6)).toEqual(go.slice(3, 6))
    // Two days out, the week figure is bold.
    expect(line[4]).toEqual([{ text: 'w 46/100', tone: 'green', bold: true }])
  })

  test('effort is the bare level, dim, and verbose spells it out', () => {
    const line = compact({}, defaultConfig(), { show: 'week' })
    expect(line[2]).toEqual([{ text: 'high', tone: 'dim' }])
    const verbose = defaultConfig()
    verbose.verbose = true
    expect(compact({}, verbose, { show: 'week' })[2]).toEqual([{ text: 'high effort', tone: 'dim' }])
    // An everyday level stays quiet, as in the Go layout.
    expect(text(compact({ effort: 'medium' }, defaultConfig(), { show: 'week' }))).toBe(
      ['paceline (dev)', 'Opus 5.5', TODAY, ...WEEK, '59m'].join(DOT),
    )
  })

  test('a week that runs out early says when in place of the days left', () => {
    const out = (outAt: string, config = defaultConfig()) =>
      compact({}, config, { show: 'week', outAt: sec(outAt) })[DAYS]
    // More than a day away: the weekday alone, in yellow.
    expect(out('2026-10-08T21:40:00Z')).toEqual([{ text: 'out ~Thu', tone: 'yellow' }])
    expect(out('2026-10-08T12:30:00Z')).toEqual([{ text: 'out ~Thu', tone: 'yellow' }])
    // Exactly a day away is within a day: the time, rounded down to the hour, in red.
    expect(out('2026-10-08T12:00:00Z')).toEqual([{ text: 'out ~Thu 12pm', tone: 'red' }])
    expect(out('2026-10-08T09:59:00Z')).toEqual([{ text: 'out ~Thu 9am', tone: 'red' }])
    expect(out('2026-10-07T18:20:00Z')).toEqual([{ text: 'out ~6pm', tone: 'red' }])
    const verbose = defaultConfig()
    verbose.verbose = true
    expect(out('2026-10-08T21:40:00Z', verbose)).toEqual([{ text: 'out ~Thu', tone: 'yellow' }])
    expect(text(compact({}, defaultConfig(), { show: 'week', outAt: sec('2026-10-08T21:40:00Z') }))).toBe(
      [...HEAD, TODAY, 'w 46/100', 'out ~Thu', '59m'].join(DOT),
    )
  })

  describe('a run-out on the last day is colored by how late it falls', () => {
    // Thursday, the week's last day: the week resets at Friday midnight.
    const out = (now: string, outAt: string) =>
      compact({}, defaultConfig(), { show: 'week', outAt: sec(outAt) }, { nowMs: Date.parse(now) })[DAYS]

    test('the week warning: red before 5pm, yellow to 8pm, green after', () => {
      const now = '2026-10-08T06:00:00Z'
      expect(out(now, '2026-10-08T16:59:00Z')).toEqual([{ text: 'out ~4pm', tone: 'red' }])
      expect(out(now, '2026-10-08T17:00:00Z')).toEqual([{ text: 'out ~5pm', tone: 'yellow' }])
      expect(out(now, '2026-10-08T19:59:00Z')).toEqual([{ text: 'out ~7pm', tone: 'yellow' }])
      expect(out(now, '2026-10-08T20:00:00Z')).toEqual([{ text: 'out ~8pm', tone: 'green' }])
      expect(out(now, '2026-10-08T23:30:00Z')).toEqual([{ text: 'out ~11pm', tone: 'green' }])
    })

    test('the week warning counts the reset day, seen from the day before', () => {
      expect(out('2026-10-07T22:00:00Z', '2026-10-08T21:00:00Z')).toEqual([{ text: 'out ~Thu 9pm', tone: 'green' }])
    })

    test('a run-out before the last day stays red, however late', () => {
      expect(out('2026-10-07T12:00:00Z', '2026-10-07T21:00:00Z')).toEqual([{ text: 'out ~9pm', tone: 'red' }])
    })

    // Today's own out time, from its pace since midnight: at noon with 80% used
    // at the start of the day, a reading of `used` runs the week out this many
    // seconds from now.
    const lastNoon = Date.parse('2026-10-08T12:00:00Z')
    const lastDaySnapshot: Snapshot = {
      date: '20261008',
      resetsAt: WEEK_RESET,
      usedAtStart: 80,
      startedAt: sec('2026-10-08T00:00:00Z'),
    }
    const usedFor = (outAt: string) => {
      const ahead = sec(outAt) - lastNoon / 1000
      return 80 + (20 * 43200) / (43200 + ahead)
    }
    const todayOut = (outAt: string, compactLayout = true) => {
      const line = render(payload({ week: usedFor(outAt) }), {
        nowMs: lastNoon,
        zone: 'utc',
        config: defaultConfig(),
        readSnapshot: () => lastDaySnapshot,
        compact: compactLayout,
      })
      return line.flat().find(s => s.text.startsWith('out ~'))
    }

    test('the today segment: red before 5pm, yellow to 8pm, green after', () => {
      expect(todayOut('2026-10-08T16:59:00Z')).toEqual({ text: 'out ~4:59pm', tone: 'red' })
      expect(todayOut('2026-10-08T17:00:00Z')).toEqual({ text: 'out ~5pm', tone: 'yellow' })
      expect(todayOut('2026-10-08T19:59:00Z')).toEqual({ text: 'out ~7:59pm', tone: 'yellow' })
      expect(todayOut('2026-10-08T20:00:00Z')).toEqual({ text: 'out ~8pm', tone: 'green' })
    })

    test("the Go layout keeps today's out time red", () => {
      expect(todayOut('2026-10-08T20:00:00Z', false)).toEqual({ text: 'out ~8pm', tone: 'red' })
    })
  })

  test('a session that runs out says when in place of its reset time', () => {
    const line = (outAt: string) => compact({ session: 80 }, withSession(), { show: 'session', outAt: sec(outAt) })
    expect(line('2026-10-07T13:59:00Z').slice(3, 5)).toEqual([
      [{ text: 's 80%', tone: 'yellow' }],
      [{ text: 'out ~1:45pm', tone: 'red' }],
    ])
    expect(line('2026-10-07T13:00:00Z')[4]).toEqual([{ text: 'out ~1pm', tone: 'red' }])
    expect(line('2026-10-07T12:14:59Z')[4]).toEqual([{ text: 'out ~12pm', tone: 'red' }])
    // The week keeps its days left.
    expect(text(line('2026-10-07T13:59:00Z'))).toBe(
      [...HEAD, 's 80%', 'out ~1:45pm', TODAY, ...WEEK, '59m'].join(DOT),
    )
  })

  test('an out time inside the current step shows the exact minute, not a time already passed', () => {
    const outAt = (now: string, pick: LimitPick) =>
      compact({ session: 80 }, withSession(), pick, { nowMs: Date.parse(now) }).find(s => s[0].text.startsWith('out ~'))
    expect(outAt('2026-10-07T13:52:00Z', { show: 'session', outAt: sec('2026-10-07T13:58:00Z') })).toEqual([
      { text: 'out ~1:58pm', tone: 'red' },
    ])
    expect(outAt('2026-10-07T12:20:00Z', { show: 'week', outAt: sec('2026-10-07T12:50:00Z') })).toEqual([
      { text: 'out ~12:50pm', tone: 'red' },
    ])
    // Rounding down to exactly now is not in the past.
    expect(outAt('2026-10-07T13:45:00Z', { show: 'session', outAt: sec('2026-10-07T13:58:00Z') })).toEqual([
      { text: 'out ~1:45pm', tone: 'red' },
    ])
  })

  test('progress sits between cache and duration', () => {
    const step = (id: string, status: 'done' | 'active' | 'pending') => ({
      id,
      label: '#' + id,
      status,
      weight: 0,
      stages: [],
      stage: '',
    })
    const run: Run = {
      name: 'loop',
      phase: 'running',
      label: '',
      steps: [step('1', 'done'), step('2', 'active'), step('3', 'pending')],
      updatedAtMs: NOW,
    }
    const line = compact({ context: 80, cacheCold: true }, defaultConfig(), { show: 'week' }, { progress: () => run })
    expect(text(line)).toBe(
      [...HEAD, TODAY, ...WEEK, 'ctx 80%', 'cache cold: ~2x', 'loop ' + FULL + FULL + EMPTY + ' #2 1/3', '59m'].join(
        DOT,
      ),
    )
  })

  describe('loop and batch', () => {
    const loopFile: LoopFile = {
      state: decodeLoopState(
        JSON.stringify({ phase: 'build', inFlight: 44, issues: [{ n: 43, status: 'done' }, { n: 44, status: 'pending' }] }),
      )!,
      mtimeMs: NOW - 60_000,
    }
    const batchState: BatchState = { commits: 3, issues: 7, reviews: [], sinceLastReview: 0 }
    const LOOP = 'loop ' + FULL + FULL + ' #44 1/2'
    const BATCH = '7 issues' + DOT + 'no review yet'
    const inputs: Partial<RenderContext> = { loop: () => loopFile, batch: () => batchState }

    test('sit after the other segments and before the duration, loop first', () => {
      expect(text(compact({}, defaultConfig(), undefined, inputs))).toBe(
        [...HEAD, TODAY, ...WEEK, LOOP, BATCH, '59m'].join(DOT),
      )
    })

    test('each turns off with its own toggle', () => {
      const noLoop = defaultConfig()
      noLoop.segments.loop = false
      expect(text(compact({}, noLoop, undefined, inputs))).toBe([...HEAD, TODAY, ...WEEK, BATCH, '59m'].join(DOT))
      const noBatch = defaultConfig()
      noBatch.segments.batch = false
      expect(text(compact({}, noBatch, undefined, inputs))).toBe([...HEAD, TODAY, ...WEEK, LOOP, '59m'].join(DOT))
    })

    test('draw nothing when there is no loop or batch', () => {
      const none: Partial<RenderContext> = { loop: () => null, batch: () => null }
      expect(text(compact({}, defaultConfig(), undefined, none))).toBe([...HEAD, TODAY, ...WEEK, '59m'].join(DOT))
    })

    test('the Go layout never draws them', () => {
      const ctx = { nowMs: NOW, zone: 'utc' as const, config: defaultConfig(), readSnapshot: () => SNAPSHOT }
      expect(toAnsi(render(payload(), { ...ctx, ...inputs }))).toBe(toAnsi(render(payload(), ctx)))
    })
  })

  test('the Go layout ignores a pick', () => {
    const ctx = { nowMs: NOW, zone: 'utc' as const, config: defaultConfig(), readSnapshot: () => SNAPSHOT }
    const pick: LimitPick = { show: 'week', outAt: sec('2026-10-08T21:40:00Z') }
    expect(toAnsi(render(payload(), { ...ctx, limits: pick }))).toBe(toAnsi(render(payload(), ctx)))
  })

  test('a pick without an out time changes nothing', () => {
    expect(text(compact({ session: 80 }, withSession(), { show: 'session' }))).toBe(
      [...HEAD, 's 80% (resets 2:30pm)', TODAY, ...WEEK, '59m'].join(DOT),
    )
    expect(text(compact({}, defaultConfig(), { show: 'week' }))).toBe([...HEAD, TODAY, ...WEEK, '59m'].join(DOT))
  })

  test('a limit turned off in the config never says when it runs out', () => {
    const config = defaultConfig()
    config.segments.week = false
    const line = compact({}, config, { show: 'week', outAt: sec('2026-10-08T21:40:00Z') })
    expect(text(line)).toBe([...HEAD, TODAY, '59m'].join(DOT))
    // The session is off by default, so it never warns unless turned on.
    expect(text(compact({ session: 80 }, defaultConfig(), { show: 'session', outAt: sec('2026-10-07T13:59:00Z') }))).toBe(
      [...HEAD, TODAY, ...WEEK, '59m'].join(DOT),
    )
  })

  test('without a pick, a session turned on shows before today, as in the Go layout', () => {
    expect(text(compact({}))).toBe([...HEAD, TODAY, ...WEEK, '59m'].join(DOT))
    expect(text(compact({}, withSession()))).toBe([...HEAD, 's 4%', TODAY, ...WEEK, '59m'].join(DOT))
    // The session's reset time shows once its headroom runs low.
    expect(compact({ session: 80 }, withSession())[3]).toEqual([{ text: 's 80% (resets 2:30pm)', tone: 'yellow' }])
  })

  test('with no rate limits there is no bar and no warning', () => {
    const p = payload()
    p.rateLimits = undefined
    const line = render(p, { nowMs: NOW, zone: 'utc', config: defaultConfig(), branch: () => 'dev', compact: true })
    expect(text(line)).toBe([...HEAD, '59m'].join(DOT))
  })
})
