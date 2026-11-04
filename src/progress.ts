/**
 * Reads and writes the progress file: a small JSON record of a long job's
 * steps that the line draws as a bar.
 *
 * The file lives in the repository's git directory, at
 * `paceline/progress-<session id>.json` for one Claude Code session's run, or
 * `paceline/progress.json` for a writer outside Claude Code. The format is the
 * one the Go paceline and paceline-mcp share, and the writer here produces the
 * same bytes and answers as paceline-mcp's progress tools. Everything in the
 * file is untrusted: the reader caps its size and rejects anything malformed,
 * and every string is cleaned before it is drawn.
 */

export const MAX_FILE_BYTES = 32 * 1024
const MAX_STEPS = 50
const MAX_STAGES = 12
const MAX_LABEL_CHARS = 40
const MAX_WEIGHT = 1000
const MAX_SESSION_LEN = 64
const VERSION = 1

const SECOND = 1000
const MINUTE = 60 * SECOND
const PAUSED_AFTER = 60 * MINUTE
const QUIET_HIDDEN_FROM = 4 * 60 * MINUTE
const DONE_HIDDEN_FROM = 30 * MINUTE

// 0001-01-01T00:00:00Z, Go's zero time, which the format rejects as unset.
const ZERO_TIME_MS = -62135596800000

const PHASES = ['running', 'done', 'halted'] as const
const STATUSES = ['pending', 'active', 'done', 'skipped', 'blocked'] as const

export type Phase = (typeof PHASES)[number]
export type Status = (typeof STATUSES)[number]

export type Step = {
  id: string
  label: string
  status: Status
  /** Size relative to the other steps; 0 counts as 1. */
  weight: number
  stages: string[]
  stage: string
}

export type Run = {
  name: string
  phase: Phase
  label: string
  steps: Step[]
  updatedAtMs: number
}

/** A step as a writer plans it, before it has a status. */
export type NewStep = Pick<Step, 'id' | 'label' | 'weight' | 'stages'>

/**
 * One progress tool's call, with its arguments decoded. A string argument the
 * caller left out is empty, and a step list it left out has no steps.
 */
export type ProgressCall =
  | { tool: 'progress_start'; name: string; steps: NewStep[] }
  | { tool: 'progress_step'; id: string; status: string; stage: string }
  | { tool: 'progress_label'; label: string }
  | { tool: 'progress_add_steps'; steps: NewStep[] }
  | { tool: 'progress_finish'; outcome: string }

/** The name of a progress tool, as paceline-mcp serves it. */
export type ProgressTool = ProgressCall['tool']

/**
 * A progress tool's outcome: the file's new text and the summary to answer
 * with, or the error to answer with, in which case the file stays as it was.
 */
export type ProgressWrite = { text: string; summary: string } | { error: string }

/** A decoded call, with the `cwd` argument that names the run's repository, or `""`. */
export type DecodedCall = { call: ProgressCall; cwd: string }

/**
 * Accepts a Claude Code session id: letters, digits, and dashes, so it can
 * never name a path outside the progress directory.
 */
export function isValidSession(session: string): boolean {
  return session.length > 0 && session.length <= MAX_SESSION_LEN && /^[0-9A-Za-z-]+$/.test(session)
}

/** The progress file inside a git directory for a session, or the shared file. */
export function progressPath(gitDir: string, session: string): string {
  const name = isValidSession(session) ? `progress-${session}.json` : 'progress.json'
  return `${gitDir}/paceline/${name}`
}

function isPhase(s: string): s is Phase {
  return (PHASES as readonly string[]).includes(s)
}

function isStatus(s: string): s is Status {
  return (STATUSES as readonly string[]).includes(s)
}

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

// A JSON null decodes to the field's zero value, as it does for the Go reader.
function text(v: unknown): string | undefined {
  if (v === undefined || v === null) return ''
  return typeof v === 'string' ? v : undefined
}

