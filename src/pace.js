// Daily pace: today's share of the weekly limit, as a countdown.
//
// The budget is anchored at the first render of each local day (a snapshot
// in paceline-day.json) so it stays fixed while you work instead of drifting
// with every refresh:
//
//   budget = weekly % left at day start / days from local midnight to reset
//
// Unspent budget rolls over by spreading across the remaining days, since the
// next day's budget divides whatever the week still has.

import { readFileSync, renameSync, writeFileSync } from 'node:fs';
import { localDateKey, startOfLocalDay } from './time.js';

const EVEN_PACE = 100 / 7;
const DAY_MS = 86_400_000;

/**
 * @param {object} p
 * @param {number} p.usedPct   weekly % used now
 * @param {number} p.resetsAt  weekly reset, epoch seconds
 * @param {Date}   p.now
 * @param {object|null} p.snapshot  { date, resetsAt, usedAtStart } or null
 * @returns {{ kind: 'none' } | { kind: 'lastDay', resetsAt: number } |
 *   { kind: 'budget', budget: number, spent: number, pctLeft: number,
 *     pctUsed: number, over: boolean, pace: 'up'|'even'|'down',
 *     snapshot: object, snapshotChanged: boolean }}
 */
export function computeDailyPace({ usedPct, resetsAt, now, snapshot }) {
  const daysLeft = (resetsAt * 1000 - startOfLocalDay(now).getTime()) / DAY_MS;
  if (!(daysLeft > 0)) return { kind: 'none' };
  // Final partial day: everything left in the week is today's.
  if (daysLeft <= 1) return { kind: 'lastDay', resetsAt };

  const date = localDateKey(now);
  const stale =
    !isValidSnapshot(snapshot) ||
    snapshot.date !== date ||
    snapshot.resetsAt !== resetsAt ||
    usedPct < snapshot.usedAtStart; // window reset under us
  const snap = stale ? { date, resetsAt, usedAtStart: usedPct } : snapshot;

  const budget = (100 - snap.usedAtStart) / daysLeft;
  const spent = usedPct - snap.usedAtStart;
  const ratio = budget / EVEN_PACE;
  return {
    kind: 'budget',
    budget,
    spent,
    pctLeft: budget > 0 ? (100 * (budget - spent)) / budget : 0,
    pctUsed: budget > 0 ? (100 * spent) / budget : 0,
    over: spent > budget,
    pace: ratio >= 1.25 ? 'up' : ratio <= 0.8 ? 'down' : 'even',
    snapshot: snap,
    snapshotChanged: stale,
  };
}

export function isValidSnapshot(s) {
  return (
    s !== null &&
    typeof s === 'object' &&
    typeof s.date === 'string' &&
    /^\d{8}$/.test(s.date) &&
    Number.isFinite(s.resetsAt) &&
    Number.isFinite(s.usedAtStart) &&
    s.usedAtStart >= 0 &&
    s.usedAtStart <= 100
  );
}

export function readSnapshot(path) {
  try {
    const text = readFileSync(path, 'utf8');
    if (text.length > 4096) return null;
    const parsed = JSON.parse(text);
    return isValidSnapshot(parsed) ? parsed : null;
  } catch {
    return null;
  }
}

/**
 * Write via a temp file and rename so concurrent sessions never read a
 * half-written file. Failures are ignored: the next render retries.
 */
export function writeSnapshot(path, snapshot) {
  const tmp = `${path}.${process.pid}.tmp`;
  try {
    writeFileSync(tmp, JSON.stringify(snapshot), { mode: 0o600 });
    renameSync(tmp, path);
  } catch {
    // best effort
  }
}
