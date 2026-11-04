import type { SessionUsage } from 'claude-code'
import { describe, expect, test } from 'claude-code/testing'

import { defaultConfig } from '../src/config.ts'
import { toPayload } from '../src/live.ts'
import type { Snapshot } from '../src/pace.ts'
import type { Limit } from '../src/payload.ts'
import { render, toAnsi } from '../src/render.ts'
import { clock, dateKey, rfc3339 } from '../src/timefmt.ts'
import { answerText, todayBudgetAnswer, usageAnswer } from '../src/usage.ts'

// The Go tests' clock: Tue 2026-10-06 12pm UTC. The week resets Thu 9pm, 2.875
// days after today's midnight, and the session at 3:30pm today.
const NOW = Date.parse('2026-10-06T12:00:00Z')
const DAY_MS = 24 * 60 * 60 * 1000
const seconds = (iso: string) => Date.parse(iso) / 1000
const WEEK_RESET_ISO = '2026-10-08T21:00:00Z'
const WEEK_RESET = seconds(WEEK_RESET_ISO)
const SESSION_RESET = seconds('2026-10-06T15:30:00Z')
const TODAY = dateKey(NOW, 'utc')

// The answers below are the Go paceline-mcp's (rogadev/paceline at 7a480ff),
// recorded by running its usage_test.go cases with the feed written at NOW.
// get_usage, by case.
const GO_USAGE = {
  "both": "Session: 79% left (21% used), resets 3:30pm. Week: 53% left (47% used), resets Thu 9pm.\n{\"session\":{\"usedPct\":21,\"leftPct\":79,\"resetsAt\":\"2026-10-06T15:30:00Z\",\"resetsClock\":\"3:30pm\",\"resetPassed\":false},\"week\":{\"usedPct\":47,\"leftPct\":53,\"resetsAt\":\"2026-10-08T21:00:00Z\",\"resetsClock\":\"Thu 9pm\",\"resetPassed\":false},\"ageSeconds\":0,\"stale\":false}",
  "no session": "Session: no reading in the feed. Week: 53% left (47% used), resets Thu 9pm.\n{\"session\":null,\"week\":{\"usedPct\":47,\"leftPct\":53,\"resetsAt\":\"2026-10-08T21:00:00Z\",\"resetsClock\":\"Thu 9pm\",\"resetPassed\":false},\"ageSeconds\":0,\"stale\":false}",
  "no week": "Session: 79% left (21% used), resets 3:30pm. Week: no reading in the feed.\n{\"session\":{\"usedPct\":21,\"leftPct\":79,\"resetsAt\":\"2026-10-06T15:30:00Z\",\"resetsClock\":\"3:30pm\",\"resetPassed\":false},\"week\":null,\"ageSeconds\":0,\"stale\":false}",
  "passed an hour ago": "Session: reset at 11am, so this reading (21% used) is out of date until Claude Code reports new usage. Week: 53% left (47% used), resets Thu 9pm.\n{\"session\":{\"usedPct\":21,\"leftPct\":79,\"resetsAt\":\"2026-10-06T11:00:00Z\",\"resetsClock\":\"11am\",\"resetPassed\":true},\"week\":{\"usedPct\":47,\"leftPct\":53,\"resetsAt\":\"2026-10-08T21:00:00Z\",\"resetsClock\":\"Thu 9pm\",\"resetPassed\":false},\"ageSeconds\":0,\"stale\":false}",
  "passed this very second": "Session: reset at 12pm, so this reading (21% used) is out of date until Claude Code reports new usage. Week: 53% left (47% used), resets Thu 9pm.\n{\"session\":{\"usedPct\":21,\"leftPct\":79,\"resetsAt\":\"2026-10-06T12:00:00Z\",\"resetsClock\":\"12pm\",\"resetPassed\":true},\"week\":{\"usedPct\":47,\"leftPct\":53,\"resetsAt\":\"2026-10-08T21:00:00Z\",\"resetsClock\":\"Thu 9pm\",\"resetPassed\":false},\"ageSeconds\":0,\"stale\":false}",
}

