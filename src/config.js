// User configuration: defaults merged with ~/.claude/paceline.json.

import { readFileSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';

const MAX_CONFIG_BYTES = 64 * 1024;

export const DEFAULT_CONFIG = Object.freeze({
  segments: Object.freeze({
    model: true,
    effort: true,
    fastMode: true,
    project: true,
    branch: true,
    session: true,
    week: true,
    today: true,
    context: true,
    cache: true,
    duration: true,
  }),
  thresholds: Object.freeze({
    headroomGreen: 30,
    headroomYellow: 15,
    contextWarn: 70,
    contextCritical: 85,
  }),
  // Effort levels considered "everyday"; anything else is shown.
  quietEfforts: Object.freeze(['low', 'medium']),
  // Project folder name -> color slot 0-11, for projects that hash to the same color.
  projectSlots: Object.freeze({}),
});

/** Claude Code's config directory, honoring CLAUDE_CONFIG_DIR. */
export function claudeDir(env = process.env) {
  return env.CLAUDE_CONFIG_DIR || join(homedir(), '.claude');
}

const isPlainObject = (v) => v !== null && typeof v === 'object' && !Array.isArray(v);
const isPercent = (v) => typeof v === 'number' && Number.isFinite(v) && v >= 0 && v <= 100;

/**
 * Merge a parsed user config onto the defaults. Only known keys with the right
 * type are taken, so a malformed file degrades to defaults instead of breaking
 * rendering, and keys like __proto__ can never reach an object prototype.
 */
export function mergeConfig(user) {
  const out = {
    segments: { ...DEFAULT_CONFIG.segments },
    thresholds: { ...DEFAULT_CONFIG.thresholds },
    quietEfforts: [...DEFAULT_CONFIG.quietEfforts],
    projectSlots: Object.create(null),
  };
  if (!isPlainObject(user)) return out;

  if (isPlainObject(user.segments)) {
    for (const key of Object.keys(DEFAULT_CONFIG.segments)) {
      if (typeof user.segments[key] === 'boolean') out.segments[key] = user.segments[key];
    }
  }
  if (isPlainObject(user.thresholds)) {
    for (const key of Object.keys(DEFAULT_CONFIG.thresholds)) {
      if (isPercent(user.thresholds[key])) out.thresholds[key] = user.thresholds[key];
    }
  }
  if (Array.isArray(user.quietEfforts) && user.quietEfforts.every((v) => typeof v === 'string')) {
    out.quietEfforts = [...user.quietEfforts];
  }
  if (isPlainObject(user.projectSlots)) {
    for (const [name, slot] of Object.entries(user.projectSlots)) {
      if (Number.isInteger(slot) && slot >= 0 && slot < 12) out.projectSlots[name] = slot;
    }
  }
  return out;
}

/** Load and merge the config file; any read or parse problem falls back to defaults. */
export function loadConfig(path = join(claudeDir(), 'paceline.json'), readFile = readFileSync) {
  try {
    const text = readFile(path, 'utf8');
    if (text.length > MAX_CONFIG_BYTES) return mergeConfig(null);
    return mergeConfig(JSON.parse(text));
  } catch {
    return mergeConfig(null);
  }
}
