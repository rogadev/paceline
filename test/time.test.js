import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { formatClock, formatDuration, localDateKey, startOfLocalDay } from '../src/time.js';
import { localDate, localEpoch } from './helpers.js';

describe('formatClock', () => {
  const now = localDate(2026, 9, 24, 10);

  it('drops :00 on the hour', () => {
    assert.equal(formatClock(localEpoch(2026, 9, 24, 21), now), '9pm');
    assert.equal(formatClock(localEpoch(2026, 9, 24, 0), now), '12am');
    assert.equal(formatClock(localEpoch(2026, 9, 24, 12), now), '12pm');
  });

  it('keeps minutes otherwise', () => {
    assert.equal(formatClock(localEpoch(2026, 9, 24, 15, 5), now), '3:05pm');
  });

  it('prefixes the weekday when the time is on another day', () => {
    assert.equal(formatClock(localEpoch(2026, 9, 25, 8, 30), now), 'Fri 8:30am');
  });
});

describe('dates', () => {
  it('builds a yyyymmdd key and local midnight', () => {
    assert.equal(localDateKey(localDate(2026, 1, 5, 23)), '20260105');
    assert.equal(startOfLocalDay(localDate(2026, 9, 24, 15, 30)).getTime(), localDate(2026, 9, 24).getTime());
  });
});

describe('formatDuration', () => {
  it('uses the largest useful unit', () => {
    assert.equal(formatDuration(40_000), '40s');
    assert.equal(formatDuration(16 * 60_000), '16m');
    assert.equal(formatDuration(65 * 60_000), '1h5m');
  });
});
