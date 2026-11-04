import type { EngineInterface, FsStat, Register, ToolSpec } from 'claude-code'

import { bandTree, type BandElements } from '../src/band.ts'
import {
  GIT_ENV,
  MAX_LEDGER_BYTES,
  ORIGIN_MAIN_REF,
  acceptResult,
  batchAllowed,
  batchKey,
  computeBatch,
  ledgerPath,
  ledgerScriptPath,
  planBatch,
  settleBatch,
  type BatchFlow,
  type BatchRefs,
  type BatchState,
  type GitOutput,
} from '../src/batch.ts'
import { defaultConfig, parseConfig } from '../src/config.ts'
import { isActivity, migrateActivity, recordActivity } from '../src/forecast.ts'
import { absoluteDir, branchIn, findRepo, readHead, resolveRef, type GitFs, type Repo } from '../src/gitinfo.ts'
import {
  ACTIVITY_KEY,
  LEGACY_ACTIVITY_KEY,
  SNAPSHOT_KEY,
  chooseSnapshot,
  configDirFrom,
  debugReport,
  renderLive,
  toPayload,
  turnEffort,
  type DebugInput,
} from '../src/live.ts'
import { MAX_STATE_BYTES, decodeLoopState, loopStatePath, type LoopFile } from '../src/loop.ts'
import type { Snapshot } from '../src/pace.ts'
import type { Limit, Payload } from '../src/payload.ts'
import {
  MAX_FILE_BYTES,
  applyProgressCall,
  decodeProgressCall,
  isValidSession,
  parseRun,
  progressPath,
  type ProgressTool,
  type Run,
} from '../src/progress.ts'
import type { LimitPick, Line } from '../src/render.ts'
import { dateKey } from '../src/timefmt.ts'
import { PROGRESS_TOOL_SPECS, TOOL_PREFIX, USAGE_TOOL_SPECS } from '../src/tools.ts'
import { answerText, todayBudgetAnswer, usageAnswer } from '../src/usage.ts'

// How often the line refreshes with nothing else to prompt it: the duration
// ticks, and a progress file another process writes can change.
const REFRESH_MS = 15_000
const MAX_CONFIG_BYTES = 64 * 1024
const MAX_SNAPSHOT_BYTES = 4096
// HEAD, a .git file, commondir, and a loose ref are a line each; packed-refs
// can be large, up to the 4 MiB `$.fs.read` accepts.
const MAX_GIT_FILE_BYTES = 4096
const MAX_PACKED_REFS_BYTES = 4 * 1024 * 1024
const DEBUG_TOOL = 'mcp__paceline__get_forecast_debug'
const DEBUG_COMMAND = 'paceline-debug'
// Each batch git call gets this long before it is killed.
const GIT_TIMEOUT_MS = 1500

// Module state starts over on a reload; the first refresh refills it.
let line: Line = []
let effort: string | undefined
let lastPick: LimitPick['show'] | undefined
let lastDebug: unknown
let running: Promise<void> | null = null
let again = false
// The batch's cached git results and the keys its background update runs by;
// `planBatch` and `acceptResult` in src/batch.ts decide every change. A refresh
// never waits on git.
let batchFlow: BatchFlow = { cache: null, pending: null, wanted: null }
// The tools this session registered, by short name. `session.start` refills it
// after a reload, and a /clear keeps it. A call to a tool missing here belongs
// to a paceline MCP server, so its hook passes the call on.
const served = new Set<string>()

// What a tool.call hook answers a call with.
type ToolReply = { result: string } | { deny: string }

// Every call on `$` stays in this file: the engine follows `$` only into
// functions declared here, never across an import.

/** Reads a small regular file's text and last write time, or null when it is missing, too big, or unreadable. */
async function readStamped(
  $: EngineInterface,
  path: string,
  maxBytes: number,
): Promise<{ text: string; mtimeMs: number } | null> {
  try {
    const stat = await $.fs.stat(path)
    if (stat.kind !== 'file' || stat.size > maxBytes) return null
    return { text: await $.fs.read(path), mtimeMs: stat.mtimeMs }
  } catch {
    return null
  }
}

