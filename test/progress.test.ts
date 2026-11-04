import { describe, expect, test } from 'claude-code/testing'

import { applyProgressCall, decodeProgressCall, parseRun, type ProgressTool, type ProgressWrite } from '../src/progress.ts'

// The Go tests' clock.
const NOW = Date.parse('2026-10-06T12:00:00Z')

type Want = { error: string } | { summary: string; file?: string }
type Call = { tool: ProgressTool; args: string; want: Want }
type Sequence = { name: string; calls: Call[] }

const lines = (...rows: string[]) => rows.join('\n')

// Decodes the arguments and applies the call, as the tool handler does.
function callTool(tool: ProgressTool, args: unknown, file: string | null, nowMs = NOW): ProgressWrite {
  const decoded = decodeProgressCall(tool, args)
  if ('error' in decoded) return decoded
  return applyProgressCall(decoded.call, file, nowMs)
}

// Runs a sequence of calls against one file: a failed call leaves the file as
// it was, and a successful one replaces it.
function runSequence(calls: Call[]): void {
  let file: string | null = null
  for (const c of calls) {
    const got = callTool(c.tool, JSON.parse(c.args), file)
    const label = `${c.tool} ${c.args.slice(0, 60)}`
    if ('error' in c.want) {
      expect([label, got]).toEqual([label, { error: c.want.error }])
      continue
    }
    if ('error' in got) throw new Error(`${label} failed: ${got.error}`)
    expect([label, got.summary]).toEqual([label, c.want.summary])
    if (c.want.file !== undefined) expect([label, got.text]).toEqual([label, c.want.file])
    expect(parseRun(got.text)).not.toBe(null)
    file = got.text
  }
}

const stepsOf = (n: number, id: (i: number) => string, label: string) =>
  Array.from({ length: n }, (_, i) => ({ id: id(i), label }))

// Argument texts too long to write out, built as the Go tests build them.
const ARGS = {
  fiftyOneSteps: JSON.stringify({ name: 'orc', steps: stepsOf(51, i => `s${i}`, 's') }),
  fortyNineMore: JSON.stringify({ steps: stepsOf(49, i => `a${i}`, 'a') }),
  // Every field at its limit: valid by every rule but larger than the reader takes.
  oversized: JSON.stringify({
    name: 'orc',
    steps: Array.from({ length: 50 }, (_, i) => ({
      id: String(i).padStart(30, '0'),
      label: 'l'.repeat(40),
      stages: Array.from({ length: 12 }, (_, j) => 's'.repeat(38) + String.fromCharCode(97 + j)),
    })),
  }),
}

describe('progress tools match the Go paceline-mcp server', () => {
  for (const seq of goSequences()) test(seq.name, () => runSequence(seq.calls))
})

describe('applyProgressCall', () => {
  const started = callTool('progress_start', { name: 'orc', steps: [{ id: '1', label: '#1' }] }, null)
  const startedText = 'text' in started ? started.text : ''

  test('reports a file it cannot read as unreadable', () => {
    expect(callTool('progress_label', { label: 'x' }, '{not json')).toEqual({
      error: 'the progress file is unreadable or invalid; start a new run to replace it',
    })
  })

  test('start replaces a file it cannot read', () => {
    expect(callTool('progress_start', { name: 'orc', steps: [{ id: '1', label: '#1' }] }, '{not json')).toEqual(started)
  })

  test('stamps updatedAt in UTC, truncated to the second', () => {
    expect(callTool('progress_start', { name: 'orc', steps: [{ id: '1', label: '#1' }] }, null, NOW + 999)).toEqual(started)
    const later = callTool('progress_label', { label: 'x' }, startedText, NOW + 61_500)
    expect('text' in later && later.text.includes('"updatedAt": "2026-10-06T12:01:01Z"')).toBe(true)
  })

  test('returns no text when a change fails', () => {
    expect(callTool('progress_step', { id: '9', status: 'done' }, startedText)).toEqual({ error: 'no step with id "9"' })
  })
})

