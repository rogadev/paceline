// End-to-end: run the real CLI as Claude Code would, with stdin and an
// isolated CLAUDE_CONFIG_DIR.

import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, it } from 'node:test';
import { fileURLToPath } from 'node:url';
import { tempDir } from './helpers.js';

const BIN = fileURLToPath(new URL('../bin/paceline.js', import.meta.url));

function run(t, args, input, extraEnv = {}) {
  const configDir = tempDir(t);
  const r = spawnSync(process.execPath, [BIN, ...args], {
    input,
    encoding: 'utf8',
    env: { ...process.env, CLAUDE_CONFIG_DIR: configDir, NO_COLOR: '1', ...extraEnv },
  });
  return { ...r, configDir };
}

describe('paceline CLI', () => {
  it('renders a payload from stdin', (t) => {
    const r = run(t, [], JSON.stringify({ model: { display_name: 'Opus 5.5 (1M context)' } }));
    assert.equal(r.status, 0);
    assert.equal(r.stdout, 'Opus 5.5');
  });

  it('writes the day snapshot into CLAUDE_CONFIG_DIR', (t) => {
    const resetsAt = Math.floor(Date.now() / 1000) + 4 * 86400;
    const r = run(t, [], JSON.stringify({ rate_limits: { seven_day: { used_percentage: 10, resets_at: resetsAt } } }));
    assert.match(r.stdout, /today's \d+% budget/);
    assert.ok(existsSync(join(r.configDir, 'paceline-day.json')));
  });

  it('falls back to a fixed label on bad input instead of failing', (t) => {
    for (const input of ['', 'not json', '{"model":']) {
      const r = run(t, [], input);
      assert.equal(r.status, 0);
      assert.equal(r.stdout, 'Claude Code');
    }
  });

  it('refuses oversized stdin', (t) => {
    const r = run(t, [], `{"pad":"${'x'.repeat(2 * 1024 * 1024)}"}`);
    assert.equal(r.stdout, 'Claude Code');
  });

  it('prints its version and help', (t) => {
    const pkg = JSON.parse(readFileSync(new URL('../package.json', import.meta.url), 'utf8'));
    assert.equal(run(t, ['--version']).stdout.trim(), pkg.version);
    assert.match(run(t, ['--help']).stdout, /paceline install/);
  });

  it('rejects unknown commands with a non-zero exit', (t) => {
    const r = run(t, ['frobnicate']);
    assert.equal(r.status, 1);
    assert.match(r.stderr, /Unknown command/);
  });

  it('installs and uninstalls against CLAUDE_CONFIG_DIR', (t) => {
    const configDir = tempDir(t);
    const env = { CLAUDE_CONFIG_DIR: configDir };
    const opts = { encoding: 'utf8', env: { ...process.env, ...env } };
    const i = spawnSync(process.execPath, [BIN, 'install'], opts);
    assert.equal(i.status, 0, i.stderr);
    const settings = JSON.parse(readFileSync(join(configDir, 'settings.json'), 'utf8'));
    assert.match(settings.statusLine.command, /^node ".*paceline\.js"$/);
    const u = spawnSync(process.execPath, [BIN, 'uninstall'], opts);
    assert.equal(u.status, 0, u.stderr);
    assert.equal(JSON.parse(readFileSync(join(configDir, 'settings.json'), 'utf8')).statusLine, undefined);
  });

  it('reports install errors on stderr with exit 1', (t) => {
    const configDir = tempDir(t);
    const opts = { encoding: 'utf8', env: { ...process.env, CLAUDE_CONFIG_DIR: configDir } };
    spawnSync(process.execPath, [BIN, 'install'], opts);
    // Corrupt the file, then try again: install must refuse.
    writeFileSync(join(configDir, 'settings.json'), '{broken');
    const r = spawnSync(process.execPath, [BIN, 'install'], opts);
    assert.equal(r.status, 1);
    assert.match(r.stderr, /Refusing to edit/);
  });
});