// get_today_budget, by TestTodayBudgetAnswers case.
const GO_TODAY_BUDGET = {
  "about an even share": "89% of today's budget left (11% used). Today's budget is 14% of the week, and you have spent 2% of the week today. That budget is about an even share of the week (100%/7 a day); the week resets Thu 9pm.\n{\"kind\":\"budget\",\"budgetPct\":14,\"spentPct\":2,\"usedPct\":11,\"leftPct\":89,\"over\":false,\"pace\":\"even\",\"weekResetsAt\":\"2026-10-08T21:00:00Z\",\"weekResetsClock\":\"Thu 9pm\",\"ageSeconds\":0,\"stale\":false}",
  "last day": "100% of today's budget left (0% used). Today's budget is 54% of the week, and you have spent 0% of the week today. That budget is larger than an even share of the week (100%/7 a day); the week resets 8pm.\n{\"kind\":\"budget\",\"budgetPct\":54,\"spentPct\":0,\"usedPct\":0,\"leftPct\":100,\"over\":false,\"pace\":\"up\",\"weekResetsAt\":\"2026-10-06T20:00:00Z\",\"weekResetsClock\":\"8pm\",\"ageSeconds\":0,\"stale\":false}",
  "no weekly window": "The feed has no weekly reading, so today's budget is unknown.\n{\"kind\":\"none\",\"ageSeconds\":0,\"stale\":false}",
  "over budget": "Over today's budget: 144% used. Today's budget is 21% of the week, and you have spent 30% of the week today. That budget is larger than an even share of the week (100%/7 a day); the week resets Thu 9pm.\n{\"kind\":\"budget\",\"budgetPct\":21,\"spentPct\":30,\"usedPct\":144,\"leftPct\":0,\"over\":true,\"pace\":\"up\",\"weekResetsAt\":\"2026-10-08T21:00:00Z\",\"weekResetsClock\":\"Thu 9pm\",\"ageSeconds\":0,\"stale\":false}",
  "reset already passed": "The weekly limit reset at 6am, so this reading is out of date and today's budget is unknown until Claude Code reports new usage.\n{\"kind\":\"none\",\"ageSeconds\":0,\"stale\":false}",
  "reset at this very second": "The weekly limit reset at 12pm, so this reading is out of date and today's budget is unknown until Claude Code reports new usage.\n{\"kind\":\"none\",\"ageSeconds\":0,\"stale\":false}",
  "smaller than an even share": "71% of today's budget left (29% used). Today's budget is 7% of the week, and you have spent 2% of the week today. That budget is smaller than an even share of the week (100%/7 a day); the week resets Thu 9pm.\n{\"kind\":\"budget\",\"budgetPct\":7,\"spentPct\":2,\"usedPct\":29,\"leftPct\":71,\"over\":false,\"pace\":\"down\",\"weekResetsAt\":\"2026-10-08T21:00:00Z\",\"weekResetsClock\":\"Thu 9pm\",\"ageSeconds\":0,\"stale\":false}",
  "under budget": "69% of today's budget left (31% used). Today's budget is 21% of the week, and you have spent 7% of the week today. That budget is larger than an even share of the week (100%/7 a day); the week resets Thu 9pm.\n{\"kind\":\"budget\",\"budgetPct\":21,\"spentPct\":7,\"usedPct\":31,\"leftPct\":69,\"over\":false,\"pace\":\"up\",\"weekResetsAt\":\"2026-10-08T21:00:00Z\",\"weekResetsClock\":\"Thu 9pm\",\"ageSeconds\":0,\"stale\":false}",
  "week used up": "No budget today: the weekly limit was used up at the start of the day; the week resets Thu 9pm.\n{\"kind\":\"budget\",\"budgetPct\":0,\"spentPct\":0,\"usedPct\":0,\"leftPct\":0,\"over\":false,\"pace\":\"down\",\"weekResetsAt\":\"2026-10-08T21:00:00Z\",\"weekResetsClock\":\"Thu 9pm\",\"ageSeconds\":0,\"stale\":false}",
}