describe('decodeProgressCall', () => {
  test('reads cwd as a string, ignoring a cwd of any other type', () => {
    expect(decodeProgressCall('progress_label', { label: 'x', cwd: '/repo' })).toEqual({
      call: { tool: 'progress_label', label: 'x' },
      cwd: '/repo',
    })
    expect(decodeProgressCall('progress_label', { label: 'x', cwd: 5 })).toEqual({
      call: { tool: 'progress_label', label: 'x' },
      cwd: '',
    })
  })

  test('treats missing arguments as empty', () => {
    expect(decodeProgressCall('progress_finish', undefined)).toEqual({ call: { tool: 'progress_finish', outcome: '' }, cwd: '' })
  })

  test('names the field and Go type of a wrong argument', () => {
    expect(decodeProgressCall('progress_finish', { outcome: ['done'] })).toEqual({
      error: 'invalid arguments: json: cannot unmarshal array into Go struct field .Outcome of type string',
    })
    expect(decodeProgressCall('progress_add_steps', { steps: [{ id: '1', label: {} }] })).toEqual({
      error: 'invalid arguments: json: cannot unmarshal object into Go struct field NewStep.steps.label of type string',
    })
    expect(decodeProgressCall('progress_step', 'x')).toEqual({
      error: 'invalid arguments: json: cannot unmarshal string into Go value of type struct { ID string; Status string; Stage string }',
    })
  })

  test('rejects a weight that is not a finite number', () => {
    const decoded = decodeProgressCall('progress_start', { name: 'orc', steps: [{ id: '1', weight: Infinity }] })
    expect('error' in decoded && decoded.error.startsWith('invalid arguments: ')).toBe(true)
  })
})

