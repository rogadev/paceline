/**
 * Parses where the current dev -> main batch stands in code review and draws it
 * as the batch segment: the batch's size, then the review's state.
 *
 * The batch is every commit between `git merge-base origin/main HEAD` and
 * HEAD, and its reviews are the lines of deep-review's
 * `tmp/reviews/ledger.jsonl` recorded against that merge base. Everything from
 * the ledger is untrusted: the parser caps its size, keeps a review's head only
 * when it is a commit id, reduces the verdict to the values the segment tells
 * apart, and clamps the counts, so no string from the file reaches the line.
 *
 * The module also decides when the segment applies, builds the batch's git
 * argument lists and cache key, turns git's results into the segment's state,
 * and decides when the cached state is drawn or recomputed. hooks/register.ts
 * runs the git calls and holds the cache; docs/notes.md records why git runs
 * at all.
 */

import { FULL_SHA, type Head } from './gitinfo.ts'
import type { Segment, Span, Tone } from './render.ts'

/** The largest ledger the parser accepts, in bytes. */
export const MAX_LEDGER_BYTES = 1024 * 1024
const MAX_COUNT = 999
/** The most commits the batch's `git log` reads, the same as the count clamp. */
export const MAX_BATCH_COMMITS = MAX_COUNT

// The same glyphs as src/render.ts, copied rather than imported so that
// render.ts can import this module without the two loading each other.
// Importing only types from render.ts adds no such cycle.
const CHECK_MARK = '\u2713'
const SEPARATOR = ' \u00b7 '

const VERDICTS = ['ready', 'incomplete'] as const

// Hex only, so a head passed to `git rev-list` can never be read as an option
// or a revision expression. A short id can still name a ref as well as a
// commit; `sinceArgv` peels it with `^{commit}`, so one naming anything but a
// commit fails the count instead of counting from it.
const COMMIT_ID = /^[0-9a-f]{4,64}$/i
const MAIN_REF = 'refs/heads/main'
// Global options for every batch git call, after `-C <work tree>`.
// `--no-lazy-fetch` keeps a partial clone from fetching missing objects, and a
// git too old to know it (before 2.45) exits 129, so the segment fails closed.
// The fsmonitor pin is defense in depth: none of these commands reads the index.
const GIT_PIN = ['--no-lazy-fetch', '-c', 'core.fsmonitor=false']
const ISSUE_REF = /\b(?:refs|closes|fixes|resolves)\s+#(\d+)/gi

/** A review's verdict, or `other` for any the segment does not draw on its own. */
export type Verdict = (typeof VERDICTS)[number] | 'other'

/** One review from the ledger, as the segment draws it. */
export type LedgerEntry = {
  /** The commit the review saw, or null when the ledger's value is not a commit id. */
  head: string | null
  verdict: Verdict
  /** The review's blocker count, from 0 to 999. */
  blockers: number
  /** The review's warning count, from 0 to 999. */
  warnings: number
}

