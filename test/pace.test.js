import assert from 'node:assert/strict';
import { readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, it } from 'node:test';
import { computeDailyPace, isValidSnapshot, readSnapshot, writeSnapshot } from '../src/pace.js';
import { localDate, localEpoch, tempDir } from './helpers.js';

const now = localDate(2026, 9, 24, 10);

describe('computeDailyPace', () => {
  it('divides the weekly headroom left by the days from midnight to reset', () => {
    const r = computeDailyPace({ usedPct: 0, resetsAt: localEpoch(2026, 10, 1), now, snapshot: null });
    assert.equal(r.kind, 'budget');
    assert.ok(Math.abs(r.budget - 100 / 7) < 1e-9);
    assert.equal(r.pace, 'even');
    assert.equal(r.pctLeft, 100);
  });

  it('keeps the budget fixed within a day by reusing the snapshot', () => {
    const resetsAt = localEpoch(2026, 9, 27);
    const first = computeDailyPace({ usedPct: 4, resetsAt, now, snapshot: null });
    assert.equal(first.snapshotChanged, true);
    const later = computeDailyPace({ usedPct: 17, resetsAt, now, snapshot: first.snapshot });
    assert.equal(later.snapshotChanged, false);
    assert.equal(later.budget, first.budget);
    assert.equal(later.spent, 13);
  });

  it('reports overshoot as % used above 100', () => {
    const resetsAt = localEpoch(2026, 10, 1);
    const snap = { date: '20260924', resetsAt, usedAtStart: 0 };
    const r = computeDailyPace({ usedPct: 20, resetsAt, now, snapshot: snap });
    assert.equal(r.over, true);
    assert.equal(Math.round(r.pctUsed), 140);
  });

  it('spreads unspent budget over the remaining days', () => {
    // 7 days out, then only 5% spent; next day has 6 days left.
    const resetsAt = localEpoch(2026, 10, 1);
    const next = computeDailyPace({ usedPct: 5, resetsAt, now: localDate(2026, 9, 25, 9), snapshot: null });
    assert.equal(Math.round(next.budget), 16);
  });

  it('flags pace up and down against even pace', () => {
    const light = computeDailyPace({ usedPct: 4, resetsAt: localEpoch(2026, 9, 27, 9), now, snapshot: null });
    assert.equal(light.pace, 'up');
    const heavy = computeDailyPace({ usedPct: 88, resetsAt: localEpoch(2026, 9, 26, 9), now, snapshot: null });
    assert.equal(heavy.pace, 'down');
  });

  it('re-snapshots on a new day, a new window, or usage going backwards', () => {
    const resetsAt = localEpoch(2026, 9, 28);
    const snap = { date: '20260923', resetsAt, usedAtStart: 10 };
    assert.equal(computeDailyPace({ usedPct: 20, resetsAt, now, snapshot: snap }).snapshotChanged, true);
    const today = { ...snap, date: '20260924' };
    assert.equal(
      computeDailyPace({ usedPct: 20, resetsAt: resetsAt + 60, now, snapshot: today }).snapshotChanged,
      true,
    );
    assert.equal(computeDailyPace({ usedPct: 5, resetsAt, now, snapshot: today }).snapshotChanged, true);
  });

  it('switches to last-day mode inside the final day', () => {
    const r = computeDailyPace({ usedPct: 92, resetsAt: localEpoch(2026, 9, 24, 21), now, snapshot: null });
    assert.deepEqual(r, { kind: 'lastDay', resetsAt: localEpoch(2026, 9, 24, 21) });
  });

  it('shows nothing when the reset is already in the past', () => {
    assert.equal(
      computeDailyPace({ usedPct: 50, resetsAt: localEpoch(2026, 9, 23), now, snapshot: null }).kind,
      'none',
    );
  });

  it('never divides by zero when the week starts fully used', () => {
    const r = computeDailyPace({ usedPct: 100, resetsAt: localEpoch(2026, 9, 28), now, snapshot: null });
    assert.equal(r.pctLeft, 0);
    assert.ok(Number.isFinite(r.pctUsed));
  });
});

describe('snapshot file', () => {
  it('round-trips through disk', (t) => {
    const path = join(tempDir(t), 'day.json');
    const snap = { date: '20260924', resetsAt: 1790838000, usedAtStart: 5 };
    writeSnapshot(path, snap);
    assert.deepEqual(readSnapshot(path), snap);
  });

  it('rejects corrupt, oversized, or wrongly typed state instead of trusting it', (t) => {
    const dir = tempDir(t);
    const path = join(dir, 'day.json');
    for (const text of [
      '{not json',
      JSON.stringify({ date: '2026-09-24', resetsAt: 1, usedAtStart: 5 }),
      JSON.stringify({ date: '20260924', resetsAt: '1', usedAtStart: 5 }),
      JSON.stringify({ date: '20260924', resetsAt: 1, usedAtStart: 500 }),
      JSON.stringify({ date: '20260924', resetsAt: 1, usedAtStart: 5, pad: 'x'.repeat(5000) }),
    ]) {
      writeFileSync(path, text);
      assert.equal(readSnapshot(path), null, text.slice(0, 40));
    }
  });

  it('returns null for a missing file and ignores write failures', (t) => {
    const dir = tempDir(t);
    assert.equal(readSnapshot(join(dir, 'missing.json')), null);
    assert.doesNotThrow(() => writeSnapshot(join(dir, 'no', 'such', 'dir.json'), { date: '20260924' }));
  });

  it('writes owner-only JSON', (t) => {
    const path = join(tempDir(t), 'day.json');
    writeSnapshot(path, { date: '20260924', resetsAt: 1, usedAtStart: 0 });
    assert.deepEqual(JSON.parse(readFileSync(path, 'utf8')), { date: '20260924', resetsAt: 1, usedAtStart: 0 });
  });

  it('isValidSnapshot rejects non-objects', () => {
    assert.equal(isValidSnapshot(null), false);
    assert.equal(isValidSnapshot('x'), false);
  });
});
