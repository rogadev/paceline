// Terminal-injection tests. A cloned repo controls its folder and branch
// names, so every string paceline prints from outside must be inert.
// Unicode test fixtures written as ASCII \u escapes (smart-quotes-ok).

import assert from 'node:assert/strict';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, it } from 'node:test';
import { getGitBranch } from '../src/git.js';
import { render } from '../src/render.js';
import { safeNumber, safeText } from '../src/sanitize.js';
import { memoryContext, tempDir } from './helpers.js';

const ESC = '\x1b';
const BEL = '\x07';
const CSI_8BIT = '\u009b';
const RLO = '‮';
const ZWSP = '​';

const ATTACKS = {
  'clear screen': `${ESC}[2J${ESC}[H`,
  'set window title (OSC 0)': `${ESC}]0;pwned${BEL}`,
  'hyperlink (OSC 8)': `${ESC}]8;;https://evil.example${ESC}\\click${ESC}]8;;${ESC}\\`,
  'clipboard write (OSC 52)': `${ESC}]52;c;cm0gLXJmIH4=${BEL}`,
  '8-bit CSI': `${CSI_8BIT}2J`,
  'bidi override': `${RLO}txt.exe`,
  'zero-width space': `a${ZWSP}b`,
  'carriage return overwrite': 'safe\rEVIL',
  newline: 'line1\nline2',
};

// Independent of src/sanitize.js on purpose: checks code points directly.
const hasControl = (s) =>
  Array.from(s).some((ch) => {
    const c = ch.codePointAt(0);
    return (
      c <= 0x1f ||
      (c >= 0x7f && c <= 0x9f) ||
      (c >= 0x200b && c <= 0x200f) ||
      (c >= 0x202a && c <= 0x202e) ||
      (c >= 0x2066 && c <= 0x2069) ||
      c === 0xfeff
    );
  });

describe('safeText', () => {
  for (const [name, attack] of Object.entries(ATTACKS)) {
    it(`neutralizes ${name}`, () => {
      assert.equal(hasControl(safeText(attack)), false);
    });
  }

  it('keeps ordinary Unicode', () => {
    assert.equal(safeText('café-日本'), 'café-日本');
  });

  it('caps length without splitting surrogate pairs', () => {
    const out = safeText('\u{1f600}'.repeat(100), 10);
    assert.equal(Array.from(out).length, 10);
    assert.ok(out.isWellFormed());
  });

  it('turns non-strings into empty text', () => {
    for (const v of [undefined, null, 42, {}, ['x']]) assert.equal(safeText(v), '');
  });
});

describe('safeNumber', () => {
  it('accepts only finite numbers', () => {
    assert.equal(safeNumber(5), 5);
    for (const v of ['5', Number.NaN, Number.POSITIVE_INFINITY, null, undefined, {}]) assert.equal(safeNumber(v), null);
  });
});

describe('render output is inert for hostile input', () => {
  // With NO_COLOR, paceline itself emits no escapes, so any ESC in the output
  // would have come from the input.
  const ctx = (extra) => memoryContext({ env: { NO_COLOR: '1' }, ...extra });

  for (const [name, attack] of Object.entries(ATTACKS)) {
    it(`strips ${name} from every text field`, () => {
      const line = render(
        {
          model: { display_name: `Opus${attack}` },
          effort: { level: `max${attack}` },
          workspace: { current_dir: `/repos/app${attack}`, project_dir: `/repos/app${attack}` },
        },
        ctx({ gitBranch: () => `main${attack}` }),
      );
      assert.equal(hasControl(line), false, JSON.stringify(line));
    });
  }

  it('strips escapes from a branch name read from a real HEAD file', (t) => {
    const root = tempDir(t);
    mkdirSync(join(root, '.git'));
    writeFileSync(join(root, '.git', 'HEAD'), `ref: refs/heads/x${ESC}]0;pwned${BEL}${ESC}[2J\n`);
    const line = render({ workspace: { current_dir: root } }, ctx({ gitBranch: getGitBranch }));
    assert.equal(hasControl(line), false, JSON.stringify(line));
    assert.match(line, /\(x\]0;pwned\[2J\)$/);
  });

  it('bounds the length of attacker-controlled fields', () => {
    const line = render({ model: { display_name: 'A'.repeat(100_000) } }, ctx());
    assert.ok(line.length < 100);
  });

  it('ignores numbers smuggled in as strings', () => {
    const line = render({ rate_limits: { five_hour: { used_percentage: '50; rm -rf /' } } }, ctx());
    assert.equal(line, '');
  });
});