/** What the batch segment draws. */
export type BatchState = {
  /** The commits in the batch. */
  commits: number
  /** The distinct issues the batch's commit messages reference. */
  issues: number
  /** The reviews of this batch, oldest first. */
  reviews: readonly LedgerEntry[]
  /**
   * The commits made since the last review's head: 0 when the last review saw
   * HEAD or there are no reviews, and `gone` when its head is null or git could
   * not count from it because the commit was rebased away.
   */
  sinceLastReview: number | 'gone'
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

function count(v: unknown): number {
  return typeof v === 'number' && Number.isInteger(v) && v >= 0 ? Math.min(v, MAX_COUNT) : 0
}

function parseEntry(lineText: string, base: string): LedgerEntry | null {
  let doc: unknown
  try {
    doc = JSON.parse(lineText)
  } catch {
    // A torn or hand-edited line costs one review, not the whole segment.
    return null
  }
  if (!isRecord(doc) || doc.base !== base || typeof doc.head !== 'string') return null
  return {
    head: COMMIT_ID.test(doc.head) ? doc.head : null,
    verdict: VERDICTS.find(known => known === doc.verdict) ?? 'other',
    blockers: count(doc.blockers),
    warnings: count(doc.warnings),
  }
}

/**
 * Parses a ledger's text into the reviews of one batch, in file order.
 *
 * A line counts when it is a JSON object whose `base` equals `base` and whose
 * `head` is a string. Blank, torn, and unparsable lines are skipped.
 *
 * @param ledgerText - The text of `tmp/reviews/ledger.jsonl`.
 * @param base - The batch's merge base, as a full sha.
 * @returns The batch's reviews, or null when the text is over {@link MAX_LEDGER_BYTES}.
 */
export function parseLedger(ledgerText: string, base: string): LedgerEntry[] | null {
  if (new TextEncoder().encode(ledgerText).length > MAX_LEDGER_BYTES) return null
  const entries: LedgerEntry[] = []
  for (const lineText of ledgerText.split('\n')) {
    if (lineText.trim() === '') continue
    const entry = parseEntry(lineText, base)
    if (entry !== null) entries.push(entry)
  }
  return entries
}

/**
 * Splits the output of `git log --format=%x00%B` into one message per commit.
 * Each message starts after a NUL; blank pieces are dropped.
 */
export function splitCommitMessages(logText: string): string[] {
  return logText.split('\0').filter(message => message.trim() !== '')
}

/**
 * Counts the distinct issue numbers that commit messages reference with
 * `refs`, `closes`, `fixes`, or `resolves` followed by `#<number>`, in any case.
 */
export function countIssueRefs(messages: readonly string[]): number {
  const issues = new Set<number>()
  for (const message of messages) {
    for (const [, n] of message.matchAll(ISSUE_REF)) issues.add(Number(n))
  }
  return issues.size
}

// The same helper as in src/render.ts, copied for the reason given above CHECK_MARK.
const span = (text: string, tone: Tone = 'plain'): Span => ({ text, tone })

function plural(n: number, word: string): string {
  return `${n} ${word}${n === 1 ? '' : 's'}`
}

function joinParts(parts: Span[]): Segment {
  const out: Segment = []
  for (const part of parts) {
    if (out.length > 0) out.push(span(SEPARATOR, 'dim'))
    out.push(part)
  }
  return out
}

function staleText(sinceLastReview: number | 'gone'): string {
  if (sinceLastReview === 'gone') return 'review stale'
  return `review stale (+${plural(sinceLastReview, 'commit')})`
}

// Only the first of these that applies shows, so blockers hide warnings.
function verdictPart(last: LedgerEntry): Span {
  if (last.verdict === 'incomplete') return span('review incomplete', 'yellow')
  if (last.blockers > 0) return span(plural(last.blockers, 'blocker'), 'red')
  if (last.warnings > 0) return span(plural(last.warnings, 'warning'), 'yellow')
  if (last.verdict === 'ready') return span(CHECK_MARK + ' ready', 'green')
  return span('not ready', 'yellow')
}

/**
 * Draws the batch as its size and its review state, joined by a dim middle
 * dot, or returns null when the batch has no commits.
 *
 * The size reads `N issues` when the commits reference any issue, otherwise
 * `N commits`. Then, in order of precedence:
 *
 * - no reviews: `no review yet`
 * - commits since the last review: `review stale (+N commits)`, or `review stale` when its commit is gone
 * - otherwise the review count, then the first that applies to the last review: `review incomplete`,
 *   its blockers, its warnings, a check mark and `ready`, or `not ready`
 */
export function batchSegment(state: BatchState): Segment | null {
  if (state.commits === 0) return null
  const lead = span(state.issues > 0 ? plural(state.issues, 'issue') : plural(state.commits, 'commit'))
  const last = state.reviews.at(-1)
  if (last === undefined) return joinParts([lead, span('no review yet', 'yellow')])
  if (state.sinceLastReview !== 0) return joinParts([lead, span(staleText(state.sinceLastReview), 'yellow')])
  return joinParts([lead, span(plural(state.reviews.length, 'review')), verdictPart(last)])
}

/** The ref the batch is measured against. */
export const ORIGIN_MAIN_REF = 'refs/remotes/origin/main'

/** Returns the path of the deep-review script whose presence marks a repository that keeps a review ledger. */
export function ledgerScriptPath(workTree: string): string {
  return `${workTree}/.claude/skills/deep-review/scripts/ledger.mjs`
}

/** Returns the path of the review ledger deep-review appends to, in the work tree. */
export function ledgerPath(workTree: string): string {
  return `${workTree}/tmp/reviews/ledger.jsonl`
}

/**
 * The inputs of the batch segment's gate and cache key, all read without a
 * process, once the work tree is known to have deep-review's `ledger.mjs`.
 */
export type BatchRefs = {
  /** The work tree that holds `.git`, where deep-review keeps its ledger. */
  workTree: string
  head: Head | null
  /** The sha `refs/remotes/origin/main` resolves to, or null when it does not. */
  mainSha: string | null
  /** The ledger file's stat, or null when there is no ledger yet. */
  ledger: { mtimeMs: number; size: number } | null
}

/**
 * Checks the rest of the batch segment's gate and builds the key its git
 * results are cached under.
 *
 * The caller checks first that the work tree has deep-review's `ledger.mjs`
 * script. Past that, the segment applies only when HEAD is on a branch other
 * than `main` that has a commit and origin/main resolves. The key changes when
 * the work tree, HEAD, origin/main, or the ledger's mtime or size does.
 *
 * @returns The key and the two shas the git calls take, or null when the segment does not apply.
 */
export function batchKey(refs: BatchRefs): { key: string; headSha: string; mainSha: string } | null {
  const { head, mainSha } = refs
  if (head?.kind !== 'branch' || head.ref === MAIN_REF || head.sha === null || mainSha === null) return null
  const ledger = refs.ledger ?? { mtimeMs: 0, size: 0 }
  const key = JSON.stringify([refs.workTree, head.sha, mainSha, ledger.mtimeMs, ledger.size])
  return { key, headSha: head.sha, mainSha }
}

/** The batch cache: the key it was computed for, its result, and when a failed run may retry. */
export type BatchCache = {
  key: string
  batch: BatchState | null
  /** Epoch milliseconds after which the key is computed again; `Infinity` for a settled result. */
  retryAtMs: number
}

/** How long a batch whose git calls timed out or could not start waits before it is tried again, in milliseconds. */
export const GIT_RETRY_MS = 60_000

/** Checks whether `cache` answers `key` at `nowMs`: the same key, and its retry time not yet reached. */
export function isFresh(cache: BatchCache | null, key: string, nowMs: number): boolean {
  return cache !== null && cache.key === key && nowMs < cache.retryAtMs
}

/** What the host environment says about where git would run. */
export type GitHost = {
  /** The host's `GIT_DIR`, when set. */
  gitDir?: string
  /** The host's `GIT_WORK_TREE`, when set. */
  workTree?: string
  /** The host's `USERPROFILE`, set on Windows. */
  userProfile?: string
}

// A drive-letter path ("C:/x") or a UNC share ("//server/share"), the two
// absolute forms on Windows, after backslashes become slashes.
const WINDOWS_ABSOLUTE = /^(?:[A-Za-z]:\/|\/\/[^/]+\/[^/])/

/**
 * Checks whether the batch segment may run git: its toggle is on, the host
 * sets neither `GIT_DIR` nor `GIT_WORK_TREE` (git would follow them instead of
 * the work tree), and `configDir`, git's working folder, is absolute.
 *
 * A drive-letter or UNC path is absolute on any host. A path rooted at `/` is
 * absolute only off Windows: on Windows it is relative to the current drive.
 * The engine doesn't say which platform it runs on, so a set `USERPROFILE`,
 * which Windows always sets, stands in for it.
 */
export function batchAllowed(on: boolean, host: GitHost, configDir: string): boolean {
  if (!on || host.gitDir !== undefined || host.workTree !== undefined) return false
  const dir = configDir.replaceAll('\\', '/')
  if (WINDOWS_ABSOLUTE.test(dir)) return true
  return host.userProfile === undefined && dir.startsWith('/') && !dir.startsWith('//')
}

/** The batch cache and the keys the background update is coordinated by. */
export type BatchFlow = {
  cache: BatchCache | null
  /** The key a background update is computing now, or null. */
  pending: string | null
  /** The key the latest refresh asked for, or null when it skipped the batch. */
  wanted: string | null
}

/**
 * Decides, for one refresh, what batch to draw and whether to start a
 * background update for `key`.
 *
 * It draws the cache only when the cache is for `key`. It starts an update
 * when the cache doesn't answer `key` (see {@link isFresh}) and none is
 * already computing it.
 *
 * @param key - The current cache key, or null when the refresh skipped the batch.
 * @returns The next flow, the batch to draw, and true if an update should start; false otherwise.
 */
export function planBatch(
  flow: BatchFlow,
  key: string | null,
  nowMs: number,
): { next: BatchFlow; draw: BatchState | null; start: boolean } {
  if (key === null) return { next: { ...flow, wanted: null }, draw: null, start: false }
  const start = !isFresh(flow.cache, key, nowMs) && flow.pending !== key
  const next = { cache: flow.cache, pending: start ? key : flow.pending, wanted: key }
  return { next, draw: flow.cache?.key === key ? flow.cache.batch : null, start }
}

/**
 * Takes a finished update's cache entry: it clears the entry's pending mark,
 * and keeps the entry only when its key is still the one wanted, so an older
 * result never replaces a newer one.
 *
 * @returns The next flow, and true if the entry was kept and the line should redraw; false otherwise.
 */
export function acceptResult(flow: BatchFlow, entry: BatchCache): { next: BatchFlow; redraw: boolean } {
  const pending = flow.pending === entry.key ? null : flow.pending
  if (entry.key !== flow.wanted) return { next: { ...flow, pending }, redraw: false }
  return { next: { cache: entry, pending, wanted: flow.wanted }, redraw: true }
}

/** Builds the arguments of `git merge-base` for origin/main and HEAD, by sha. */
export function mergeBaseArgv(workTree: string, mainSha: string, headSha: string): string[] {
  return ['-C', workTree, ...GIT_PIN, 'merge-base', mainSha, headSha]
}

/**
 * Builds the arguments of `git log` for the batch's messages, in the format
 * {@link splitCommitMessages} reads, at most {@link MAX_BATCH_COMMITS} of them.
 * `--no-show-signature` keeps a repo's `log.showSignature` from starting its
 * `gpg.program`.
 */
export function logArgv(workTree: string, base: string, headSha: string): string[] {
  return [
    '-C',
    workTree,
    ...GIT_PIN,
    'log',
    '--no-show-signature',
    `--max-count=${MAX_BATCH_COMMITS}`,
    '--format=%x00%B',
    `${base}..${headSha}`,
  ]
}

/** Builds the arguments of `git rev-list --count` from the reviewed commit to HEAD. */
export function sinceArgv(workTree: string, reviewHead: string, headSha: string): string[] {
  return ['-C', workTree, ...GIT_PIN, 'rev-list', '--count', `${reviewHead}^{commit}..${headSha}`]
}

/**
 * The environment every batch git call adds to the host's. It turns off lazy
 * fetches for a git that reads the variable. It sets no `GIT_DIR` or
 * `GIT_WORK_TREE`: git finds the repository from `-C` itself, so its
 * `safe.directory` ownership check still applies.
 */
export const GIT_ENV: Readonly<Record<string, string>> = { GIT_NO_LAZY_FETCH: '1' }

/**
 * Gets the reviewed commit to count new commits from: the last review's head,
 * or null when there is no review, its head is not a commit id, or it saw HEAD.
 */
export function staleReviewHead(last: LedgerEntry | undefined, headSha: string): string | null {
  if (last === undefined || last.head === null || last.head === headSha) return null
  return last.head
}

/**
 * Maps the last review and the output of `git rev-list --count` from its head
 * to {@link BatchState.sinceLastReview}.
 *
 * @param countText - What git printed, or null when the count failed or did not run.
 * @returns 0 with no review or when it saw HEAD; `gone` when its head is not a
 *     commit id or the count failed or is not a number; otherwise the count, up to 999.
 */
export function sinceLastReview(
  last: LedgerEntry | undefined,
  headSha: string,
  countText: string | null,
): number | 'gone' {
  if (last === undefined || last.head === headSha) return 0
  const n = countText?.trim() ?? ''
  if (last.head === null || !/^\d+$/.test(n)) return 'gone'
  return Math.min(Number(n), MAX_COUNT)
}

/** What a git call printed, when it exited 0. */
export type GitOutput = { stdout: string; isStdoutTruncated: boolean }

/**
 * Runs one git call with the given arguments: its output on exit 0, or null on
 * any other exit. Rejects when git times out or cannot start.
 */
export type GitRun = (argv: readonly string[]) => Promise<GitOutput | null>

/** Reads the ledger's text, or null when it cannot be read. */
export type LedgerRead = () => Promise<string | null>

/**
 * Runs git for the batch between origin/main and HEAD, reads its reviews, and
 * returns what the segment draws, or null when there is no batch to draw.
 *
 * No batch when git finds no merge base, the log fails or passes the output
 * cap (a cut message makes the counts untrustworthy), the batch has no
 * commits, or the ledger exists but can't be read. The commits since the last
 * review are counted only when it saw a commit other than HEAD.
 *
 * @param run - Runs one git call. A rejection passes through.
 * @param readLedger - Reads the ledger, or null when there is no ledger yet.
 */
export async function computeBatch(
  run: GitRun,
  readLedger: LedgerRead | null,
  workTree: string,
  shas: { headSha: string; mainSha: string },
): Promise<BatchState | null> {
  const { headSha, mainSha } = shas
  const base = (await run(mergeBaseArgv(workTree, mainSha, headSha)))?.stdout.trim() ?? ''
  if (!FULL_SHA.test(base)) return null
  const [log, reviews] = await Promise.all([run(logArgv(workTree, base, headSha)), reviewsOf(readLedger, base)])
  if (log === null || log.isStdoutTruncated || reviews === null) return null
  const messages = splitCommitMessages(log.stdout)
  if (messages.length === 0) return null
  const last = reviews.at(-1)
  const from = staleReviewHead(last, headSha)
  const counted = from === null ? null : await run(sinceArgv(workTree, from, headSha))
  return {
    commits: messages.length,
    issues: countIssueRefs(messages),
    reviews,
    sinceLastReview: sinceLastReview(last, headSha, counted?.stdout ?? null),
  }
}

// The batch's reviews: none without a ledger, null when it can't be read.
async function reviewsOf(readLedger: LedgerRead | null, base: string): Promise<LedgerEntry[] | null> {
  if (readLedger === null) return []
  const text = await readLedger()
  return text === null ? null : parseLedger(text, base)
}

/**
 * Runs `compute` and turns its outcome into a cache entry for `key`. A result,
 * including no batch, is kept until the key changes. A rejection (a git call
 * that timed out or could not start) is kept as no batch for
 * {@link GIT_RETRY_MS}, and returned as `error` for the caller to log.
 */
export async function settleBatch(
  key: string,
  nowMs: number,
  compute: () => Promise<BatchState | null>,
): Promise<{ cache: BatchCache; error?: unknown }> {
  try {
    return { cache: { key, batch: await compute(), retryAtMs: Infinity } }
  } catch (error) {
    return { cache: { key, batch: null, retryAtMs: nowMs + GIT_RETRY_MS }, error }
  }
}