// RFC 3339, as Go's time.Time decodes it.
const RFC3339 = /^\d{4}-\d{2}-\d{2}[Tt]\d{2}:\d{2}:\d{2}(\.\d+)?([Zz]|[+-]\d{2}:\d{2})$/

// Decodes a step's fields and their types; `validateRun` checks the format's rules.
function parseStep(v: unknown): Step | null {
  if (!isObject(v)) return null
  const id = text(v.id)
  const label = text(v.label)
  const status = text(v.status)
  const stage = text(v.stage)
  const weight = v.weight === undefined || v.weight === null ? 0 : v.weight
  const stagesRaw = v.stages === undefined || v.stages === null ? [] : v.stages
  if (id === undefined || label === undefined || status === undefined || stage === undefined) return null
  if (typeof weight !== 'number' || !Number.isFinite(weight)) return null
  if (!Array.isArray(stagesRaw) || !isStatus(status)) return null
  const stages: string[] = []
  for (const raw of stagesRaw) {
    const s = text(raw)
    if (s === undefined) return null
    stages.push(s)
  }
  return { id, label, status, weight, stages, stage }
}

/**
 * Decodes and validates a progress file's text, or returns null when it is
 * oversized, not JSON, or breaks any rule of the format.
 */
export function parseRun(fileText: string): Run | null {
  if (new TextEncoder().encode(fileText).length > MAX_FILE_BYTES) return null
  let doc: unknown
  try {
    doc = JSON.parse(fileText)
  } catch {
    return null
  }
  if (!isObject(doc) || doc.version !== VERSION) return null
  const name = text(doc.name)
  const phase = text(doc.phase)
  const label = text(doc.label)
  if (name === undefined || phase === undefined || label === undefined || !isPhase(phase)) return null
  if (!Array.isArray(doc.steps)) return null
  if (typeof doc.updatedAt !== 'string' || !RFC3339.test(doc.updatedAt)) return null
  const updatedAtMs = Date.parse(doc.updatedAt)
  if (!Number.isFinite(updatedAtMs) || updatedAtMs === ZERO_TIME_MS) return null

  const steps: Step[] = []
  for (const raw of doc.steps) {
    const step = parseStep(raw)
    if (step === null) return null
    steps.push(step)
  }
  const run = { name, phase, label, steps, updatedAtMs }
  return validateRun(run) === null ? run : null
}

/**
 * Reports whether the run belongs on the line: a finished run shows for a
 * while, and any other run hides once it has gone quiet long enough that its
 * writer is gone.
 */
export function isVisible(r: Run, nowMs: number): boolean {
  const age = nowMs - r.updatedAtMs
  return r.phase === 'done' ? age <= DONE_HIDDEN_FROM : age <= QUIET_HIDDEN_FROM
}

/** Reports whether a running run has been quiet so long its writer likely stopped. */
export function isPaused(r: Run, nowMs: number): boolean {
  return r.phase === 'running' && nowMs - r.updatedAtMs > PAUSED_AFTER
}

/** Reports whether any step has a weight or stages, which makes `percent` more than a count. */
export function isPlanned(r: Run): boolean {
  return r.steps.some(s => s.weight > 0 || s.stages.length > 0)
}

/** The first active step's index, or -1. */
export function activeIndex(r: Run): number {
  return r.steps.findIndex(s => s.status === 'active')
}

/** How many steps are no longer pending or active. */
export function settledCount(r: Run): number {
  return r.steps.filter(s => s.status !== 'pending' && s.status !== 'active').length
}

/**
 * The weighted share of planned work finished, 0 to 100. Skipped and blocked
 * steps leave the total, since the run will not do them. An active step counts
 * the stages it has finished: a step in the third of four stages is half done.
 */
export function percent(r: Run): number {
  let total = 0
  let finished = 0
  for (const s of r.steps) {
    if (s.status === 'skipped' || s.status === 'blocked') continue
    const w = s.weight === 0 ? 1 : s.weight
    total += w
    if (s.status === 'done') finished += w
    if (s.status === 'active') {
      const i = s.stages.indexOf(s.stage)
      if (i > 0) finished += (w * i) / s.stages.length
    }
  }
  return total === 0 ? 100 : (100 * finished) / total
}