// get_today_budget, by TestTodayBudgetMatchesTheStatusLine case.
const GO_MATCHES_LINE = {
  "about an even pace": "89% of today's budget left (11% used). Today's budget is 14% of the week, and you have spent 2% of the week today. That budget is about an even share of the week (100%/7 a day); the week resets Thu 9pm.\n{\"kind\":\"budget\",\"budgetPct\":14,\"spentPct\":2,\"usedPct\":11,\"leftPct\":89,\"over\":false,\"pace\":\"even\",\"weekResetsAt\":\"2026-10-08T21:00:00Z\",\"weekResetsClock\":\"Thu 9pm\",\"ageSeconds\":0,\"stale\":false}",
  "below an even pace": "71% of today's budget left (29% used). Today's budget is 7% of the week, and you have spent 2% of the week today. That budget is smaller than an even share of the week (100%/7 a day); the week resets Thu 9pm.\n{\"kind\":\"budget\",\"budgetPct\":7,\"spentPct\":2,\"usedPct\":29,\"leftPct\":71,\"over\":false,\"pace\":\"down\",\"weekResetsAt\":\"2026-10-08T21:00:00Z\",\"weekResetsClock\":\"Thu 9pm\",\"ageSeconds\":0,\"stale\":false}",
  "half-point tie": "68% of today's budget left (33% used). Today's budget is 20% of the week, and you have spent 7% of the week today. That budget is larger than an even share of the week (100%/7 a day); the week resets Thu 9pm.\n{\"kind\":\"budget\",\"budgetPct\":20,\"spentPct\":7,\"usedPct\":33,\"leftPct\":68,\"over\":false,\"pace\":\"up\",\"weekResetsAt\":\"2026-10-08T21:00:00Z\",\"weekResetsClock\":\"Thu 9pm\",\"ageSeconds\":0,\"stale\":false}",
  "just over budget": "Over today's budget: 101% used. Today's budget is 21% of the week, and you have spent 21% of the week today. That budget is larger than an even share of the week (100%/7 a day); the week resets Thu 9pm.\n{\"kind\":\"budget\",\"budgetPct\":21,\"spentPct\":21,\"usedPct\":101,\"leftPct\":0,\"over\":true,\"pace\":\"up\",\"weekResetsAt\":\"2026-10-08T21:00:00Z\",\"weekResetsClock\":\"Thu 9pm\",\"ageSeconds\":0,\"stale\":false}",
  "no anchor yet": "100% of today's budget left (0% used). Today's budget is 19% of the week, and you have spent 0% of the week today. That budget is larger than an even share of the week (100%/7 a day); the week resets Thu 9pm.\n{\"kind\":\"budget\",\"budgetPct\":19,\"spentPct\":0,\"usedPct\":0,\"leftPct\":100,\"over\":false,\"pace\":\"up\",\"weekResetsAt\":\"2026-10-08T21:00:00Z\",\"weekResetsClock\":\"Thu 9pm\",\"ageSeconds\":0,\"stale\":false}",
  "over budget": "Over today's budget: 144% used. Today's budget is 21% of the week, and you have spent 30% of the week today. That budget is larger than an even share of the week (100%/7 a day); the week resets Thu 9pm.\n{\"kind\":\"budget\",\"budgetPct\":21,\"spentPct\":30,\"usedPct\":144,\"leftPct\":0,\"over\":true,\"pace\":\"up\",\"weekResetsAt\":\"2026-10-08T21:00:00Z\",\"weekResetsClock\":\"Thu 9pm\",\"ageSeconds\":0,\"stale\":false}",
  "under budget, fractional reset": "69% of today's budget left (31% used). Today's budget is 21% of the week, and you have spent 7% of the week today. That budget is larger than an even share of the week (100%/7 a day); the week resets Thu 9pm.\n{\"kind\":\"budget\",\"budgetPct\":21,\"spentPct\":7,\"usedPct\":31,\"leftPct\":69,\"over\":false,\"pace\":\"up\",\"weekResetsAt\":\"2026-10-08T21:00:00Z\",\"weekResetsClock\":\"Thu 9pm\",\"ageSeconds\":0,\"stale\":false}",
  "week used up": "No budget today: the weekly limit was used up at the start of the day; the week resets Thu 9pm.\n{\"kind\":\"budget\",\"budgetPct\":0,\"spentPct\":0,\"usedPct\":0,\"leftPct\":0,\"over\":false,\"pace\":\"down\",\"weekResetsAt\":\"2026-10-08T21:00:00Z\",\"weekResetsClock\":\"Thu 9pm\",\"ageSeconds\":0,\"stale\":false}",
}
// The Go server names its feed file where the mod reads Claude Code's own
// figures, so those sentences are reworded. Everything else is Go's text.
const REWORDED: [go: string, mod: string][] = [
  ['no reading in the feed.', 'no reading from Claude Code.'],
  [
    "The feed has no weekly reading, so today's budget is unknown.",
    "There is no weekly reading from Claude Code, so today's budget is unknown.",
  ],
]

