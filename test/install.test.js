import assert from 'node:assert/strict';
import { existsSync, readFileSync, statSync, symlinkSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, it } from 'node:test';
import { buildCommand, install, isPacelineStatusLine, uninstall } from '../src/install.js';
import { tempDir } from './helpers.js';

const SCRIPT = '/opt/lib/node_modules/paceline/bin/paceline.js';
const readJson = (p) => JSON.parse(readFileSync(p, 'utf8'));

function setup(t, settings) {
  const dir = tempDir(t);
  const settingsPath = join(dir, 'settings.json');
  if (settings !== undefined) {
    writeFileSync(settingsPath, typeof settings === 'string' ? settings : JSON.stringify(settings));
  }
  return { dir, settingsPath };
}

describe('buildCommand', () => {
  it('quotes the path and normalizes Windows separators', () => {
    assert.equal(buildCommand(SCRIPT), `node "${SCRIPT}"`);
    assert.equal(
      buildCommand('C:\\Users\\me\\AppData\\Roaming\\npm\\node_modules\\paceline\\bin\\paceline.js'),
      'node "C:/Users/me/AppData/Roaming/npm/node_modules/paceline/bin/paceline.js"',
    );
  });

  it('allows spaces, which the quotes cover', () => {
    assert.equal(buildCommand('/Users/Jane Doe/paceline.js'), 'node "/Users/Jane Doe/paceline.js"');
  });

  it('refuses paths that could inject shell commands', () => {
    for (const p of [
      '/tmp/x"; rm -rf ~; echo "/paceline.js',
      '/tmp/$(curl evil)/paceline.js',
      '/tmp/`id`/paceline.js',
      'C:\\Users\\%USERPROFILE%\\paceline.js',
      'C:\\Users\\a!b!\\paceline.js',
      '/tmp/a\nb/paceline.js',
    ]) {
      assert.throws(() => buildCommand(p), /unsafe/, p);
    }
  });
});