// Go's %q verb: quotes and backslashes escaped, and any character Go does not
// count as printable (controls, separators other than the space, unassigned
// code points) written as a \x, \u, or \U escape.
const GO_QUOTE_ESCAPES: Record<string, string> = {
  '"': '\\"',
  '\\': '\\\\',
  '\u0007': '\\a',
  '\b': '\\b',
  '\f': '\\f',
  '\n': '\\n',
  '\r': '\\r',
  '\t': '\\t',
  '\v': '\\v',
}

function goQuote(s: string): string {
  const quoted = s.replace(/["\\]|(?! )[\p{C}\p{Z}]/gu, ch => {
    const escape = GO_QUOTE_ESCAPES[ch]
    if (escape !== undefined) return escape
    const code = ch.codePointAt(0)
    if (code === undefined) return ch
    if (code < 0x80) return '\\x' + code.toString(16).padStart(2, '0')
    if (code < 0x10000) return '\\u' + code.toString(16).padStart(4, '0')
    return '\\U' + code.toString(16).padStart(8, '0')
  })
  return `"${quoted}"`
}

// Go's %v verb for a string slice.
function goList(items: readonly string[]): string {
  return `[${items.join(' ')}]`
}

// Go's validText: what is wrong with a text field, after its name, or null.
function textError(s: string, required: boolean): string | null {
  if (required && s === '') return 'must not be empty'
  if ([...s].length > MAX_LABEL_CHARS) return `must be at most ${MAX_LABEL_CHARS} characters`
  return null
}

function validateStep(s: Step): string | null {
  const id = textError(s.id, true)
  if (id !== null) return `id ${id}`
  const label = textError(s.label, false)
  if (label !== null) return `label ${label}`
  if (s.weight < 0 || s.weight > MAX_WEIGHT) return `weight must be between 0 and ${MAX_WEIGHT}`
  if (s.stages.length > MAX_STAGES) return `at most ${MAX_STAGES} stages`
  for (const stage of s.stages) {
    const invalid = textError(stage, true)
    if (invalid !== null) return `stage ${invalid}`
  }
  if (s.stage !== '' && !s.stages.includes(s.stage)) return `stage ${goQuote(s.stage)} is not one of its stages`
  return null
}

// Go's Run.Validate, with its messages, in its order: the first rule the run
// breaks, or null. The version, phase, and statuses are left out because the
// `Run` type already guarantees them.
function validateRun(r: Run): string | null {
  const name = textError(r.name, true)
  if (name !== null) return `name ${name}`
  const label = textError(r.label, false)
  if (label !== null) return `label ${label}`
  if (r.steps.length === 0 || r.steps.length > MAX_STEPS) return `a run needs 1 to ${MAX_STEPS} steps`
  const seen = new Set<string>()
  for (const s of r.steps) {
    const invalid = validateStep(s)
    if (invalid !== null) return `step ${goQuote(s.id)}: ${invalid}`
    if (seen.has(s.id)) return `step id ${goQuote(s.id)} is used twice`
    seen.add(s.id)
  }
  return null
}

// A lone UTF-16 surrogate, which Go's JSON decoder reads as U+FFFD.
const LONE_SURROGATE = /[\ud800-\udbff](?![\udc00-\udfff])|(?<![\ud800-\udbff])[\udc00-\udfff]/g

function wellFormed(s: string): string {
  return s.replace(LONE_SURROGATE, '\ufffd')
}

// Characters Go's encoding/json escapes that JSON.stringify writes as they are.
const GO_JSON_ESCAPES: Record<string, string> = {
  '<': '\\u003c',
  '>': '\\u003e',
  '&': '\\u0026',
  '\u2028': '\\u2028',
  '\u2029': '\\u2029',
}