function fromGo(text: string): string {
  let mod = text
  for (const [go, replacement] of REWORDED) mod = mod.replaceAll(go, replacement)
  return mod
}

const limit = (usedPercentage: number, resetsAt: number): Limit => ({ usedPercentage, resetsAt })
const anchor = (usedAtStart: number, resetsAt = WEEK_RESET): Snapshot => ({ date: TODAY, resetsAt, usedAtStart })

// A session's figures as the mods API reports them, with only the given
// rate-limit windows: a week-only list is what an account without a
// five-hour limit reports.
function figuresWith(rateLimits: SessionUsage['rateLimits']) {
  return {
    usage: { startedAt: NOW, context: { window: 200_000 }, rateLimits },
    modelId: 'claude-opus-5-5',
    cwd: 'C:/r/paceline-mod',
    root: 'C:/r/paceline-mod',
    sessionId: 'abc',
    nowMs: NOW,
    settings: {},
  }
}

const weekOnly = (percentUsed: number, resetsAt = WEEK_RESET_ISO) =>
  toPayload(figuresWith([{ kind: 'seven_day', percentUsed, resetsAt }]))

describe('get_today_budget answers', () => {
  const cases: { name: string; week: Limit | undefined; snapshot: Snapshot | null; go: string }[] = [
    { name: 'under budget', week: limit(46.5, WEEK_RESET), snapshot: anchor(40), go: GO_TODAY_BUDGET['under budget'] },
    { name: 'over budget', week: limit(70, WEEK_RESET), snapshot: anchor(40), go: GO_TODAY_BUDGET['over budget'] },
    {
      name: 'smaller than an even share',
      week: limit(82, WEEK_RESET),
      snapshot: anchor(80),
      go: GO_TODAY_BUDGET['smaller than an even share'],
    },
    {
      name: 'about an even share',
      week: limit(61.5, WEEK_RESET),
      snapshot: anchor(60),
      go: GO_TODAY_BUDGET['about an even share'],
    },
    { name: 'week used up', week: limit(100, WEEK_RESET), snapshot: anchor(100), go: GO_TODAY_BUDGET['week used up'] },
    {
      name: 'last day',
      week: limit(46.5, seconds('2026-10-06T20:00:00Z')),
      snapshot: null,
      go: GO_TODAY_BUDGET['last day'],
    },
    {
      name: 'reset already passed',
      week: limit(46.5, seconds('2026-10-06T06:00:00Z')),
      snapshot: null,
      go: GO_TODAY_BUDGET['reset already passed'],
    },
    {
      name: 'reset at this very second',
      week: limit(46.5, NOW / 1000),
      snapshot: null,
      go: GO_TODAY_BUDGET['reset at this very second'],
    },
    { name: 'no weekly window', week: undefined, snapshot: null, go: GO_TODAY_BUDGET['no weekly window'] },
  ]

  for (const c of cases) {
    test(`matches the Go answer: ${c.name}`, () => {
      expect(answerText(todayBudgetAnswer(c.week, c.snapshot, NOW, 'utc'))).toBe(fromGo(c.go))
    })
  }

  test("says there is no weekly reading in the mod's words", () => {
    const { summary, fields } = todayBudgetAnswer(undefined, null, NOW, 'utc')
    expect(summary).toBe("There is no weekly reading from Claude Code, so today's budget is unknown.")
    expect(fields).toEqual({ kind: 'none', ageSeconds: 0, stale: false })
  })

  test('treats a weekly limit missing a figure as no reading', () => {
    for (const week of [{ usedPercentage: 46.5 }, { resetsAt: WEEK_RESET }]) {
      const answer = todayBudgetAnswer(week, anchor(40), NOW, 'utc')
      expect([week, answer.fields]).toEqual([week, { kind: 'none', ageSeconds: 0, stale: false }])
      expect(answer.summary).toContain('There is no weekly reading from Claude Code')
    }
  })

  test('needs only the weekly reading', () => {
    const payload = weekOnly(46.5)
    expect(payload.rateLimits?.fiveHour).toBe(undefined)
    const answer = todayBudgetAnswer(payload.rateLimits?.sevenDay, null, NOW, 'utc')
    expect(answerText(answer)).toBe(GO_MATCHES_LINE['no anchor yet'])
  })
})

