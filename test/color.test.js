import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  contrastRatio,
  headroomLevel,
  oklchToRgb,
  PROJECT_SLOTS,
  projectSlot,
  relativeLuminance,
  slotColor,
} from '../src/color.js';

const TERMINAL_BG = [12, 12, 12];

describe('headroomLevel', () => {
  it('uses the configured thresholds', () => {
    const t = { green: 30, yellow: 15 };
    assert.equal(headroomLevel(30, t), 'green');
    assert.equal(headroomLevel(29, t), 'yellow');
    assert.equal(headroomLevel(15, t), 'yellow');
    assert.equal(headroomLevel(14, t), 'red');
    assert.equal(headroomLevel(-5, t), 'red');
  });
});

describe('oklchToRgb', () => {
  it('maps achromatic lightness to gray', () => {
    assert.deepEqual(oklchToRgb(1, 0, 0), [255, 255, 255]);
    assert.deepEqual(oklchToRgb(0, 0, 0), [0, 0, 0]);
  });

  it('returns null outside the sRGB gamut', () => {
    assert.equal(oklchToRgb(0.8, 0.4, 265), null);
  });

  it('keeps channels as integers in 0-255 (regression: int rounding before gamma)', () => {
    const rgb = oklchToRgb(0.8, 0.1, 255);
    assert.ok(
      rgb.every((c) => Number.isInteger(c) && c > 0 && c < 255),
      String(rgb),
    );
  });
});

describe('project colors', () => {
  it('every slot is readable on a dark terminal (WCAG AA 4.5:1 and then some)', () => {
    for (let slot = 0; slot < PROJECT_SLOTS; slot++) {
      const ratio = contrastRatio(slotColor(slot), TERMINAL_BG);
      assert.ok(ratio >= 9, `slot ${slot} contrast ${ratio.toFixed(1)}`);
    }
  });

  it('every slot is a distinct color', () => {
    const colors = new Set(Array.from({ length: PROJECT_SLOTS }, (_, i) => slotColor(i).join(',')));
    assert.equal(colors.size, PROJECT_SLOTS);
  });

  it('slots have similar perceived brightness', () => {
    const lums = Array.from({ length: PROJECT_SLOTS }, (_, i) => relativeLuminance(slotColor(i)));
    assert.ok(Math.max(...lums) / Math.min(...lums) < 1.4);
  });

  it('hashes names stably and case-insensitively', () => {
    assert.equal(projectSlot('techcentral'), projectSlot('techcentral'));
    assert.equal(projectSlot('TechCentral'), projectSlot('techcentral'));
    const slot = projectSlot('anything');
    assert.ok(Number.isInteger(slot) && slot >= 0 && slot < PROJECT_SLOTS);
  });

  it('honors overrides case-insensitively and wraps out-of-range values', () => {
    assert.equal(projectSlot('Website', { website: 4 }), 4);
    assert.equal(projectSlot('api', { api: 13 }), 1);
    assert.equal(projectSlot('api', { api: -1 }), 11);
  });
});