describe('install', () => {
  it('creates settings.json when missing', (t) => {
    const { dir, settingsPath } = setup(t);
    const r = install({ claudeDir: dir, scriptPath: SCRIPT });
    assert.equal(r.status, 'installed');
    assert.equal(r.backup, null);
    assert.equal(readJson(settingsPath).statusLine.command, `node "${SCRIPT}"`);
  });

  it('preserves every other setting and writes a backup first', (t) => {
    const original = { model: 'opus', permissions: { allow: ['Bash(ls)'] }, hooks: {} };
    const { dir, settingsPath } = setup(t, original);
    const r = install({ claudeDir: dir, scriptPath: SCRIPT });
    const after = readJson(settingsPath);
    assert.deepEqual({ ...after, statusLine: undefined }, { ...original, statusLine: undefined });
    assert.deepEqual(readJson(r.backup), original);
  });

  it('is idempotent', (t) => {
    const { dir } = setup(t, {});
    install({ claudeDir: dir, scriptPath: SCRIPT });
    assert.equal(install({ claudeDir: dir, scriptPath: SCRIPT }).status, 'unchanged');
  });

  it('updates its own entry when the install path moves', (t) => {
    const { dir, settingsPath } = setup(t, {});
    install({ claudeDir: dir, scriptPath: SCRIPT });
    const r = install({ claudeDir: dir, scriptPath: '/new/paceline/bin/paceline.js' });
    assert.equal(r.status, 'updated');
    assert.match(readJson(settingsPath).statusLine.command, /\/new\/paceline/);
  });

  it('refuses to replace another status line without --force', (t) => {
    const other = { type: 'command', command: 'bash ~/my-line.sh' };
    const { dir, settingsPath } = setup(t, { statusLine: other });
    assert.throws(() => install({ claudeDir: dir, scriptPath: SCRIPT }), /--force/);
    assert.deepEqual(readJson(settingsPath).statusLine, other);
  });

  it('replaces with --force and uninstall restores the original', (t) => {
    const other = { type: 'command', command: 'bash ~/my-line.sh' };
    const { dir, settingsPath } = setup(t, { statusLine: other, theme: 'dark' });
    const r = install({ claudeDir: dir, scriptPath: SCRIPT, force: true });
    assert.deepEqual(r.replaced, other);
    const u = uninstall({ claudeDir: dir });
    assert.deepEqual(u.restored, other);
    assert.deepEqual(readJson(settingsPath), { statusLine: other, theme: 'dark' });
    assert.equal(existsSync(join(dir, 'paceline-install.json')), false);
  });

  it('never overwrites a settings file it cannot parse', (t) => {
    for (const text of ['{ "model": "opus", }', '[1,2]', 'null']) {
      const { dir, settingsPath } = setup(t, text);
      assert.throws(() => install({ claudeDir: dir, scriptPath: SCRIPT }), /Refusing to edit/);
      assert.equal(readFileSync(settingsPath, 'utf8'), text);
    }
  });

  it('treats an empty settings file as empty settings', (t) => {
    const { dir, settingsPath } = setup(t, '');
    install({ claudeDir: dir, scriptPath: SCRIPT });
    assert.ok(readJson(settingsPath).statusLine);
  });

  it('refuses unsafe install paths without touching settings', (t) => {
    const { dir, settingsPath } = setup(t, { model: 'opus' });
    assert.throws(() => install({ claudeDir: dir, scriptPath: '/tmp/$(id)/paceline.js' }), /unsafe/);
    assert.deepEqual(readJson(settingsPath), { model: 'opus' });
  });

  it('leaves no temp files behind', (t) => {
    const { dir } = setup(t, {});
    install({ claudeDir: dir, scriptPath: SCRIPT });
    assert.equal(existsSync(join(dir, `settings.json.${process.pid}.tmp`)), false);
  });

  it('writes through a symlinked settings.json instead of replacing the link', (t) => {
    const { dir } = setup(t);
    const realDir = tempDir(t);
    const real = join(realDir, 'real-settings.json');
    writeFileSync(real, '{}');
    try {
      symlinkSync(real, join(dir, 'settings.json'));
    } catch {
      t.skip('symlinks need extra privileges on this platform');
      return;
    }
    install({ claudeDir: dir, scriptPath: SCRIPT });
    assert.ok(readJson(real).statusLine);
  });

  it('writes settings readable only by the owner on POSIX', { skip: process.platform === 'win32' }, (t) => {
    const { dir, settingsPath } = setup(t);
    install({ claudeDir: dir, scriptPath: SCRIPT });
    assert.equal(statSync(settingsPath).mode & 0o077, 0);
  });
});

describe('uninstall', () => {
  it('removes its own entry', (t) => {
    const { dir, settingsPath } = setup(t, { model: 'opus' });
    install({ claudeDir: dir, scriptPath: SCRIPT });
    assert.deepEqual(uninstall({ claudeDir: dir }), { status: 'uninstalled', restored: null });
    assert.deepEqual(readJson(settingsPath), { model: 'opus' });
  });

  it('never removes a status line it does not own', (t) => {
    const other = { type: 'command', command: 'bash ~/my-line.sh' };
    const { dir, settingsPath } = setup(t, { statusLine: other });
    assert.equal(uninstall({ claudeDir: dir }).status, 'not-installed');
    assert.deepEqual(readJson(settingsPath).statusLine, other);
  });

  it('ignores a tampered install record', (t) => {
    const { dir, settingsPath } = setup(t, {});
    install({ claudeDir: dir, scriptPath: SCRIPT });
    writeFileSync(join(dir, 'paceline-install.json'), JSON.stringify({ previousStatusLine: 'rm -rf ~' }));
    assert.equal(uninstall({ claudeDir: dir }).restored, null);
    assert.equal(readJson(settingsPath).statusLine, undefined);
  });

  it('is a no-op without a settings file', (t) => {
    const { dir } = setup(t);
    assert.equal(uninstall({ claudeDir: dir }).status, 'not-installed');
  });
});

describe('isPacelineStatusLine', () => {
  it('recognizes only command status lines mentioning paceline', () => {
    assert.equal(isPacelineStatusLine({ command: `node "${SCRIPT}"` }), true);
    assert.equal(isPacelineStatusLine({ command: 'bash x.sh' }), false);
    assert.equal(isPacelineStatusLine(undefined), false);
    assert.equal(isPacelineStatusLine({ command: 42 }), false);
  });
});
