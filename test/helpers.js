// Shared test helpers.

import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { mergeConfig } from '../src/config.js';

/** Epoch seconds for a local date/time (month is 1-based). */
export function localEpoch(year, month, day, hour = 0, minute = 0) {
  return new Date(year, month - 1, day, hour, minute).getTime() / 1000;
}

export function localDate(year, month, day, hour = 0, minute = 0) {
  return new Date(year, month - 1, day, hour, minute);
}

/** A temp dir removed after the test. */
export function tempDir(t, prefix = 'paceline-') {
  const dir = mkdtempSync(join(tmpdir(), prefix));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  return dir;
}

/** Render context with an in-memory snapshot store and colors off. */
export function memoryContext(overrides = {}) {
  let snapshot = null;
  return {
    now: localDate(2026, 9, 24, 10),
    config: mergeConfig(null),
    env: { NO_COLOR: '1' },
    readSnapshot: () => snapshot,
    writeSnapshot: (s) => {
      snapshot = s;
    },
    gitBranch: () => null,
    ...overrides,
  };
}