// The bytes Go's json.MarshalIndent(r, "", "  ") writes. JSON.stringify drops
// a key whose value is undefined, which stands in for Go's omitempty; its
// number format and indentation already match Go's.
function serializeRun(r: Run): string {
  const doc = {
    version: VERSION,
    name: r.name,
    phase: r.phase,
    label: r.label === '' ? undefined : r.label,
    steps: r.steps.map(s => ({
      id: s.id,
      label: s.label,
      status: s.status,
      weight: s.weight === 0 ? undefined : s.weight,
      stages: s.stages.length === 0 ? undefined : s.stages,
      stage: s.stage === '' ? undefined : s.stage,
    })),
    updatedAt: new Date(r.updatedAtMs).toISOString().replace(/\.\d{3}Z$/, 'Z'),
  }
  const json = JSON.stringify(doc, (_key, value: unknown) => (typeof value === 'string' ? wellFormed(value) : value), 2)
  return json.replace(/[<>&\u2028\u2029]/g, ch => GO_JSON_ESCAPES[ch])
}

// paceline-mcp's summary: where the run stands after a change.
function summarize(r: Run): string {
  const settled = `${r.name}: ${r.phase}, ${settledCount(r)} of ${r.steps.length} steps settled`
  return isPlanned(r) ? `${settled}, ${Math.trunc(percent(r))}% of planned work.` : `${settled}.`
}

// Go's Save: stamps the run, validates it, and writes it if it fits the cap.
function saveRun(r: Run, nowMs: number): ProgressWrite {
  r.updatedAtMs = Math.floor(nowMs / SECOND) * SECOND
  const invalid = validateRun(r)
  if (invalid !== null) return { error: invalid }
  const fileText = serializeRun(r)
  if (new TextEncoder().encode(fileText).length > MAX_FILE_BYTES) {
    return { error: 'the run is too large to save; use fewer or shorter steps' }
  }
  return { text: fileText, summary: summarize(r) }
}

// Go's Load, which says why a file is unusable so the agent can start over.
function loadRun(fileText: string | null): Run | { error: string } {
  if (fileText === null) return { error: 'no run in progress; start one first' }
  return parseRun(fileText) ?? { error: 'the progress file is unreadable or invalid; start a new run to replace it' }
}

function pendingStep(n: NewStep): Step {
  return { ...n, status: 'pending', stage: '' }
}

function startRun(name: string, steps: NewStep[]): Run {
  return { name, phase: 'running', label: '', steps: steps.map(pendingStep), updatedAtMs: 0 }
}

// Go's SetStep. Naming a stage implies the step is active, and activating a
// step returns any other active step to pending, where it keeps its stage.
function setStep(r: Run, id: string, statusArg: string, stage: string): string | null {
  const step = r.steps.find(s => s.id === id)
  if (step === undefined) return `no step with id ${goQuote(id)}`
  if (statusArg === '' && stage === '') return 'give a status, a stage, or both'
  const status = statusArg === '' ? 'active' : statusArg
  if (!isStatus(status)) return `status must be one of ${goList(STATUSES)}`
  if (stage !== '') {
    if (!step.stages.includes(stage)) {
      return `stage ${goQuote(stage)} is not one of step ${goQuote(id)}'s stages ${goList(step.stages)}`
    }
    if (status !== 'active') return 'only an active step has a current stage'
    step.stage = stage
  }
  step.status = status
  if (status === 'active') {
    for (const other of r.steps) if (other.id !== id && other.status === 'active') other.status = 'pending'
  } else {
    step.stage = ''
  }
  // A step change after finishing means the run picked up again.
  r.phase = 'running'
  return null
}

function addSteps(r: Run, steps: NewStep[]): string | null {
  if (steps.length === 0) return 'give at least one step'
  r.steps.push(...steps.map(pendingStep))
  return r.steps.length > MAX_STEPS ? `a run holds at most ${MAX_STEPS} steps` : null
}