/** Reads a small regular file's text, or null when it is missing, too big, or unreadable. */
async function readSmall($: EngineInterface, path: string, maxBytes: number): Promise<string | null> {
  return (await readStamped($, path, maxBytes))?.text ?? null
}

/** Reads a file under a git directory for gitinfo; anything but a small regular file reads as missing. */
async function readGitFile($: EngineInterface, path: string): Promise<string | null> {
  return readSmall($, path, path.endsWith('/packed-refs') ? MAX_PACKED_REFS_BYTES : MAX_GIT_FILE_BYTES)
}

async function statOf($: EngineInterface, path: string): Promise<FsStat | null> {
  try {
    return await $.fs.stat(path)
  } catch {
    return null
  }
}

async function kindOf($: EngineInterface, path: string): Promise<'file' | 'dir' | 'other' | null> {
  return (await statOf($, path))?.kind ?? null
}

/** The repository's progress run for this session, or the shared run. */
async function loadProgress($: EngineInterface, gitDir: string, session: string): Promise<Run | null> {
  if (gitDir === '') return null
  for (const id of isValidSession(session) ? [session, ''] : ['']) {
    const text = await readSmall($, progressPath(gitDir, id), MAX_FILE_BYTES)
    const run = text === null ? null : parseRun(text)
    if (run) return run
  }
  return null
}

/** The repository's `/orc-loop` state file, decoded, or null when it is missing or unreadable. */
async function loadLoop($: EngineInterface, gitDir: string): Promise<LoopFile | null> {
  if (gitDir === '') return null
  const file = await readStamped($, loopStatePath(gitDir), MAX_STATE_BYTES)
  if (file === null) return null
  const state = decodeLoopState(file.text)
  return state === null ? null : { state, mtimeMs: file.mtimeMs }
}

/**
 * Reads the batch gate's inputs: HEAD, origin/main, and the ledger's stat, or
 * null when the work tree has no deep-review `ledger.mjs`, so a repository
 * without deep-review costs one stat.
 */
async function readBatchRefs($: EngineInterface, fs: GitFs, repo: Repo): Promise<BatchRefs | null> {
  if ((await kindOf($, ledgerScriptPath(repo.workTree))) !== 'file') return null
  const [head, mainSha, ledger] = await Promise.all([
    readHead(fs, repo.gitDir),
    resolveRef(fs, repo.gitDir, ORIGIN_MAIN_REF),
    statOf($, ledgerPath(repo.workTree)),
  ])
  return { workTree: repo.workTree, head, mainSha, ledger: ledger && { mtimeMs: ledger.mtimeMs, size: ledger.size } }
}

/**
 * Runs one batch git call and resolves its output, or null when git exits
 * non-zero. Rejects when git times out or cannot start.
 *
 * `cwd` is a folder the repository doesn't control, so a `git` executable
 * committed to the repository can't be found ahead of the real one; the
 * arguments name the work tree with `-C`.
 */
async function runGit($: EngineInterface, cwd: string, argv: readonly string[]): Promise<GitOutput | null> {
  const result = await $.process.run(['git', ...argv], { cwd, env: GIT_ENV, timeoutMs: GIT_TIMEOUT_MS })
  return result.exitCode === 0 ? result : null
}

/**
 * Computes the batch for `found.key` in the background, keeps the result when
 * that key is still the one the latest refresh asked for, and redraws. Never
 * rejects: a failure is logged and kept as no batch until its retry time.
 */
