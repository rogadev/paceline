import assert from 'node:assert/strict';
import { writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, it } from 'node:test';
import { claudeDir, DEFAULT_CONFIG, loadConfig, mergeConfig } from '../src/config.js';
import { tempDir } from './helpers.js';

describe('mergeConfig', () => {
  it('returns defaults for anything that is not an object', () => {
    for (const v of [null, undefined, 42, 'x', []]) {
      assert.deepEqual(mergeConfig(v).segments, DEFAULT_CONFIG.segments);
    }
  });

  it('takes valid values and ignores wrong types or out-of-range numbers', () => {
    const c = mergeConfig({
      segments: { model: false, duration: 'no', unknown: false },
      thresholds: { headroomGreen: 40, headroomYellow: 500, contextWarn: Number.NaN },
      quietEfforts: ['low'],
      projectSlots: { api: 3, web: 12, bad: 1.5 },
    });
    assert.equal(c.segments.model, false);
    assert.equal(c.segments.duration, true);
    assert.ok(!('unknown' in c.segments));
    assert.equal(c.thresholds.headroomGreen, 40);
    assert.equal(c.thresholds.headroomYellow, 15);
    assert.equal(c.thresholds.contextWarn, 70);
    assert.deepEqual(c.quietEfforts, ['low']);
    assert.deepEqual({ ...c.projectSlots }, { api: 3 });
  });

  it('cannot be used for prototype pollution', () => {
    const hostile = JSON.parse('{"__proto__": {"polluted": true}, "projectSlots": {"__proto__": 1, "constructor": 2}}');
    const c = mergeConfig(hostile);
    assert.equal({}.polluted, undefined);
    assert.equal(Object.getPrototypeOf(c.projectSlots), null);
    assert.equal(c.projectSlots.constructor, 2);
    assert.equal({}.constructor, Object);
  });

  it('does not share state with the frozen defaults', () => {
    const c = mergeConfig(null);
    c.segments.model = false;
    assert.equal(DEFAULT_CONFIG.segments.model, true);
  });
});

describe('loadConfig', () => {
  it('reads a config file', (t) => {
    const path = join(tempDir(t), 'paceline.json');
    writeFileSync(path, JSON.stringify({ segments: { duration: false } }));
    assert.equal(loadConfig(path).segments.duration, false);
  });

  it('falls back to defaults for a missing, invalid, or oversized file', (t) => {
    const dir = tempDir(t);
    assert.equal(loadConfig(join(dir, 'missing.json')).segments.model, true);
    const bad = join(dir, 'bad.json');
    writeFileSync(bad, '{nope');
    assert.equal(loadConfig(bad).segments.model, true);
    const big = join(dir, 'big.json');
    writeFileSync(big, JSON.stringify({ segments: { model: false }, pad: 'x'.repeat(70_000) }));
    assert.equal(loadConfig(big).segments.model, true);
  });
});

describe('claudeDir', () => {
  it('honors CLAUDE_CONFIG_DIR', () => {
    assert.equal(claudeDir({ CLAUDE_CONFIG_DIR: '/custom' }), '/custom');
    assert.ok(claudeDir({}).endsWith('.claude'));
  });
});