// Go's Finish: finishing done marks the active step done, and every other
// step keeps the status the writer gave it.
function finishRun(r: Run, outcome: string): string | null {
  if (outcome !== 'done' && outcome !== 'halted') return 'outcome must be "done" or "halted"'
  r.phase = outcome
  r.label = ''
  if (outcome === 'halted') return null
  for (const s of r.steps) {
    if (s.status !== 'active') continue
    s.status = 'done'
    s.stage = ''
  }
  return null
}

function changeRun(r: Run, call: Exclude<ProgressCall, { tool: 'progress_start' }>): string | null {
  switch (call.tool) {
    case 'progress_step':
      return setStep(r, call.id, call.status, call.stage)
    case 'progress_label':
      r.label = call.label
      return null
    case 'progress_add_steps':
      return addSteps(r, call.steps)
    case 'progress_finish':
      return finishRun(r, call.outcome)
  }
}

/**
 * Applies a progress tool's call to the current progress file, as
 * paceline-mcp does, and returns the file's new text with the tool's summary,
 * or the tool's error.
 *
 * Start replaces whatever the file holds. Every other call needs a readable
 * run, and a change that fails or leaves the run invalid returns an error and
 * no text, so the file stays as it was.
 *
 * @param fileText - The progress file's text, or null when there is no file.
 * @param nowMs - The time to stamp the run with; it is written in UTC,
 *     truncated to the second.
 */
export function applyProgressCall(call: ProgressCall, fileText: string | null, nowMs: number): ProgressWrite {
  if (call.tool === 'progress_start') return saveRun(startRun(call.name, call.steps), nowMs)
  const loaded = loadRun(fileText)
  if ('error' in loaded) return loaded
  const failed = changeRun(loaded, call)
  return failed === null ? saveRun(loaded, nowMs) : { error: failed }
}

// One field of the Go tool's argument struct. Go's errors name a tagged field
// by its JSON key and an untagged one by its Go name.
type ArgField = { key: string; goName: string; tagged: boolean; kind: 'string' | 'steps' }

const GO_FIELD_TYPES = { string: 'string', steps: '[]progress.NewStep' } as const

const TOOL_FIELDS: Record<ProgressTool, readonly ArgField[]> = {
  progress_start: [
    { key: 'name', goName: 'Name', tagged: true, kind: 'string' },
    { key: 'steps', goName: 'Steps', tagged: true, kind: 'steps' },
  ],
  progress_step: [
    { key: 'id', goName: 'ID', tagged: false, kind: 'string' },
    { key: 'status', goName: 'Status', tagged: false, kind: 'string' },
    { key: 'stage', goName: 'Stage', tagged: false, kind: 'string' },
  ],
  progress_label: [{ key: 'label', goName: 'Label', tagged: false, kind: 'string' }],
  progress_add_steps: [{ key: 'steps', goName: 'Steps', tagged: true, kind: 'steps' }],
  progress_finish: [{ key: 'outcome', goName: 'Outcome', tagged: false, kind: 'string' }],
}

// The Go struct's type as fmt prints it, for an argument that is not an object.
function goStructType(fields: readonly ArgField[]): string {
  const parts = fields.map(f => {
    const tag = f.tagged ? ` "json:\\"${f.key}\\""` : ''
    return `${f.goName} ${GO_FIELD_TYPES[f.kind]}${tag}`
  })
  return `struct { ${parts.join('; ')} }`
}

class ArgumentError extends Error {}

function jsonKind(v: unknown): string {
  if (Array.isArray(v)) return 'array'
  return typeof v === 'boolean' ? 'bool' : typeof v
}

// Go's json.UnmarshalTypeError message.
function typeError(value: unknown, where: string, goType: string): ArgumentError {
  return new ArgumentError(`json: cannot unmarshal ${jsonKind(value)} into ${where} of type ${goType}`)
}

function decodeString(v: unknown, where: string): string {
  if (typeof v !== 'string') throw typeError(v, where, 'string')
  return wellFormed(v)
}