async function updateBatch(
  $: EngineInterface,
  workTree: string,
  found: { key: string; headSha: string; mainSha: string },
  hasLedger: boolean,
  nowMs: number,
  gitCwd: string,
): Promise<void> {
  const readLedger = hasLedger ? () => readSmall($, ledgerPath(workTree), MAX_LEDGER_BYTES) : null
  const { cache, error } = await settleBatch(found.key, nowMs, () =>
    computeBatch(argv => runGit($, gitCwd, argv), readLedger, workTree, found),
  )
  if (error !== undefined) $.ui.log(`paceline: batch git failed: ${String(error)}`, { to: 'debug' })
  const accepted = acceptResult(batchFlow, cache)
  batchFlow = accepted.next
  if (accepted.redraw) void refresh($)
}

/**
 * The batch to draw now, from the cache, starting a background update when
 * `planBatch` says to. With `refs` null (the batch path was skipped), it
 * draws nothing and forgets the wanted key.
 */
function currentBatch($: EngineInterface, refs: BatchRefs | null, nowMs: number, gitCwd: string): BatchState | null {
  const found = refs === null ? null : batchKey(refs)
  const plan = planBatch(batchFlow, found?.key ?? null, nowMs)
  batchFlow = plan.next
  if (plan.start && refs !== null && found !== null) {
    void updateBatch($, refs.workTree, found, refs.ledger !== null, nowMs, gitCwd)
  }
  return plan.draw
}

/** Gives gitinfo its file reads. */
function gitFsOf($: EngineInterface): GitFs {
  return { kind: path => kindOf($, path), read: path => readGitFile($, path) }
}

/** Asks the session for its figures and builds the status payload from them. */
async function readFigures(
  $: EngineInterface,
): Promise<{ payload: Payload; cwd: string; sessionId: string; nowMs: number }> {
  const [usage, modelId, cwd, root, sessionId, nowMs, settings] = await Promise.all([
    $.session.usage(),
    $.session.model(),
    $.session.cwd(),
    $.session.root(),
    $.session.id(),
    $.clock.now(),
    $.settings.read(),
  ])
  const envEffort = await $.env.get('CLAUDE_EFFORT')
  const payload = toPayload({ usage, modelId, cwd, root, sessionId, nowMs, settings, effort, envEffort })
  return { payload, cwd, sessionId, nowMs }
}

/** Gets Claude Code's config directory, with the `USERPROFILE` it read on the way. */
async function readConfigDir($: EngineInterface): Promise<{ dir: string; userProfile: string | undefined }> {
  const userProfile = await $.env.get('USERPROFILE')
  const dir = configDirFrom({
    claudeConfigDir: await $.env.get('CLAUDE_CONFIG_DIR'),
    userProfile,
    home: await $.env.get('HOME'),
  })
  return { dir, userProfile }
}

/**
 * Picks today's snapshot the way both the band and `get_today_budget` read
 * it, with the source it came from. Reads only; the band saves a new anchor
 * from its render's result.
 */
async function readDaySnapshot(
  $: EngineInterface,
  dir: string,
  nowMs: number,
  week: Limit | undefined,
): Promise<{ snapshot: Snapshot | null; source: DebugInput['snapshotSource'] }> {
  const [goSnapshot, stored] = await Promise.all([
    readSmall($, `${dir}/paceline-day.json`, MAX_SNAPSHOT_BYTES),
    $.store.get(SNAPSHOT_KEY),
  ])
  const snapshot = chooseSnapshot(goSnapshot, stored, dateKey(nowMs, 'local'), week)
  if (snapshot === null) return { snapshot, source: 'none' }
  return { snapshot, source: snapshot === stored ? 'store' : 'go-file' }
}

