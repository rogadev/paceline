import { describe, expect, test } from 'claude-code/testing'

import { PROGRESS_TOOL_SPECS, TOOL_PREFIX, USAGE_TOOL_SPECS } from '../src/tools.ts'

// The Go paceline-mcp's tools/list answer (rogadev/paceline at 7a480ff),
// recorded by sending its server an initialize and a tools/list request.
const GO_TOOLS_LIST = "[{\"description\":\"Start a progress bar for a long job, replacing any earlier run in this session. Call once, after planning, with every step you expect in order; every step starts pending. Give steps weights and stages so the bar's percentage is accurate.\",\"inputSchema\":{\"type\":\"object\",\"required\":[\"name\",\"steps\"],\"properties\":{\"name\":{\"type\":\"string\",\"description\":\"Short name for the run, shown before the bar, such as \\\"loop\\\" or \\\"orc\\\".\"},\"steps\":{\"type\":\"array\",\"minItems\":1,\"maxItems\":50,\"items\":{\"type\":\"object\",\"required\":[\"id\",\"label\"],\"properties\":{\"id\":{\"type\":\"string\",\"description\":\"Unique id used to update the step later, such as an issue number or task number.\"},\"label\":{\"type\":\"string\",\"description\":\"Short text shown while the step is active, such as \\\"#41\\\" or \\\"auth\\\". At most 40 characters; keep it under 12.\"},\"weight\":{\"type\":\"number\",\"minimum\":0,\"maximum\":1000,\"description\":\"Planned size relative to the other steps. Omit for 1.\"},\"stages\":{\"type\":\"array\",\"maxItems\":12,\"items\":{\"type\":\"string\"},\"description\":\"Stages the step will pass through, in order, such as [\\\"design\\\", \\\"build\\\", \\\"review\\\", \\\"commit\\\"].\"}}}},\"cwd\":{\"type\":\"string\",\"description\":\"Directory inside the repository the run belongs to. Defaults to the server's working directory, which is normally the project.\"}}},\"name\":\"progress_start\"},{\"description\":\"Update one step: mark it active when you start it, move it to its next stage, or mark it done, skipped, or blocked when it ends. Activating a step returns any other active step to pending.\",\"inputSchema\":{\"type\":\"object\",\"required\":[\"id\"],\"properties\":{\"id\":{\"type\":\"string\",\"description\":\"The step's id from progress_start.\"},\"status\":{\"type\":\"string\",\"enum\":[\"active\",\"done\",\"skipped\",\"blocked\",\"pending\"],\"description\":\"New status. Omit when only the stage changes.\"},\"stage\":{\"type\":\"string\",\"description\":\"The stage the step is in now, one of its planned stages. Implies active.\"},\"cwd\":{\"type\":\"string\",\"description\":\"Directory inside the repository the run belongs to. Defaults to the server's working directory, which is normally the project.\"}}},\"name\":\"progress_step\"},{\"description\":\"Set the bar's status text for work that is not a step, such as \\\"review 2/3\\\" or \\\"planning\\\". An empty label returns to showing the active step.\",\"inputSchema\":{\"type\":\"object\",\"required\":[\"label\"],\"properties\":{\"label\":{\"type\":\"string\",\"description\":\"Status text, at most 40 characters. Empty to clear.\"},\"cwd\":{\"type\":\"string\",\"description\":\"Directory inside the repository the run belongs to. Defaults to the server's working directory, which is normally the project.\"}}},\"name\":\"progress_label\"},{\"description\":\"Append steps to the current run when the plan grows, such as a fix pass after review or a refilled batch slot.\",\"inputSchema\":{\"type\":\"object\",\"required\":[\"steps\"],\"properties\":{\"steps\":{\"type\":\"array\",\"minItems\":1,\"maxItems\":50,\"items\":{\"type\":\"object\",\"required\":[\"id\",\"label\"],\"properties\":{\"id\":{\"type\":\"string\",\"description\":\"Unique id used to update the step later, such as an issue number or task number.\"},\"label\":{\"type\":\"string\",\"description\":\"Short text shown while the step is active, such as \\\"#41\\\" or \\\"auth\\\". At most 40 characters; keep it under 12.\"},\"weight\":{\"type\":\"number\",\"minimum\":0,\"maximum\":1000,\"description\":\"Planned size relative to the other steps. Omit for 1.\"},\"stages\":{\"type\":\"array\",\"maxItems\":12,\"items\":{\"type\":\"string\"},\"description\":\"Stages the step will pass through, in order, such as [\\\"design\\\", \\\"build\\\", \\\"review\\\", \\\"commit\\\"].\"}}}},\"cwd\":{\"type\":\"string\",\"description\":\"Directory inside the repository the run belongs to. Defaults to the server's working directory, which is normally the project.\"}}},\"name\":\"progress_add_steps\"},{\"description\":\"End the run: \\\"done\\\" when the job finished, \\\"halted\\\" when it stopped early and needs a person. A done run disappears from the status line 30 minutes later.\",\"inputSchema\":{\"type\":\"object\",\"required\":[\"outcome\"],\"properties\":{\"outcome\":{\"type\":\"string\",\"enum\":[\"done\",\"halted\"]},\"cwd\":{\"type\":\"string\",\"description\":\"Directory inside the repository the run belongs to. Defaults to the server's working directory, which is normally the project.\"}}},\"name\":\"progress_finish\"},{\"description\":\"Call before starting a long or expensive task, or a batch of subagents, or when asked about Claude usage limits, to check how much of the five-hour session and the weekly limit is used and left and when each resets. Read-only.\",\"inputSchema\":{\"type\":\"object\",\"properties\":{}},\"name\":\"get_usage\"},{\"description\":\"Call before starting a long or expensive task, or a batch of subagents, to check how much of today's Claude usage budget is left. Returns today's share of the weekly limit as the status line shows it: percent of today's budget used and left, whether it is over, and whether today's budget is larger or smaller than an even share of the week. Read-only.\",\"inputSchema\":{\"type\":\"object\",\"properties\":{}},\"name\":\"get_today_budget\"}]"


type GoTool = { name: string; description: string; inputSchema: Record<string, unknown> }

const goTools: GoTool[] = JSON.parse(GO_TOOLS_LIST)

describe('tool specs', () => {
  test('match the Go server tool for tool, in its order', () => {
    const ours = [...PROGRESS_TOOL_SPECS, ...USAGE_TOOL_SPECS].map(s => ({
      name: s.name,
      description: s.description,
      inputSchema: s.inputSchema,
    }))
    expect(ours).toEqual(goTools)
  })

  test('give every progress tool the optional cwd argument', () => {
    for (const spec of PROGRESS_TOOL_SPECS) {
      const schema = spec.inputSchema as { required: string[]; properties: Record<string, unknown> }
      expect(Object.keys(schema.properties)).toContain('cwd')
      expect(schema.required).not.toContain('cwd')
    }
  })

  test('name the tools the model calls under the paceline prefix', () => {
    expect(TOOL_PREFIX + PROGRESS_TOOL_SPECS[0].name).toBe('mcp__paceline__progress_start')
  })
})
