/**
 * Decodes an `/orc-loop` run's state file and draws it as the loop segment: one
 * cell per issue in the batch, then the run's phase.
 *
 * orc-loop writes the file to `<git dir>/orc-loop/state.json`. Everything in
 * it is untrusted: the decoder caps its size and the number of issues, keeps
 * only the fields the segment draws, and reduces the in-flight marker to an
 * issue number, so no string from the file reaches the line.
 */

import type { Segment, Span, Tone } from './render.ts'

/** The largest state file the decoder accepts, in bytes. */
export const MAX_STATE_BYTES = 256 * 1024
const MAX_ISSUES = 50
const MAX_REVIEW_ROUND = 99
const REVIEW_ROUNDS = 3

const MINUTE = 60 * 1000
const DONE_HIDDEN_AFTER = 30 * MINUTE
const PAUSED_AFTER = 6 * 60 * MINUTE

// The same glyphs as src/render.ts, copied rather than imported so that
// render.ts can import this module without the two loading each other.
// Importing only types from render.ts adds no such cycle.
const CELL_FULL = '\u25b0'
const CELL_EMPTY = '\u25b1'
const CHECK_MARK = '\u2713'

const PHASES = ['plan', 'build', 'review', 'triage', 'fallow', 'comments', 'done', 'halted'] as const
const STATUSES = ['pending', 'done', 'skipped', 'blocked'] as const
const PAUSABLE: readonly LoopPhase[] = ['build', 'review', 'plan']

// "44" or "#44": orc-loop writes the bare number while building, and a
// hand-edited or older file may carry the hash.
const ISSUE_REF = /^#?([1-9]\d{0,8})$/

/** A phase orc-loop writes, or `other` for one this version does not know. */
export type LoopPhase = (typeof PHASES)[number] | 'other'

/** An issue's status, or `other` for a missing or unknown one. */
export type IssueStatus = (typeof STATUSES)[number] | 'other'

/** One issue in the run's batch, as the segment draws it. */
export type LoopIssue = {
  /** The issue number, or null when the file's value is not one. */
  n: number | null
  status: IssueStatus
}

/** The parts of an orc-loop state file the segment draws. */
export type LoopState = {
  phase: LoopPhase
  issues: LoopIssue[]
  /** The issue being built, or null when the run is between issues or past the build. */
  inFlight: number | null
  /** The review round in progress; 0 before the first. */
  reviewRound: number
  /** True when the run draws its own bar through paceline's progress tools. */
  progressOn: boolean
}

/** A decoded state file and when it was last written, what {@link loopSegment} draws from. */
export type LoopFile = {
  state: LoopState
  /** The file's last write time, in epoch milliseconds. */
  mtimeMs: number
}

/** Returns the path of the state file inside a git directory. */
export function loopStatePath(gitDir: string): string {
  return `${gitDir}/orc-loop/state.json`
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

function oneOf<T extends string>(values: readonly T[], v: unknown): T | 'other' {
  return values.find(known => known === v) ?? 'other'
}

function issueNumber(v: unknown): number | null {
  if (typeof v === 'number') return Number.isSafeInteger(v) && v > 0 ? v : null
  if (typeof v !== 'string') return null
  const m = ISSUE_REF.exec(v)
  return m ? Number(m[1]) : null
}

function reviewRound(v: unknown): number {
  return typeof v === 'number' && Number.isInteger(v) && v >= 0 && v <= MAX_REVIEW_ROUND ? v : 0
}

function parseIssue(v: unknown): LoopIssue | null {
  if (!isRecord(v)) return null
  return { n: issueNumber(v.n), status: oneOf(STATUSES, v.status) }
}

/**
 * Decodes a state file's text, or returns null when it is oversized, not JSON,
 * has no string `phase`, or has an `issues` value that is not a list of up to
 * 50 objects. Missing optional fields take their empty values.
 */
export function decodeLoopState(fileText: string): LoopState | null {
  if (new TextEncoder().encode(fileText).length > MAX_STATE_BYTES) return null
  let doc: unknown
  try {
    doc = JSON.parse(fileText)
  } catch {
    return null
  }
  if (!isRecord(doc) || typeof doc.phase !== 'string') return null
  const issuesRaw = doc.issues ?? []
  if (!Array.isArray(issuesRaw) || issuesRaw.length > MAX_ISSUES) return null

  const issues: LoopIssue[] = []
  for (const raw of issuesRaw) {
    const issue = parseIssue(raw)
    if (issue === null) return null
    issues.push(issue)
  }
  return {
    phase: oneOf(PHASES, doc.phase),
    issues,
    inFlight: issueNumber(doc.inFlight),
    reviewRound: reviewRound(doc.reviewRound),
    progressOn: isRecord(doc.progress) && Boolean(doc.progress.on),
  }
}

// The same helper as in src/render.ts, copied for the reason given above CELL_FULL.
const span = (text: string, tone: Tone = 'plain'): Span => ({ text, tone })

function cell(issue: LoopIssue, inFlight: number | null): Span {
  if (issue.status === 'done') return span(CELL_FULL, 'green')
  if (issue.status === 'blocked') return span(CELL_FULL, 'red')
  if (issue.status === 'skipped') return span(CELL_FULL, 'dim')
  if (issue.n !== null && issue.n === inFlight) return span(CELL_FULL, 'cyan')
  return span(CELL_EMPTY, 'dim')
}

// Neighbors with the same glyph and tone share one span.
function cells(s: LoopState): Span[] {
  const out: Span[] = []
  for (const issue of s.issues) {
    const next = cell(issue, s.inFlight)
    const last = out.at(-1)
    if (last && last.tone === next.tone && last.text.startsWith(next.text)) last.text += next.text
    else out.push(next)
  }
  return out
}

function label(s: LoopState): Span {
  if (s.phase === 'halted') return span('halted', 'red')
  if (s.phase === 'done') return span(CHECK_MARK + ' done', 'green')
  if (s.phase === 'plan') return span('planning')
  if (s.phase === 'review') return span(`review ${Math.max(s.reviewRound, 1)}/${REVIEW_ROUNDS}`)
  const settled = s.issues.filter(i => i.status !== 'pending').length
  const current = s.inFlight === null ? '' : `#${s.inFlight} `
  return span(`${current}${settled}/${s.issues.length}`)
}

/**
 * Draws the run as "loop cells label", or returns null when it is hidden.
 *
 * The segment hides when there is no state, when the run draws its own bar
 * through paceline's progress tools, and once a finished run's file is over 30
 * minutes old. A build, review, or plan whose file has gone 6 hours without a
 * write gets a dim " (paused)" after its label.
 *
 * @param state - The decoded state file, or null when it is missing or unreadable.
 * @param mtimeMs - The state file's last write time, in epoch milliseconds.
 * @param nowMs - The current time, in epoch milliseconds.
 */
export function loopSegment(state: LoopState | null, mtimeMs: number, nowMs: number): Segment | null {
  if (state === null || state.progressOn) return null
  const age = nowMs - mtimeMs
  if (state.phase === 'done' && age > DONE_HIDDEN_AFTER) return null

  const out: Span[] = [span('loop'), span(' ')]
  const drawn = cells(state)
  if (drawn.length > 0) out.push(...drawn, span(' '))
  out.push(label(state))
  if (PAUSABLE.includes(state.phase) && age > PAUSED_AFTER) out.push(span(' (paused)', 'dim'))
  return out
}