/** Asks the session for its figures and renders the line. */
async function liveLine($: EngineInterface): Promise<Line> {
  const { payload, cwd, sessionId, nowMs } = await readFigures($)
  const { dir, userProfile } = await readConfigDir($)

  const fs = gitFsOf($)
  const [configText, day, storedActivity, legacyActivity, repo, hostGitDir, hostWorkTree] =
    await Promise.all([
      readSmall($, `${dir}/paceline.json`, MAX_CONFIG_BYTES),
      readDaySnapshot($, dir, nowMs, payload.rateLimits?.sevenDay),
      $.store.get(ACTIVITY_KEY),
      $.store.get(LEGACY_ACTIVITY_KEY),
      findRepo(fs, cwd),
      $.env.get('GIT_DIR'),
      $.env.get('GIT_WORK_TREE'),
    ])
  // Parsed before the repository reads, so the loop or batch segment turned off costs no reads, and the batch
  // turned off starts no git.
  const config = configText === null ? defaultConfig() : parseConfig(configText)
  const gitDir = repo?.gitDir ?? ''
  const allowed = batchAllowed(config.segments.batch, { gitDir: hostGitDir, workTree: hostWorkTree, userProfile }, dir)
  const [branch, run, loop, batchRefs] = await Promise.all([
    branchIn(fs, gitDir),
    loadProgress($, gitDir, sessionId),
    config.segments.loop ? loadLoop($, gitDir) : null,
    allowed && repo !== null ? readBatchRefs($, fs, repo) : null,
  ])
  const batch = currentBatch($, batchRefs, nowMs, dir)

  const { snapshot, source: snapshotSource } = day
  const recorded = recordActivity(
    isActivity(storedActivity) ? storedActivity : migrateActivity(legacyActivity),
    nowMs,
    'local',
    payload.rateLimits?.fiveHour,
    payload.rateLimits?.sevenDay,
  )
  if (recorded.changed) await $.store.set(ACTIVITY_KEY, recorded.activity)

  const state = {
    snapshot,
    branch,
    run,
    loop,
    batch,
    activity: recorded.activity,
    nowMs,
    previous: lastPick,
  }
  const result = renderLive(payload, config, state)
  lastDebug = debugReport({
    nowMs,
    payload,
    config,
    state,
    snapshotSource,
    result,
  })
  lastPick = result.pick?.show
  if (result.save) await $.store.set(SNAPSHOT_KEY, result.save)
  return result.line
}

/**
 * Re-reads the session's figures and redraws the band. One refresh runs at a
 * time; a request that arrives during one runs once more after it, so the
 * line always ends on the latest figures.
 */
function refresh($: EngineInterface): Promise<void> {
  if (running) {
    again = true
    return running
  }
  running = refreshLoop($)
  return running
}

async function refreshLoop($: EngineInterface): Promise<void> {
  do {
    again = false
    try {
      line = await liveLine($)
      $.ui.invalidate('ui.render')
    } catch (err) {
      // Keep the last good line: a failed read must never blank it.
      $.ui.log(`paceline: refresh failed: ${String(err)}`, { to: 'debug' })
    }
  } while (again)
  running = null
}

/**
 * Registers one of the tools for this session and notes it as served.
 *
 * Registering throws when the session's MCP config has a server named
 * "paceline" (the Go paceline-mcp), which owns the `mcp__paceline__` names.
 * The tool is then left to that server, and the band and /paceline-debug
 * work without it.
 */
async function registerTool($: EngineInterface, spec: ToolSpec): Promise<void> {
  try {
    await $.tool.register(spec)
    served.add(spec.name)
  } catch {
    // Left to the MCP server for this session.
  }
}

/**
 * Reads a progress file for a progress tool: null when there is none, and
 * empty text, which no run parses from, when it exists but is too big or
 * can't be read, so the tool calls it unreadable rather than missing.
 */
async function readProgressFile($: EngineInterface, path: string): Promise<string | null> {
  if ((await statOf($, path)) === null) return null
  return (await readSmall($, path, MAX_FILE_BYTES)) ?? ''
}

/**
 * Finds the git directory a progress run belongs to, from the `cwd` argument
 * or the session's. A relative `cwd` is taken from the session's, as the MCP
 * server took it from its own working directory.
 */