// Each call's arguments and the Go server's answer and file bytes, recorded
// by running the same calls through paceline-mcp's tools at paceline commit
// 7a480ffd88e7cb49b50544f47f5db7f9d319a90c. The first four sequences port
// TestToolsDriveTheProgressFile, TestInvalidCallsLeaveTheFileUnchanged,
// TestCwdArgument's empty step call, and TestStepBeforeStart from
// cmd/paceline-mcp/main_test.go, plus the writer cases of
// internal/progress/progress_test.go.
function goSequences(): Sequence[] {
  return [
    {
      name: 'drives a run from start to finish',
      calls: [
        {
          tool: 'progress_start',
          args: '{"name": "orc", "steps": [ {"id": "1", "label": "#1", "weight": 3, "stages": ["build", "review", "commit"]}, {"id": "2", "label": "#2", "weight": 1}]}',
          want: {
            summary: 'orc: running, 0 of 2 steps settled, 0% of planned work.',
            file: lines(
              '{',
              '  "version": 1,',
              '  "name": "orc",',
              '  "phase": "running",',
              '  "steps": [',
              '    {',
              '      "id": "1",',
              '      "label": "#1",',
              '      "status": "pending",',
              '      "weight": 3,',
              '      "stages": [',
              '        "build",',
              '        "review",',
              '        "commit"',
              '      ]',
              '    },',
              '    {',
              '      "id": "2",',
              '      "label": "#2",',
              '      "status": "pending",',
              '      "weight": 1',
              '    }',
              '  ],',
              '  "updatedAt": "2026-10-06T12:00:00Z"',
              '}',
            ),
          },
        },
        {
          tool: 'progress_step',
          args: '{"id": "1", "stage": "review"}',
          want: {
            summary: 'orc: running, 0 of 2 steps settled, 25% of planned work.',
            file: lines(
              '{',
              '  "version": 1,',
              '  "name": "orc",',
              '  "phase": "running",',
              '  "steps": [',
              '    {',
              '      "id": "1",',
              '      "label": "#1",',
              '      "status": "active",',
              '      "weight": 3,',
              '      "stages": [',
              '        "build",',
              '        "review",',
              '        "commit"',
              '      ],',
              '      "stage": "review"',
              '    },',
              '    {',
              '      "id": "2",',
              '      "label": "#2",',
              '      "status": "pending",',
              '      "weight": 1',
              '    }',
              '  ],',
              '  "updatedAt": "2026-10-06T12:00:00Z"',
              '}',
            ),
          },
        },
        {
          tool: 'progress_label',
          args: '{"label": "review 1/3"}',
          want: {
            summary: 'orc: running, 0 of 2 steps settled, 25% of planned work.',
            file: lines(
              '{',
              '  "version": 1,',
              '  "name": "orc",',
              '  "phase": "running",',
              '  "label": "review 1/3",',
              '  "steps": [',
              '    {',
              '      "id": "1",',
              '      "label": "#1",',
              '      "status": "active",',
              '      "weight": 3,',
              '      "stages": [',
              '        "build",',
              '        "review",',
              '        "commit"',
              '      ],',
              '      "stage": "review"',
              '    },',
              '    {',
              '      "id": "2",',
              '      "label": "#2",',
              '      "status": "pending",',
              '      "weight": 1',
              '    }',
              '  ],',
              '  "updatedAt": "2026-10-06T12:00:00Z"',
              '}',
            ),
          },
        },
        {
          tool: 'progress_add_steps',
          args: '{"steps": [{"id": "fix", "label": "fix"}]}',
          want: {
            summary: 'orc: running, 0 of 3 steps settled, 20% of planned work.',
            file: lines(
              '{',
              '  "version": 1,',
              '  "name": "orc",',
              '  "phase": "running",',
              '  "label": "review 1/3",',
              '  "steps": [',
              '    {',
              '      "id": "1",',
              '      "label": "#1",',
              '      "status": "active",',
              '      "weight": 3,',
              '      "stages": [',
              '        "build",',
              '        "review",',
              '        "commit"',
              '      ],',
              '      "stage": "review"',
              '    },',
              '    {',
              '      "id": "2",',
              '      "label": "#2",',
              '      "status": "pending",',
              '      "weight": 1',
              '    },',
              '    {',
              '      "id": "fix",',
              '      "label": "fix",',
              '      "status": "pending"',
              '    }',
              '  ],',
              '  "updatedAt": "2026-10-06T12:00:00Z"',
              '}',
            ),
          },
        },
        {
          tool: 'progress_step',
          args: '{"id": "1", "status": "done"}',
          want: {
            summary: 'orc: running, 1 of 3 steps settled, 60% of planned work.',
            file: lines(
              '{',
              '  "version": 1,',
              '  "name": "orc",',
              '  "phase": "running",',
              '  "label": "review 1/3",',
              '  "steps": [',
              '    {',
              '      "id": "1",',
              '      "label": "#1",',
              '      "status": "done",',
              '      "weight": 3,',
              '      "stages": [',
              '        "build",',
              '        "review",',
              '        "commit"',
              '      ]',
              '    },',
              '    {',
              '      "id": "2",',
              '      "label": "#2",',
              '      "status": "pending",',
              '      "weight": 1',
              '    },',
              '    {',
              '      "id": "fix",',
              '      "label": "fix",',
              '      "status": "pending"',
              '    }',
              '  ],',
              '  "updatedAt": "2026-10-06T12:00:00Z"',
              '}',
            ),
          },
        },
        {
          tool: 'progress_finish',
          args: '{"outcome": "done"}',
          want: {
            summary: 'orc: done, 1 of 3 steps settled, 60% of planned work.',
            file: lines(
              '{',
              '  "version": 1,',
              '  "name": "orc",',
              '  "phase": "done",',
              '  "steps": [',
              '    {',
              '      "id": "1",',
              '      "label": "#1",',
              '      "status": "done",',
              '      "weight": 3,',
              '      "stages": [',
              '        "build",',
              '        "review",',
              '        "commit"',
              '      ]',
              '    },',
              '    {',
              '      "id": "2",',
              '      "label": "#2",',
              '      "status": "pending",',
              '      "weight": 1',
              '    },',
              '    {',
              '      "id": "fix",',
              '      "label": "fix",',
              '      "status": "pending"',
              '    }',
              '  ],',
              '  "updatedAt": "2026-10-06T12:00:00Z"',
              '}',
            ),
          },
        },
      ],
    },
    {
      name: 'halts, resumes, and finishes a planned run',
      calls: [
        {
          tool: 'progress_start',
          args: '{"name": "loop", "steps": [{"id": "1", "label": "#1", "weight": 0.5, "stages": ["build", "review"]}, {"id": "2", "label": "#2", "stages": []}, {"id": "3", "label": "<a&b>", "weight": 2}]}',
          want: {
            summary: 'loop: running, 0 of 3 steps settled, 0% of planned work.',
            file: lines(
              '{',
              '  "version": 1,',
              '  "name": "loop",',
              '  "phase": "running",',
              '  "steps": [',
              '    {',
              '      "id": "1",',
              '      "label": "#1",',
              '      "status": "pending",',
              '      "weight": 0.5,',
              '      "stages": [',
              '        "build",',
              '        "review"',
              '      ]',
              '    },',
              '    {',
              '      "id": "2",',
              '      "label": "#2",',
              '      "status": "pending"',
              '    },',
              '    {',
              '      "id": "3",',
              '      "label": "\\u003ca\\u0026b\\u003e",',
              '      "status": "pending",',
              '      "weight": 2',
              '    }',
              '  ],',
              '  "updatedAt": "2026-10-06T12:00:00Z"',
              '}',
            ),
          },
        },
        {
          tool: 'progress_step',
          args: '{"id": "1", "status": "active"}',
          want: {
            summary: 'loop: running, 0 of 3 steps settled, 0% of planned work.',
            file: lines(
              '{',
              '  "version": 1,',
              '  "name": "loop",',
              '  "phase": "running",',
              '  "steps": [',
              '    {',
              '      "id": "1",',
              '      "label": "#1",',
              '      "status": "active",',
              '      "weight": 0.5,',
              '      "stages": [',
              '        "build",',
              '        "review"',
              '      ]',
              '    },',
              '    {',
              '      "id": "2",',
              '      "label": "#2",',
              '      "status": "pending"',
              '    },',
              '    {',
              '      "id": "3",',
              '      "label": "\\u003ca\\u0026b\\u003e",',
              '      "status": "pending",',
              '      "weight": 2',
              '    }',
              '  ],',
              '  "updatedAt": "2026-10-06T12:00:00Z"',
              '}',
            ),
          },
        },
        {
          tool: 'progress_step',
          args: '{"id": "2", "status": "active"}',
          want: {
            summary: 'loop: running, 0 of 3 steps settled, 0% of planned work.',
            file: lines(
              '{',
              '  "version": 1,',
              '  "name": "loop",',
              '  "phase": "running",',
              '  "steps": [',
              '    {',
              '      "id": "1",',
              '      "label": "#1",',
              '      "status": "pending",',
              '      "weight": 0.5,',
              '      "stages": [',
              '        "build",',
              '        "review"',
              '      ]',
              '    },',
              '    {',
              '      "id": "2",',
              '      "label": "#2",',
              '      "status": "active"',
              '    },',
              '    {',
              '      "id": "3",',
              '      "label": "\\u003ca\\u0026b\\u003e",',
              '      "status": "pending",',
              '      "weight": 2',
              '    }',
              '  ],',
              '  "updatedAt": "2026-10-06T12:00:00Z"',
              '}',
            ),
          },
        },
        {
          tool: 'progress_step',
          args: '{"id": "1", "stage": "build"}',
          want: {
            summary: 'loop: running, 0 of 3 steps settled, 0% of planned work.',
            file: lines(
              '{',
              '  "version": 1,',
              '  "name": "loop",',
              '  "phase": "running",',
              '  "steps": [',
              '    {',
              '      "id": "1",',
              '      "label": "#1",',
              '      "status": "active",',
              '      "weight": 0.5,',
              '      "stages": [',
              '        "build",',
              '        "review"',
              '      ],',
              '      "stage": "build"',
              '    },',
              '    {',
              '      "id": "2",',
              '      "label": "#2",',
              '      "status": "pending"',
              '    },',
              '    {',
              '      "id": "3",',
              '      "label": "\\u003ca\\u0026b\\u003e",',
              '      "status": "pending",',
              '      "weight": 2',
              '    }',
              '  ],',
              '  "updatedAt": "2026-10-06T12:00:00Z"',
              '}',
            ),
          },
        },
        {
          tool: 'progress_label',
          args: '{"label": "wrapping up"}',
          want: {
            summary: 'loop: running, 0 of 3 steps settled, 0% of planned work.',
            file: lines(
              '{',
              '  "version": 1,',
              '  "name": "loop",',
              '  "phase": "running",',
              '  "label": "wrapping up",',
              '  "steps": [',
              '    {',
              '      "id": "1",',
              '      "label": "#1",',
              '      "status": "active",',
              '      "weight": 0.5,',
              '      "stages": [',
              '        "build",',
              '        "review"',
              '      ],',
              '      "stage": "build"',
              '    },',
              '    {',
              '      "id": "2",',
              '      "label": "#2",',
              '      "status": "pending"',
              '    },',
              '    {',
              '      "id": "3",',
              '      "label": "\\u003ca\\u0026b\\u003e",',
              '      "status": "pending",',
              '      "weight": 2',
              '    }',
              '  ],',
              '  "updatedAt": "2026-10-06T12:00:00Z"',
              '}',
            ),
          },
        },
        {
          tool: 'progress_finish',
          args: '{"outcome": "halted"}',
          want: {
            summary: 'loop: halted, 0 of 3 steps settled, 0% of planned work.',
            file: lines(
              '{',
              '  "version": 1,',
              '  "name": "loop",',
              '  "phase": "halted",',
              '  "steps": [',
              '    {',
              '      "id": "1",',
              '      "label": "#1",',
              '      "status": "active",',
              '      "weight": 0.5,',
              '      "stages": [',
              '        "build",',
              '        "review"',
              '      ],',
              '      "stage": "build"',
              '    },',
              '    {',
              '      "id": "2",',
              '      "label": "#2",',
              '      "status": "pending"',
              '    },',
              '    {',
              '      "id": "3",',
              '      "label": "\\u003ca\\u0026b\\u003e",',
              '      "status": "pending",',
              '      "weight": 2',
              '    }',
              '  ],',
              '  "updatedAt": "2026-10-06T12:00:00Z"',
              '}',
            ),
          },
        },
        {
          tool: 'progress_step',
          args: '{"id": "3", "status": "skipped"}',
          want: {
            summary: 'loop: running, 1 of 3 steps settled, 0% of planned work.',
            file: lines(
              '{',
              '  "version": 1,',
              '  "name": "loop",',
              '  "phase": "running",',
              '  "steps": [',
              '    {',
              '      "id": "1",',
              '      "label": "#1",',
              '      "status": "active",',
              '      "weight": 0.5,',
              '      "stages": [',
              '        "build",',
              '        "review"',
              '      ],',
              '      "stage": "build"',
              '    },',
              '    {',
              '      "id": "2",',
              '      "label": "#2",',
              '      "status": "pending"',
              '    },',
              '    {',
              '      "id": "3",',
              '      "label": "\\u003ca\\u0026b\\u003e",',
              '      "status": "skipped",',
              '      "weight": 2',
              '    }',
              '  ],',
              '  "updatedAt": "2026-10-06T12:00:00Z"',
              '}',
            ),
          },
        },
        {
          tool: 'progress_label',
          args: '{"label": ""}',
          want: {
            summary: 'loop: running, 1 of 3 steps settled, 0% of planned work.',
            file: lines(
              '{',
              '  "version": 1,',
              '  "name": "loop",',
              '  "phase": "running",',
              '  "steps": [',
              '    {',
              '      "id": "1",',
              '      "label": "#1",',
              '      "status": "active",',
              '      "weight": 0.5,',
              '      "stages": [',
              '        "build",',
              '        "review"',
              '      ],',
              '      "stage": "build"',
              '    },',
              '    {',
              '      "id": "2",',
              '      "label": "#2",',
              '      "status": "pending"',
              '    },',
              '    {',
              '      "id": "3",',
              '      "label": "\\u003ca\\u0026b\\u003e",',
              '      "status": "skipped",',
              '      "weight": 2',
              '    }',
              '  ],',
              '  "updatedAt": "2026-10-06T12:00:00Z"',
              '}',
            ),
          },
        },
        {
          tool: 'progress_step',
          args: '{"id": "1", "status": "blocked"}',
          want: {
            summary: 'loop: running, 2 of 3 steps settled, 0% of planned work.',
            file: lines(
              '{',
              '  "version": 1,',
              '  "name": "loop",',
              '  "phase": "running",',
              '  "steps": [',
              '    {',
              '      "id": "1",',
              '      "label": "#1",',
              '      "status": "blocked",',
              '      "weight": 0.5,',
              '      "stages": [',
              '        "build",',
              '        "review"',
              '      ]',
              '    },',
              '    {',
              '      "id": "2",',
              '      "label": "#2",',
              '      "status": "pending"',
              '    },',
              '    {',
              '      "id": "3",',
              '      "label": "\\u003ca\\u0026b\\u003e",',
              '      "status": "skipped",',
              '      "weight": 2',
              '    }',
              '  ],',
              '  "updatedAt": "2026-10-06T12:00:00Z"',
              '}',
            ),
          },
        },
        {
          tool: 'progress_finish',
          args: '{"outcome": "done"}',
          want: {
            summary: 'loop: done, 2 of 3 steps settled, 0% of planned work.',
            file: lines(
              '{',
              '  "version": 1,',
              '  "name": "loop",',
              '  "phase": "done",',
              '  "steps": [',
              '    {',
              '      "id": "1",',
              '      "label": "#1",',
              '      "status": "blocked",',
              '      "weight": 0.5,',
              '      "stages": [',
              '        "build",',
              '        "review"',
              '      ]',
              '    },',
              '    {',
              '      "id": "2",',
              '      "label": "#2",',
              '      "status": "pending"',
              '    },',
              '    {',
              '      "id": "3",',
              '      "label": "\\u003ca\\u0026b\\u003e",',
              '      "status": "skipped",',
              '      "weight": 2',
              '    }',
              '  ],',
              '  "updatedAt": "2026-10-06T12:00:00Z"',
              '}',
            ),
          },
        },
      ],
    },
    {
      name: 'answers invalid calls with the Go errors',
      calls: [
        {
          tool: 'progress_start',
          args: '{"name": "orc", "steps": [{"id": "1", "label": "#1", "stages": ["build"]}]}',
          want: {
            summary: 'orc: running, 0 of 1 steps settled, 0% of planned work.',
            file: lines(
              '{',
              '  "version": 1,',
              '  "name": "orc",',
              '  "phase": "running",',
              '  "steps": [',
              '    {',
              '      "id": "1",',
              '      "label": "#1",',
              '      "status": "pending",',
              '      "stages": [',
              '        "build"',
              '      ]',
              '    }',
              '  ],',
              '  "updatedAt": "2026-10-06T12:00:00Z"',
              '}',
            ),
          },
        },
        {
          tool: 'progress_step',
          args: '{"id": "1", "status": "waiting"}',
          want: { error: 'status must be one of [pending active done skipped blocked]' },
        },
        {
          tool: 'progress_step',
          args: '{"id": "9", "status": "done"}',
          want: { error: 'no step with id "9"' },
        },
        {
          tool: 'progress_step',
          args: '{"id": "1", "stage": "ship"}',
          want: { error: 'stage "ship" is not one of step "1"\'s stages [build]' },
        },
        {
          tool: 'progress_label',
          args: '{"label": "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"}',
          want: { error: 'label must be at most 40 characters' },
        },
        {
          tool: 'progress_step',
          args: '{"id": 1}',
          want: { error: 'invalid arguments: json: cannot unmarshal number into Go struct field .ID of type string' },
        },
        {
          tool: 'progress_add_steps',
          args: '{"steps": [{"id": "1", "label": "again"}]}',
          want: { error: 'step id "1" is used twice' },
        },
        {
          tool: 'progress_add_steps',
          args: '{"steps": []}',
          want: { error: 'give at least one step' },
        },
        {
          tool: 'progress_finish',
          args: '{"outcome": "cancelled"}',
          want: { error: 'outcome must be "done" or "halted"' },
        },
        {
          tool: 'progress_start',
          args: '{"name": "orc", "steps": []}',
          want: { error: 'a run needs 1 to 50 steps' },
        },
        {
          tool: 'progress_start',
          args: '{"name": "orc", "steps": [{"id": "1", "label": "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"}]}',
          want: { error: 'step "1": label must be at most 40 characters' },
        },
        {
          tool: 'progress_step',
          args: '{"id": "1"}',
          want: { error: 'give a status, a stage, or both' },
        },
        {
          tool: 'progress_step',
          args: '{"id": "1", "status": "done", "stage": "build"}',
          want: { error: 'only an active step has a current stage' },
        },
        {
          tool: 'progress_start',
          args: ARGS.fiftyOneSteps,
          want: { error: 'a run needs 1 to 50 steps' },
        },
        {
          tool: 'progress_add_steps',
          args: ARGS.fortyNineMore,
          want: { summary: 'orc: running, 0 of 50 steps settled, 0% of planned work.' },
        },
        {
          tool: 'progress_add_steps',
          args: '{"steps": [{"id": "over", "label": "a"}]}',
          want: { error: 'a run holds at most 50 steps' },
        },
        {
          tool: 'progress_step',
          args: '{"id": "x\\ny\\u00a0\\u2028\u00e9\\u0007", "status": "done"}',
          want: { error: 'no step with id "x\\ny\\u00a0\\u2028\u00e9\\a"' },
        },
        {
          tool: 'progress_start',
          args: '{"steps": [{"weight": "x"}], "name": 5}',
          want: { error: 'invalid arguments: json: cannot unmarshal string into Go struct field NewStep.steps.weight of type float64' },
        },
        {
          tool: 'progress_start',
          args: '{"name": "", "steps": [{"id": "1", "label": "a"}]}',
          want: { error: 'name must not be empty' },
        },
        {
          tool: 'progress_start',
          args: '{"name": "orc", "steps": [{"id": "1"}, {"id": "1"}]}',
          want: { error: 'step id "1" is used twice' },
        },
        {
          tool: 'progress_start',
          args: '{"name": "orc", "steps": [{"id": "", "label": "a"}]}',
          want: { error: 'step "": id must not be empty' },
        },
        {
          tool: 'progress_start',
          args: '{"name": "orc", "steps": [{"id": "1", "weight": -1}]}',
          want: { error: 'step "1": weight must be between 0 and 1000' },
        },
        {
          tool: 'progress_start',
          args: '{"name": "orc", "steps": [{"id": "1", "weight": 1001}]}',
          want: { error: 'step "1": weight must be between 0 and 1000' },
        },
        {
          tool: 'progress_start',
          args: '{"name": "orc", "steps": [{"id": "1", "stages": ["a","b","c","d","e","f","g","h","i","j","k","l","m"]}]}',
          want: { error: 'step "1": at most 12 stages' },
        },
        {
          tool: 'progress_start',
          args: '{"name": "orc", "steps": [{"id": "1", "stages": [""]}]}',
          want: { error: 'step "1": stage must not be empty' },
        },
        {
          tool: 'progress_start',
          args: '{"name": "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", "steps": [{"id": "1"}]}',
          want: { error: 'name must be at most 40 characters' },
        },
        {
          tool: 'progress_start',
          args: ARGS.oversized,
          want: { error: 'the run is too large to save; use fewer or shorter steps' },
        },
        {
          tool: 'progress_start',
          args: '{"name": 5, "steps": []}',
          want: { error: 'invalid arguments: json: cannot unmarshal number into Go struct field .name of type string' },
        },
        {
          tool: 'progress_start',
          args: '{"name": "orc", "steps": [{"id": "1", "weight": "2"}]}',
          want: { error: 'invalid arguments: json: cannot unmarshal string into Go struct field NewStep.steps.weight of type float64' },
        },
        {
          tool: 'progress_start',
          args: '{"name": "orc", "steps": [{"id": "1", "stages": "a"}]}',
          want: { error: 'invalid arguments: json: cannot unmarshal string into Go struct field NewStep.steps.stages of type []string' },
        },
        {
          tool: 'progress_start',
          args: '{"name": "orc", "steps": [{"id": "1", "stages": [1]}]}',
          want: { error: 'invalid arguments: json: cannot unmarshal number into Go struct field NewStep.steps.stages of type string' },
        },
        {
          tool: 'progress_start',
          args: '{"name": "orc", "steps": {}}',
          want: { error: 'invalid arguments: json: cannot unmarshal object into Go struct field .steps of type []progress.NewStep' },
        },
        {
          tool: 'progress_start',
          args: '{"name": "orc", "steps": ["x"]}',
          want: { error: 'invalid arguments: json: cannot unmarshal string into Go struct field .steps of type progress.NewStep' },
        },
        {
          tool: 'progress_start',
          args: '[]',
          want: { error: 'invalid arguments: json: cannot unmarshal array into Go value of type struct { Name string "json:\\"name\\""; Steps []progress.NewStep "json:\\"steps\\"" }' },
        },
        {
          tool: 'progress_label',
          args: '{"label": true}',
          want: { error: 'invalid arguments: json: cannot unmarshal bool into Go struct field .Label of type string' },
        },
        {
          tool: 'progress_finish',
          args: '{"outcome": null}',
          want: { error: 'outcome must be "done" or "halted"' },
        },
      ],
    },
    {
      name: 'needs a run before any call but start',
      calls: [
        {
          tool: 'progress_step',
          args: '{"id": "1", "status": "done"}',
          want: { error: 'no run in progress; start one first' },
        },
        {
          tool: 'progress_label',
          args: '{"label": "x"}',
          want: { error: 'no run in progress; start one first' },
        },
        {
          tool: 'progress_add_steps',
          args: '{"steps": [{"id": "1", "label": "a"}]}',
          want: { error: 'no run in progress; start one first' },
        },
        {
          tool: 'progress_finish',
          args: '{"outcome": "done"}',
          want: { error: 'no run in progress; start one first' },
        },
      ],
    },
    {
      name: 'formats ids and stages with spaces as Go does',
      calls: [
        {
          tool: 'progress_start',
          args: '{"name": "sp", "steps": [{"id": "a b", "label": "x", "stages": ["one two", "three"]}]}',
          want: {
            summary: 'sp: running, 0 of 1 steps settled, 0% of planned work.',
            file: lines(
              '{',
              '  "version": 1,',
              '  "name": "sp",',
              '  "phase": "running",',
              '  "steps": [',
              '    {',
              '      "id": "a b",',
              '      "label": "x",',
              '      "status": "pending",',
              '      "stages": [',
              '        "one two",',
              '        "three"',
              '      ]',
              '    }',
              '  ],',
              '  "updatedAt": "2026-10-06T12:00:00Z"',
              '}',
            ),
          },
        },
        {
          tool: 'progress_step',
          args: '{"id": "a b", "stage": "four"}',
          want: { error: 'stage "four" is not one of step "a b"\'s stages [one two three]' },
        },
        {
          tool: 'progress_start',
          args: '{"name": "dup", "label": "x", "steps": [{"id": "1", "label": null}], "Name": null}',
          want: {
            summary: 'dup: running, 0 of 1 steps settled.',
            file: lines(
              '{',
              '  "version": 1,',
              '  "name": "dup",',
              '  "phase": "running",',
              '  "steps": [',
              '    {',
              '      "id": "1",',
              '      "label": "",',
              '      "status": "pending"',
              '    }',
              '  ],',
              '  "updatedAt": "2026-10-06T12:00:00Z"',
              '}',
            ),
          },
        },
      ],
    },
    {
      name: 'escapes strings and matches keys as Go does',
      calls: [
        {
          tool: 'progress_start',
          args: '{"name": "q\\"\\\\ \\u2028 \\u0001\\b\\f\\n\\r\\t\\u007f caf\u00e9 \\ud800", "steps": [{"ID": "A", "Label": null, "weight": 1e-7, "stages": null, "extra": 1}], "cwd": 5}',
          want: {
            summary: 'q"\\ \u2028 \u0001\u0008\u000c\n\u000d\t\u007f caf\u00e9 \ufffd: running, 0 of 1 steps settled, 0% of planned work.',
            file: lines(
              '{',
              '  "version": 1,',
              '  "name": "q\\"\\\\ \\u2028 \\u0001\\b\\f\\n\\r\\t\u007f caf\u00e9 \ufffd",',
              '  "phase": "running",',
              '  "steps": [',
              '    {',
              '      "id": "A",',
              '      "label": "",',
              '      "status": "pending",',
              '      "weight": 1e-7',
              '    }',
              '  ],',
              '  "updatedAt": "2026-10-06T12:00:00Z"',
              '}',
            ),
          },
        },
        {
          tool: 'progress_step',
          args: '{"Id": "A", "STATUS": "active", "status": "done"}',
          want: {
            summary: 'q"\\ \u2028 \u0001\u0008\u000c\n\u000d\t\u007f caf\u00e9 \ufffd: running, 1 of 1 steps settled, 100% of planned work.',
            file: lines(
              '{',
              '  "version": 1,',
              '  "name": "q\\"\\\\ \\u2028 \\u0001\\b\\f\\n\\r\\t\u007f caf\u00e9 \ufffd",',
              '  "phase": "running",',
              '  "steps": [',
              '    {',
              '      "id": "A",',
              '      "label": "",',
              '      "status": "done",',
              '      "weight": 1e-7',
              '    }',
              '  ],',
              '  "updatedAt": "2026-10-06T12:00:00Z"',
              '}',
            ),
          },
        },
      ],
    },
    {
      name: 'decodes null steps and arguments as zero values',
      calls: [
        {
          tool: 'progress_start',
          args: '{"name": "n", "steps": [null]}',
          want: { error: 'step "": id must not be empty' },
        },
        {
          tool: 'progress_start',
          args: 'null',
          want: { error: 'name must not be empty' },
        },
      ],
    },
  ]
}