describe('get_today_budget agrees with the band', () => {
  // Claude Code can report a reset with a fraction of a second; the payload
  // keeps the whole second, which is what the mod's day anchor is saved with.
  const FRACTIONAL = '2026-10-08T21:00:00.700Z'
  const cases: {
    name: keyof typeof GO_MATCHES_LINE
    anchorStart: number | null
    used: number
    resetsAt: string
    pace: string
  }[] = [
    { name: 'under budget, fractional reset', anchorStart: 40, used: 46.5, resetsAt: FRACTIONAL, pace: 'up' },
    { name: 'over budget', anchorStart: 40, used: 70, resetsAt: FRACTIONAL, pace: 'up' },
    // A 20.87-point budget with 20.93 spent is over, though both round to 21,
    // so the band shows one point past the budget: 22/21.
    { name: 'just over budget', anchorStart: 40, used: 60.93, resetsAt: FRACTIONAL, pace: 'up' },
    { name: 'below an even pace', anchorStart: 80, used: 82, resetsAt: WEEK_RESET_ISO, pace: 'down' },
    { name: 'about an even pace', anchorStart: 60, used: 61.5, resetsAt: WEEK_RESET_ISO, pace: 'even' },
    { name: 'week used up', anchorStart: 100, used: 100, resetsAt: WEEK_RESET_ISO, pace: 'down' },
    // A 20-point budget with 6.5 spent is a half-point tie: the tool and the
    // band both round the spend up to 7.
    { name: 'half-point tie', anchorStart: 42.5, used: 49, resetsAt: WEEK_RESET_ISO, pace: 'up' },
    { name: 'no anchor yet', anchorStart: null, used: 46.5, resetsAt: FRACTIONAL, pace: 'up' },
  ]
  // The band's today segment ends with the weekly points: spent of the budget
  // counting up ("7/21", at least one past the budget once over: "22/21"),
  // and left ("14/21 left") or over ("1 over/21") counting down.
  const TODAY_FIGURE = /Today: \S+ (\d+(?: over)?\/\d+(?: left)?)/

  for (const c of cases) {
    test(c.name, () => {
      const payload = weekOnly(c.used, c.resetsAt)
      const snapshot = c.anchorStart === null ? null : anchor(c.anchorStart)
      const answer = todayBudgetAnswer(payload.rateLimits?.sevenDay, snapshot, NOW, 'utc')
      expect(answerText(answer)).toBe(GO_MATCHES_LINE[c.name])

      const a = answer.fields
      if (a.kind !== 'budget') throw new Error(`kind is ${a.kind}, want budget`)
      expect(a.pace).toBe(c.pace)
      // Over budget, the band never shows a spend equal to the budget.
      const shownSpent = a.over ? Math.max(a.spentPct, a.budgetPct + 1) : a.spentPct
      for (const down of [false, true]) {
        const config = defaultConfig()
        config.countDown.today = down
        const line = render(payload, { nowMs: NOW, zone: 'utc', config, readSnapshot: () => snapshot, compact: true })
        let want = `${shownSpent}/${a.budgetPct}`
        if (down) want = a.over ? `${shownSpent - a.budgetPct} over/${a.budgetPct}` : `${a.budgetPct - a.spentPct}/${a.budgetPct} left`
        const text = toAnsi(line, true)
        expect([down, TODAY_FIGURE.exec(text)?.[1] ?? text]).toEqual([down, want])
      }
    })
  }
})