async function progressGitDir($: EngineInterface, cwdArg: string): Promise<{ gitDir: string } | { error: string }> {
  const sessionCwd = await $.session.cwd()
  const cwd = cwdArg === '' ? sessionCwd : cwdArg
  const repo = await findRepo(gitFsOf($), absoluteDir(sessionCwd, cwd))
  if (repo === null) return { error: `${cwd} is not inside a git repository; pass cwd to name one` }
  return { gitDir: repo.gitDir }
}

/**
 * Serves a progress tool as paceline-mcp does: applies the call to this
 * session's run (the shared run when the session id can't name a file),
 * writes the file, and redraws the bar. A failed call leaves the file as it
 * was and refuses with the reason.
 */
async function serveProgress($: EngineInterface, tool: ProgressTool, args: unknown): Promise<ToolReply> {
  const decoded = decodeProgressCall(tool, args)
  if ('error' in decoded) return { deny: decoded.error }
  const found = await progressGitDir($, decoded.cwd)
  if ('error' in found) return { deny: found.error }
  const [sessionId, nowMs] = await Promise.all([$.session.id(), $.clock.now()])
  const path = progressPath(found.gitDir, isValidSession(sessionId) ? sessionId : '')
  const write = applyProgressCall(decoded.call, await readProgressFile($, path), nowMs)
  if ('error' in write) return { deny: write.error }
  await $.fs.write(path, write.text)
  void refresh($)
  return { result: write.summary }
}

/** Serves `get_usage` from the figures the band draws from. */
async function serveUsage($: EngineInterface): Promise<ToolReply> {
  const { payload, nowMs } = await readFigures($)
  return { result: answerText(usageAnswer(payload.rateLimits, nowMs, 'local')) }
}

/**
 * Serves `get_today_budget` from the weekly reading and the day snapshot the
 * band uses. It never saves a snapshot: the band owns the day's anchor, and a
 * tool call must not fix today's starting point.
 */
async function serveTodayBudget($: EngineInterface): Promise<ToolReply> {
  const [{ payload, nowMs }, { dir }] = await Promise.all([readFigures($), readConfigDir($)])
  const week = payload.rateLimits?.sevenDay
  const { snapshot } = await readDaySnapshot($, dir, nowMs, week)
  return { result: answerText(todayBudgetAnswer(week, snapshot, nowMs, 'local')) }
}

/** Settles a tool's reply, refusing the call with the error's message when serving it failed. */
async function settleReply(reply: Promise<ToolReply>): Promise<ToolReply> {
  try {
    return await reply
  } catch (err) {
    return { deny: err instanceof Error ? err.message : String(err) }
  }
}

function noteEffort(event: Parameters<typeof turnEffort>[1]): void {
  effort = turnEffort(effort, event)
}