function decodeStages(v: unknown, where: string): string[] {
  if (!Array.isArray(v)) throw typeError(v, where, '[]string')
  return v.map(item => (item === null ? '' : decodeString(item, where)))
}

// Decodes one planned step. As in Go, a null leaves a field as it was, except
// that a null slice empties it, and a key matches its field in any case.
function decodeNewStep(v: unknown, where: string): NewStep {
  const step: NewStep = { id: '', label: '', weight: 0, stages: [] }
  if (v === null) return step
  if (!isObject(v)) throw typeError(v, where, 'progress.NewStep')
  for (const [rawKey, value] of Object.entries(v)) {
    const key = rawKey.toLowerCase()
    const at = `Go struct field NewStep.steps.${key}`
    if (key === 'stages') step.stages = value === null ? [] : decodeStages(value, at)
    if (value === null) continue
    if (key === 'id' || key === 'label') step[key] = decodeString(value, at)
    if (key === 'weight') {
      if (typeof value !== 'number' || !Number.isFinite(value)) throw typeError(value, at, 'float64')
      step.weight = value
    }
  }
  return step
}

function decodeSteps(v: unknown, where: string): NewStep[] {
  if (!Array.isArray(v)) throw typeError(v, where, GO_FIELD_TYPES.steps)
  return v.map(item => decodeNewStep(item, where))
}

type ArgValues = { strings: Record<string, string>; steps: NewStep[] }

// Decodes a tool's arguments the way Go's json.Unmarshal fills its struct:
// keys match fields in any case, the last match wins, unknown keys are
// ignored, and the first wrong type in document order is the error.
function decodeArgs(fields: readonly ArgField[], args: unknown): ArgValues {
  const values: ArgValues = { strings: {}, steps: [] }
  for (const f of fields) if (f.kind === 'string') values.strings[f.key] = ''
  if (args === undefined || args === null) return values
  if (!isObject(args)) throw typeError(args, 'Go value', goStructType(fields))
  for (const [key, value] of Object.entries(args)) {
    const f = fields.find(field => field.key === key.toLowerCase())
    if (f === undefined) continue
    const where = `Go struct field .${f.tagged ? f.key : f.goName}`
    if (f.kind === 'steps') values.steps = value === null ? [] : decodeSteps(value, where)
    else if (value !== null) values.strings[f.key] = decodeString(value, where)
  }
  return values
}

// paceline-mcp reads cwd in a second pass that ignores type errors.
function decodeCwd(args: unknown): string {
  let cwd = ''
  if (!isObject(args)) return cwd
  for (const [key, value] of Object.entries(args)) {
    if (key.toLowerCase() === 'cwd' && typeof value === 'string') cwd = wellFormed(value)
  }
  return cwd
}

function buildCall(tool: ProgressTool, { strings: s, steps }: ArgValues): ProgressCall {
  switch (tool) {
    case 'progress_start':
      return { tool, name: s.name, steps }
    case 'progress_step':
      return { tool, id: s.id, status: s.status, stage: s.stage }
    case 'progress_label':
      return { tool, label: s.label }
    case 'progress_add_steps':
      return { tool, steps }
    case 'progress_finish':
      return { tool, outcome: s.outcome }
  }
}

/**
 * Decodes a progress tool's raw arguments as paceline-mcp does, or returns an
 * error starting `invalid arguments: ` when one has the wrong type.
 *
 * Missing or null arguments decode as empty, so the call's own checks report
 * them. The `cwd` argument is read separately and is `""` when it is missing
 * or not a string.
 */
export function decodeProgressCall(tool: ProgressTool, args: unknown): DecodedCall | { error: string } {
  let values: ArgValues
  try {
    values = decodeArgs(TOOL_FIELDS[tool], args)
  } catch (e) {
    if (e instanceof ArgumentError) return { error: `invalid arguments: ${e.message}` }
    throw e
  }
  return { call: buildCall(tool, values), cwd: decodeCwd(args) }
}