describe('get_today_budget saves nothing', () => {
  const GO_FIELDS = [
    'kind',
    'budgetPct',
    'spentPct',
    'usedPct',
    'leftPct',
    'over',
    'pace',
    'weekResetsAt',
    'weekResetsClock',
    'ageSeconds',
    'stale',
  ]

  test("returns only the summary and Go's fields, and leaves its snapshot as it was", () => {
    const snapshot = Object.freeze(anchor(40))
    const before = { ...snapshot }
    const answer = todayBudgetAnswer(limit(46.5, WEEK_RESET), snapshot, NOW, 'utc')
    expect(Object.keys(answer)).toEqual(['summary', 'fields'])
    expect(Object.keys(answer.fields)).toEqual(GO_FIELDS)
    expect(snapshot).toEqual(before)
  })

  // computeBudget re-anchors the day here and asks for the new snapshot to be
  // saved; the answer must not hand that on.
  test('re-anchors a stale snapshot without returning a new one', () => {
    const yesterday = Object.freeze({ ...anchor(30), date: dateKey(NOW - DAY_MS, 'utc') })
    const before = { ...yesterday }
    const cases: { name: string; snapshot: Snapshot | null }[] = [
      { name: 'yesterday', snapshot: yesterday },
      { name: 'none', snapshot: null },
    ]
    for (const c of cases) {
      const answer = todayBudgetAnswer(limit(46.5, WEEK_RESET), c.snapshot, NOW, 'utc')
      const f = answer.fields
      expect([c.name, f.kind, f.kind === 'budget' ? f.spentPct : null]).toEqual([c.name, 'budget', 0])
      expect([c.name, Object.keys(answer.fields)]).toEqual([c.name, GO_FIELDS])
      expect([c.name, answerText(answer).includes('usedAtStart')]).toEqual([c.name, false])
    }
    expect(yesterday).toEqual(before)
  })
})

describe('get_usage answers', () => {
  test('reports both windows', () => {
    const answer = usageAnswer({ fiveHour: limit(21, SESSION_RESET), sevenDay: limit(46.5, WEEK_RESET) }, NOW, 'utc')
    expect(answerText(answer)).toBe(GO_USAGE['both'])
  })

  test('never reports less than nothing left', () => {
    const { fields } = usageAnswer({ sevenDay: limit(100.5, WEEK_RESET) }, NOW, 'utc')
    expect([fields.week?.usedPct, fields.week?.leftPct]).toEqual([101, 0])
  })

  test('reports a missing session window as null and still reports the week', () => {
    const payload = weekOnly(46.5)
    const answer = usageAnswer(payload.rateLimits, NOW, 'utc')
    expect(answerText(answer)).toBe(fromGo(GO_USAGE['no session']))
    expect(answer.fields.session).toBe(null)
    expect(answer.summary).toContain('Session: no reading from Claude Code.')
    expect(answerText(answer)).toContain('"session":null')
    expect(answer.fields.week?.usedPct).toBe(47)
  })

  test('reports a missing week window as null', () => {
    const answer = usageAnswer({ fiveHour: limit(21, SESSION_RESET) }, NOW, 'utc')
    expect(answerText(answer)).toBe(fromGo(GO_USAGE['no week']))
    expect(answer.summary).toContain('Week: no reading from Claude Code.')
    expect(answer.fields.week).toBe(null)
  })

  test('reports a window missing a figure as no reading', () => {
    const answer = usageAnswer({ fiveHour: { usedPercentage: 21 }, sevenDay: { resetsAt: WEEK_RESET } }, NOW, 'utc')
    expect(answer.fields).toEqual({ session: null, week: null, ageSeconds: 0, stale: false })
    expect(answer.summary).toBe('Session: no reading from Claude Code. Week: no reading from Claude Code.')
  })

  test('reports a passed reset without a left figure', () => {
    const cases = [
      { name: 'an hour ago', resetsAt: seconds('2026-10-06T11:00:00Z'), go: GO_USAGE['passed an hour ago'] },
      { name: 'this very second', resetsAt: NOW / 1000, go: GO_USAGE['passed this very second'] },
    ]
    for (const c of cases) {
      const answer = usageAnswer({ fiveHour: limit(21, c.resetsAt), sevenDay: limit(46.5, WEEK_RESET) }, NOW, 'utc')
      expect([c.name, answerText(answer)]).toEqual([c.name, c.go])
      expect([c.name, answer.fields.session?.resetPassed]).toEqual([c.name, true])
    }
  })
})

