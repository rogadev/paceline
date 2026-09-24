// Adds or removes paceline as Claude Code's status line in settings.json.
//
// This edits a file the user owns, so it is deliberately conservative:
// - an unparseable settings.json is never overwritten
// - an existing status line from something else is only replaced with --force,
//   and is remembered so uninstall can put it back
// - a backup copy is written before the first change
// - writes go through a temp file and rename, so a crash cannot truncate it
// - the command written is validated so an install path cannot inject shell

import { copyFileSync, existsSync, readFileSync, realpathSync, renameSync, rmSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

const MARKER = 'paceline';

// Characters that could break out of the double-quoted path in the command,
// or be expanded by a shell (bash: $ ` \ "; cmd.exe: % !).
const UNSAFE_PATH_CHARS = /["`$\\%!\r\n]/;

/** The statusLine command for a script path, or throws if the path is unsafe to quote. */
export function buildCommand(scriptPath) {
  const normalized = scriptPath.replace(/\\/g, '/');
  if (UNSAFE_PATH_CHARS.test(normalized)) {
    throw new Error(
      `Refusing to install: the install path contains characters that are unsafe in a shell command: ${normalized}`,
    );
  }
  return `node "${normalized}"`;
}

export function isPacelineStatusLine(statusLine) {
  return Boolean(statusLine && typeof statusLine.command === 'string' && statusLine.command.includes(MARKER));
}

function readSettings(path) {
  if (!existsSync(path)) return { settings: {}, existed: false };
  const text = readFileSync(path, 'utf8');
  let settings;
  try {
    settings = text.trim() ? JSON.parse(text) : {};
  } catch (err) {
    throw new Error(`Refusing to edit ${path}: it is not valid JSON (${err.message}). Fix it and rerun.`);
  }
  if (settings === null || typeof settings !== 'object' || Array.isArray(settings)) {
    throw new Error(`Refusing to edit ${path}: expected a JSON object at the top level.`);
  }
  return { settings, existed: true };
}

/** Atomic write that follows a symlinked settings file to its real target. */
function writeJsonAtomic(path, value) {
  const target = existsSync(path) ? realpathSync(path) : path;
  const tmp = `${target}.${process.pid}.tmp`;
  writeFileSync(tmp, `${JSON.stringify(value, null, 2)}\n`, { mode: 0o600 });
  renameSync(tmp, target);
}

/**
 * @param {object} opts
 * @param {string} opts.claudeDir   Claude Code config dir
 * @param {string} opts.scriptPath  absolute path to bin/paceline.js
 * @param {boolean} [opts.force]    replace another tool's status line
 * @returns {{ status: 'installed'|'updated'|'unchanged', backup: string|null, replaced: object|null }}
 */
export function install({ claudeDir, scriptPath, force = false }) {
  const settingsPath = join(claudeDir, 'settings.json');
  const recordPath = join(claudeDir, 'paceline-install.json');
  const command = buildCommand(scriptPath);
  const { settings, existed } = readSettings(settingsPath);
  const current = settings.statusLine;

  if (isPacelineStatusLine(current) && current.command === command) {
    return { status: 'unchanged', backup: null, replaced: null };
  }
  if (current && !isPacelineStatusLine(current) && !force) {
    throw new Error(
      `A status line is already configured (${JSON.stringify(current)}). Rerun with --force to replace it; ` +
        "'paceline uninstall' will restore it.",
    );
  }

  let backup = null;
  if (existed) {
    backup = `${settingsPath}.paceline-backup`;
    copyFileSync(settingsPath, backup);
  }
  const replaced = current && !isPacelineStatusLine(current) ? current : null;
  if (replaced) writeJsonAtomic(recordPath, { previousStatusLine: replaced });

  settings.statusLine = { type: 'command', command, padding: 0 };
  writeJsonAtomic(settingsPath, settings);
  return { status: isPacelineStatusLine(current) ? 'updated' : 'installed', backup, replaced };
}

/** Remove paceline, restoring whatever status line it replaced. Leaves other tools' status lines alone. */
export function uninstall({ claudeDir }) {
  const settingsPath = join(claudeDir, 'settings.json');
  const recordPath = join(claudeDir, 'paceline-install.json');
  const { settings, existed } = readSettings(settingsPath);
  if (!existed || !isPacelineStatusLine(settings.statusLine)) return { status: 'not-installed', restored: null };

  let restored = null;
  try {
    const record = JSON.parse(readFileSync(recordPath, 'utf8'));
    const prev = record?.previousStatusLine;
    if (prev && typeof prev === 'object' && !Array.isArray(prev) && typeof prev.command === 'string') restored = prev;
  } catch {
    // no record: nothing to restore
  }
  if (restored) settings.statusLine = restored;
  else delete settings.statusLine;
  writeJsonAtomic(settingsPath, settings);
  rmSync(recordPath, { force: true });
  return { status: 'uninstalled', restored };
}
