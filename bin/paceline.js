#!/usr/bin/env node
// paceline CLI. With no arguments it reads Claude Code's status JSON on stdin
// and prints the status line; `install` and `uninstall` edit settings.json.

import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { claudeDir, loadConfig } from '../src/config.js';
import { getGitBranch } from '../src/git.js';
import { install, uninstall } from '../src/install.js';
import { readSnapshot, writeSnapshot } from '../src/pace.js';
import { render } from '../src/render.js';

// Claude Code's payload is a few KB; anything far larger is not a real payload.
const MAX_STDIN_BYTES = 1024 * 1024;
const FALLBACK = 'Claude Code';

const scriptPath = fileURLToPath(import.meta.url);
const pkg = JSON.parse(readFileSync(new URL('../package.json', import.meta.url), 'utf8'));

async function readStdin() {
  const chunks = [];
  let size = 0;
  for await (const chunk of process.stdin) {
    size += chunk.length;
    if (size > MAX_STDIN_BYTES) throw new Error('stdin too large');
    chunks.push(chunk);
  }
  return Buffer.concat(chunks).toString('utf8');
}

async function renderFromStdin() {
  try {
    const payload = JSON.parse(await readStdin());
    const dir = claudeDir();
    const statePath = join(dir, 'paceline-day.json');
    const line = render(payload, {
      now: new Date(),
      config: loadConfig(join(dir, 'paceline.json')),
      readSnapshot: () => readSnapshot(statePath),
      writeSnapshot: (s) => writeSnapshot(statePath, s),
      gitBranch: getGitBranch,
      env: process.env,
    });
    process.stdout.write(line || FALLBACK);
  } catch {
    // Never leave the status line empty.
    process.stdout.write(FALLBACK);
  }
}

const HELP = `paceline ${pkg.version}: a Claude Code status line with a daily usage pace

Usage:
  paceline install [--force]   set paceline as your Claude Code status line
  paceline uninstall           remove it and restore the previous status line
  paceline --version
  paceline --help

With no arguments, paceline reads Claude Code's status JSON on stdin.
Config: ${join(claudeDir(), 'paceline.json')}`;

async function main(args) {
  const [cmd, ...rest] = args;
  if (!cmd) return renderFromStdin();
  if (cmd === '--version' || cmd === '-v') return console.log(pkg.version);
  if (cmd === '--help' || cmd === '-h') return console.log(HELP);
  try {
    if (cmd === 'install') {
      const r = install({ claudeDir: claudeDir(), scriptPath, force: rest.includes('--force') });
      if (r.status === 'unchanged') console.log('paceline is already your status line.');
      else {
        console.log(`paceline ${r.status}. It appears on the next status line refresh.`);
        if (r.backup) console.log(`Backup of your previous settings: ${r.backup}`);
        if (r.replaced) console.log("Your previous status line was saved; 'paceline uninstall' restores it.");
      }
      return;
    }
    if (cmd === 'uninstall') {
      const r = uninstall({ claudeDir: claudeDir() });
      if (r.status === 'not-installed') console.log('paceline is not your status line; nothing changed.');
      else console.log(r.restored ? 'paceline removed; previous status line restored.' : 'paceline removed.');
      return;
    }
  } catch (err) {
    console.error(err.message);
    process.exitCode = 1;
    return;
  }
  console.error(`Unknown command: ${cmd}\n\n${HELP}`);
  process.exitCode = 1;
}

await main(process.argv.slice(2));