// The classic and tool.call hooks below only watch, so each `.catch` lets the
// event go on if the hook fails: `next(e)` runs what is beneath it, or replays
// the result when the hook had already called `next`.
export const register: Register = on => {
  on('session.start', async ($, e, next) => {
    await refresh($)
    $.clock.every(REFRESH_MS, () => refresh($))
    await $.command.register({
      name: DEBUG_COMMAND,
      description: "Prints the figures behind paceline's forecast.",
    })
    await registerTool($, {
      name: 'get_forecast_debug',
      description:
        "Returns, as JSON, every figure behind paceline's forecast and its 'out ~time' warning: the usage readings, the day snapshot, the stored activity history, the burn rates and where they came from, and each limit's projection. Call it when asked why the line shows what it shows. Read-only.",
    })
    for (const spec of [...PROGRESS_TOOL_SPECS, ...USAGE_TOOL_SPECS]) await registerTool($, spec)
    return next(e)
  })

  // paceline-mcp's tools, when this session registered them; otherwise the
  // call goes on to the MCP server that owns the name. A failure to serve one
  // refuses the call with the reason, as the MCP server's errors did. A
  // progress tool's arguments are `e` itself: the keys the engine adds beside
  // them name no argument, and the decoder ignores keys it doesn't know.
  on('tool.call', { tool: 'mcp__paceline__progress_start' }, ($, e, next) =>
    served.has('progress_start') ? settleReply(serveProgress($, 'progress_start', e)) : next(e),
  )
  on('tool.call', { tool: 'mcp__paceline__progress_step' }, ($, e, next) =>
    served.has('progress_step') ? settleReply(serveProgress($, 'progress_step', e)) : next(e),
  )
  on('tool.call', { tool: 'mcp__paceline__progress_label' }, ($, e, next) =>
    served.has('progress_label') ? settleReply(serveProgress($, 'progress_label', e)) : next(e),
  )
  on('tool.call', { tool: 'mcp__paceline__progress_add_steps' }, ($, e, next) =>
    served.has('progress_add_steps') ? settleReply(serveProgress($, 'progress_add_steps', e)) : next(e),
  )
  on('tool.call', { tool: 'mcp__paceline__progress_finish' }, ($, e, next) =>
    served.has('progress_finish') ? settleReply(serveProgress($, 'progress_finish', e)) : next(e),
  )
  on('tool.call', { tool: 'mcp__paceline__get_usage' }, ($, e, next) =>
    served.has('get_usage') ? settleReply(serveUsage($)) : next(e),
  )
  on('tool.call', { tool: 'mcp__paceline__get_today_budget' }, ($, e, next) =>
    served.has('get_today_budget') ? settleReply(serveTodayBudget($)) : next(e),
  )

  // The debug tool and command answer from the latest refresh, redone first so
  // the figures are current.
  on('tool.call', { tool: DEBUG_TOOL }, async ($, e, next) => {
    await refresh($)
    return { result: JSON.stringify(lastDebug ?? { note: 'No reading yet.' }, null, 2) }
  })
  on('command.run', { command: DEBUG_COMMAND }, async ($, e, next) => {
    await refresh($)
    return { text: JSON.stringify(lastDebug ?? { note: 'No reading yet.' }, null, 2) }
  })

  // `/effort` changed the level: redraw now rather than on the timer.
  on('command.run', { command: 'effort' }, async ($, e, next) => {
    const result = await next(e)
    void refresh($)
    return result
  }).catch(($, e, next) => next(e))

  // Usage moved: a turn ended, or a rate-limit window moved a whole point.
  on('session.measure', ($, e, next) => {
    void refresh($)
    return next(e)
  })

  // /clear, /resume, and /branch start the session's figures over.
  on('classic.SessionStart', ($, e, next) => {
    void refresh($)
    return next(e)
  }).catch(($, e, next) => next(e))

  // The mods API has no effort reading; PostToolUse and Stop carry the turn's.
  // A subagent's events carry its own effort, so `noteEffort` ignores them.
  on('classic.PostToolUse', ($, e, next) => {
    noteEffort(e)
    return next(e)
  }).catch(($, e, next) => next(e))
  on('classic.Stop', ($, e, next) => {
    noteEffort(e)
    void refresh($)
    return next(e)
  }).catch(($, e, next) => next(e))

  // An agent reported progress to a paceline MCP server: redraw the bar now
  // rather than on the timer. The progress tools this mod serves redraw from
  // their own hooks.
  on('tool.call', async ($, e, next) => {
    const result = await next(e)
    const tool = String(e.tool)
    if (tool.startsWith(`${TOOL_PREFIX}progress_`) && !served.has(tool.slice(TOOL_PREFIX.length))) void refresh($)
    return result
  }).catch(($, e, next) => next(e))

  on('ui.render', { component: 'AbovePrompt' }, async ($, e, next) => {
    if (e.props.hasSurvey || line.length === 0) return next(e)
    const el = $.ui.resolve(e) as unknown as BandElements
    const ours = bandTree(line, el, e.surface)
    // Keep whatever the mods after this one draw in the band, under the line.
    const theirs = await next(e)
    return el.Box({ flexDirection: 'column', children: [ours, theirs] })
  })
}
