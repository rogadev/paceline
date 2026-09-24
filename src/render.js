// Builds the status line from Claude Code's status JSON payload.

import { basename } from 'node:path';
import { createStyle } from './ansi.js';
import { headroomLevel, projectSlot, slotColor } from './color.js';
import { computeDailyPace } from './pace.js';
import { safeNumber, safeText } from './sanitize.js';
import { formatClock, formatDuration } from './time.js';

const ARROWS = { up: '▲', even: '●', down: '▼' };
const HOURGLASS = '⏳';
const MIDDLE_DOT = '·';

/** Last path segment, for either separator style. */
function leaf(path) {
  return basename(path.replace(/[\\/]+$/, '').replace(/\\/g, '/'));
}

/**
 * @param {object} payload  parsed status JSON from Claude Code
 * @param {object} ctx
 * @param {Date} ctx.now
 * @param {object} ctx.config          merged config (see config.js)
 * @param {function} [ctx.readSnapshot]  () => snapshot | null
 * @param {function} [ctx.writeSnapshot] (snapshot) => void
 * @param {function} [ctx.gitBranch]     (dir) => string | null
 * @param {object} [ctx.env]             for NO_COLOR
 * @returns {string}
 */
export function render(payload, ctx) {
  const { now, config } = ctx;
  const on = config.segments;
  const t = config.thresholds;
  const style = createStyle(ctx.env);
  const colorFor = (left) => style[headroomLevel(left, { green: t.headroomGreen, yellow: t.headroomYellow })];
  const p = payload && typeof payload === 'object' ? payload : {};
  const parts = [];

  // Model, minus any "(1M context)"-style suffix.
  const model = safeText(p.model?.display_name).replace(/\s*\(.*\)$/, '');
  if (on.model && model) parts.push(style.cyan(model));

  // Effort, only above the everyday levels.
  const effort = safeText(p.effort?.level, 16);
  if (on.effort && effort && !config.quietEfforts.includes(effort)) {
    parts.push(style.dim(`${effort} effort`));
  }

  if (on.fastMode && p.fast_mode === true) parts.push(style.yellow('fast mode'));

  // Folder, colored by project; the color comes from the project root so a
  // subfolder keeps its project's color.
  const dir = typeof p.workspace?.current_dir === 'string' ? p.workspace.current_dir : p.cwd;
  if (on.project && typeof dir === 'string' && dir) {
    const projectDir =
      typeof p.workspace?.project_dir === 'string' && p.workspace.project_dir ? p.workspace.project_dir : dir;
    const name = safeText(leaf(dir));
    const [r, g, b] = slotColor(projectSlot(leaf(projectDir), config.projectSlots));
    let segment = style.rgb(r, g, b)(name);
    if (on.branch && ctx.gitBranch) {
      let branch = null;
      try {
        branch = safeText(ctx.gitBranch(dir), 40);
      } catch {
        // unreadable repo: no branch
      }
      if (branch) segment += ` ${style.dim('(')}${branch}${style.dim(')')}`;
    }
    parts.push(segment);
  }

  // Usage limits: session, week, then today's share of the week.
  const fiveHour = p.rate_limits?.five_hour;
  const sessionUsed = safeNumber(fiveHour?.used_percentage);
  if (on.session && sessionUsed !== null) {
    const left = Math.round(100 - sessionUsed);
    const resetsAt = safeNumber(fiveHour.resets_at);
    const when = left < t.headroomGreen && resetsAt !== null ? ` (resets ${formatClock(resetsAt, now)})` : '';
    parts.push(colorFor(left)(`${left}% session${when}`));
  }

  const sevenDay = p.rate_limits?.seven_day;
  const weekUsed = safeNumber(sevenDay?.used_percentage);
  if (on.week && weekUsed !== null) {
    parts.push(colorFor(Math.round(100 - weekUsed))(`${Math.round(100 - weekUsed)}% week`));
  }
  const weekResets = safeNumber(sevenDay?.resets_at);
  if (on.today && weekUsed !== null && weekResets !== null) {
    const today = renderToday(weekUsed, weekResets, ctx, style, colorFor);
    if (today) parts.push(today);
  }

  // Context-window warning, silent while there is headroom.
  const ctxUsed = safeNumber(p.context_window?.used_percentage);
  if (on.context && ctxUsed !== null) {
    const used = Math.round(ctxUsed);
    if (used >= t.contextCritical) parts.push(style.red(`ctx ${used}%`));
    else if (used >= t.contextWarn) parts.push(style.yellow(`ctx ${used}%`));
  }

  // Prompt-cache warning, only once the cache has gone cold.
  const expiresAt = safeNumber(p.prompt_cache?.expires_at);
  if (on.cache && expiresAt !== null && expiresAt <= now.getTime() / 1000) {
    const recache = safeNumber(p.prompt_cache.recache_tokens_if_cold);
    parts.push(style.red(recache !== null ? `cache cold: ${Math.round(recache / 1000)}k @ ~2x` : 'cache cold: ~2x'));
  }

  const durationMs = safeNumber(p.cost?.total_duration_ms);
  if (on.duration && durationMs !== null && durationMs > 0) parts.push(style.dim(formatDuration(durationMs)));

  return parts.join(` ${style.dim(MIDDLE_DOT)} `);
}

function renderToday(usedPct, resetsAt, ctx, style, colorFor) {
  const snapshot = ctx.readSnapshot ? ctx.readSnapshot() : null;
  const pace = computeDailyPace({ usedPct, resetsAt, now: ctx.now, snapshot });
  if (pace.kind === 'none') return null;
  if (pace.kind === 'lastDay') {
    return style.cyan(`${HOURGLASS} last day, resets ${formatClock(pace.resetsAt, ctx.now)}`);
  }
  if (pace.snapshotChanged && ctx.writeSnapshot) ctx.writeSnapshot(pace.snapshot);

  const arrow = ARROWS[pace.pace];
  const budget = Math.round(pace.budget);
  // Past the budget, flip from "% left" to "% used" so the overshoot shows (102%).
  if (pace.over) return style.red(`${arrow} ${Math.round(pace.pctUsed)}% used of today's ${budget}% budget`);
  const left = Math.round(pace.pctLeft);
  return colorFor(left)(`${arrow} ${left}% left of today's ${budget}% budget`);
}