describe('the reading is live', () => {
  test('every answer is zero seconds old and leads with no age line', () => {
    const answers = [
      usageAnswer({ fiveHour: limit(21, SESSION_RESET), sevenDay: limit(46.5, WEEK_RESET) }, NOW, 'utc'),
      usageAnswer(undefined, NOW, 'utc'),
      todayBudgetAnswer(limit(46.5, WEEK_RESET), anchor(40), NOW, 'utc'),
      todayBudgetAnswer(limit(46.5, NOW / 1000), null, NOW, 'utc'),
      todayBudgetAnswer(undefined, null, NOW, 'utc'),
    ]
    for (const a of answers) {
      expect([a.summary, a.fields.ageSeconds, a.fields.stale]).toEqual([a.summary, 0, false])
      expect(a.summary.startsWith('This reading is')).toBe(false)
    }
  })
})

describe('rfc3339', () => {
  test('formats UTC as Go does, to the whole second', () => {
    expect(rfc3339(WEEK_RESET, 'utc')).toBe('2026-10-08T21:00:00Z')
    expect(rfc3339(WEEK_RESET + 0.7, 'utc')).toBe('2026-10-08T21:00:00Z')
    expect(rfc3339(seconds('2026-01-02T03:04:05Z'), 'utc')).toBe('2026-01-02T03:04:05Z')
  })

  // Tests can't set the machine's time zone, so this checks the local form
  // against the zone the tests run in: the offset is the reset instant's own.
  test('formats local time with the offset at that instant', () => {
    for (const sec of [SESSION_RESET, WEEK_RESET, seconds('2026-01-15T12:00:00Z'), seconds('2026-07-15T12:00:00Z')]) {
      const text = rfc3339(sec, 'local')
      const offset = -new Date(sec * 1000).getTimezoneOffset()
      const pad = (n: number) => String(n).padStart(2, '0')
      const suffix =
        offset === 0 ? 'Z' : `${offset < 0 ? '-' : '+'}${pad(Math.trunc(Math.abs(offset) / 60))}:${pad(Math.abs(offset) % 60)}`
      expect(text).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(Z|[+-]\d{2}:\d{2})$/)
      expect([text, text.endsWith(suffix)]).toEqual([text, true])
      expect([text, Date.parse(text)]).toEqual([text, sec * 1000])
    }
  })

  test('gives get_usage local reset times', () => {
    const { fields } = usageAnswer({ fiveHour: limit(21, SESSION_RESET) }, NOW, 'local')
    expect(fields.session?.resetsAt).toBe(rfc3339(SESSION_RESET, 'local'))
    expect(fields.session?.resetsClock).toBe(clock(SESSION_RESET, NOW, 'local'))
  })
})
