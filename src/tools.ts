/**
 * The tools the mod serves in place of the Go paceline-mcp server: the same
 * names, descriptions, and input schemas, so agents that call
 * `mcp__paceline__<name>` keep working. hooks/register.ts registers and
 * answers them.
 */

import type { ToolSpec } from 'claude-code'

import type { ProgressTool } from './progress.ts'

/** The name of a usage tool, as paceline-mcp serves it. */
export type UsageTool = 'get_usage' | 'get_today_budget'

/** A tool's spec, with its name narrowed to the tools of one kind. */
export type NamedToolSpec<N extends string> = ToolSpec & { name: N }

/** The prefix of every tool name the model calls, for a plugin or MCP server named `paceline`. */
export const TOOL_PREFIX = 'mcp__paceline__'

// The optional argument every progress tool takes.
const CWD_PROPERTY = {
  type: 'string',
  description:
    "Directory inside the repository the run belongs to. Defaults to the server's working directory, which is normally the project.",
}

const STEPS_PROPERTY = {
  type: 'array',
  minItems: 1,
  maxItems: 50,
  items: {
    type: 'object',
    required: ['id', 'label'],
    properties: {
      id: {
        type: 'string',
        description: 'Unique id used to update the step later, such as an issue number or task number.',
      },
      label: {
        type: 'string',
        description:
          'Short text shown while the step is active, such as "#41" or "auth". At most 40 characters; keep it under 12.',
      },
      weight: {
        type: 'number',
        minimum: 0,
        maximum: 1000,
        description: 'Planned size relative to the other steps. Omit for 1.',
      },
      stages: {
        type: 'array',
        maxItems: 12,
        items: { type: 'string' },
        description: 'Stages the step will pass through, in order, such as ["design", "build", "review", "commit"].',
      },
    },
  },
}

// Go's schema helper: an object with the given required keys, and `cwd`
// added to its properties.
function progressSchema(required: string[], properties: Record<string, unknown>): Record<string, unknown> {
  return { type: 'object', required, properties: { ...properties, cwd: CWD_PROPERTY } }
}

/** The five progress tools, in paceline-mcp's order. */
export const PROGRESS_TOOL_SPECS: readonly NamedToolSpec<ProgressTool>[] = [
  {
    name: 'progress_start',
    description:
      "Start a progress bar for a long job, replacing any earlier run in this session. Call once, after planning, with every step you expect in order; every step starts pending. Give steps weights and stages so the bar's percentage is accurate.",
    inputSchema: progressSchema(['name', 'steps'], {
      name: {
        type: 'string',
        description: 'Short name for the run, shown before the bar, such as "loop" or "orc".',
      },
      steps: STEPS_PROPERTY,
    }),
  },
  {
    name: 'progress_step',
    description:
      'Update one step: mark it active when you start it, move it to its next stage, or mark it done, skipped, or blocked when it ends. Activating a step returns any other active step to pending.',
    inputSchema: progressSchema(['id'], {
      id: { type: 'string', description: "The step's id from progress_start." },
      status: {
        type: 'string',
        enum: ['active', 'done', 'skipped', 'blocked', 'pending'],
        description: 'New status. Omit when only the stage changes.',
      },
      stage: {
        type: 'string',
        description: 'The stage the step is in now, one of its planned stages. Implies active.',
      },
    }),
  },
  {
    name: 'progress_label',
    description: `Set the bar's status text for work that is not a step, such as "review 2/3" or "planning". An empty label returns to showing the active step.`,
    inputSchema: progressSchema(['label'], {
      label: { type: 'string', description: 'Status text, at most 40 characters. Empty to clear.' },
    }),
  },
  {
    name: 'progress_add_steps',
    description:
      'Append steps to the current run when the plan grows, such as a fix pass after review or a refilled batch slot.',
    inputSchema: progressSchema(['steps'], { steps: STEPS_PROPERTY }),
  },
  {
    name: 'progress_finish',
    description:
      'End the run: "done" when the job finished, "halted" when it stopped early and needs a person. A done run disappears from the status line 30 minutes later.',
    inputSchema: progressSchema(['outcome'], { outcome: { type: 'string', enum: ['done', 'halted'] } }),
  },
]

// Both usage tools' descriptions open with this, so the model checks the
// budget before work that could use up a lot of it.
const WHEN_TO_CALL = 'Call before starting a long or expensive task, or a batch of subagents, '

/** The two usage tools, in paceline-mcp's order. Neither takes arguments. */
export const USAGE_TOOL_SPECS: readonly NamedToolSpec<UsageTool>[] = [
  {
    name: 'get_usage',
    description:
      WHEN_TO_CALL +
      'or when asked about Claude usage limits, to check how much of the five-hour session and the weekly limit is used and left and when each resets. Read-only.',
    inputSchema: { type: 'object', properties: {} },
  },
  {
    name: 'get_today_budget',
    description:
      WHEN_TO_CALL +
      "to check how much of today's Claude usage budget is left. Returns today's share of the weekly limit as the status line shows it: percent of today's budget used and left, whether it is over, and whether today's budget is larger or smaller than an even share of the week. Read-only.",
    inputSchema: { type: 'object', properties: {} },
  },
]
