// Supply-chain and packaging guarantees. paceline runs on every status-line
// refresh on the user's machine, so what ships and what it can do are tested
// like behavior.

import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, it } from 'node:test';
import { fileURLToPath } from 'node:url';

const ROOT = fileURLToPath(new URL('..', import.meta.url));
const pkg = JSON.parse(readFileSync(join(ROOT, 'package.json'), 'utf8'));

function sourceFiles() {
  const files = [];
  for (const dir of ['src', 'bin']) {
    for (const name of readdirSync(join(ROOT, dir))) {
      if (name.endsWith('.js')) files.push(join(ROOT, dir, name));
    }
  }
  return files;
}

describe('package', () => {
  it('has zero runtime dependencies', () => {
    for (const field of ['dependencies', 'optionalDependencies', 'peerDependencies', 'bundleDependencies']) {
      assert.deepEqual(Object.keys(pkg[field] ?? {}), [], field);
    }
  });

  it('has no install lifecycle scripts that would run on users machines', () => {
    for (const hook of ['preinstall', 'install', 'postinstall']) {
      assert.equal(pkg.scripts?.[hook], undefined, hook);
    }
  });

  it('pins dev tooling to exact versions', () => {
    for (const [name, range] of Object.entries(pkg.devDependencies ?? {})) {
      assert.match(range, /^\d+\.\d+\.\d+$/, `${name}@${range}`);
    }
  });

  it('ships only bin, src, and the standard files', () => {
    const out = execFileSync(process.platform === 'win32' ? 'npm.cmd' : 'npm', ['pack', '--dry-run', '--json'], {
      cwd: ROOT,
      encoding: 'utf8',
      shell: process.platform === 'win32',
    });
    const files = JSON.parse(out)[0].files.map((f) => f.path.replace(/\\/g, '/'));
    for (const f of files) {
      assert.match(f, /^(bin\/|src\/|package\.json$|README\.md$|LICENSE$)/, f);
    }
    assert.ok(files.includes('bin/paceline.js'));
  });
});

describe('runtime code', () => {
  it('never spawns processes, uses the network, or evaluates code', () => {
    const forbidden = [
      /node:child_process|['"]child_process['"]/,
      /node:(net|http|https|http2|dgram|tls|dns)\b|['"](net|http|https|http2|dgram|tls|dns)['"]/,
      /\beval\s*\(/,
      /new Function\s*\(/,
      /node:vm\b|['"]vm['"]/,
      /\bfetch\s*\(/,
    ];
    for (const file of sourceFiles()) {
      const text = readFileSync(file, 'utf8');
      for (const re of forbidden) assert.doesNotMatch(text, re, `${file} matches ${re}`);
    }
  });

  it('imports only node: builtins and its own modules', () => {
    for (const file of sourceFiles()) {
      const text = readFileSync(file, 'utf8');
      for (const [, spec] of text.matchAll(/^import .* from '([^']+)';$/gm)) {
        assert.ok(spec.startsWith('node:') || spec.startsWith('./') || spec.startsWith('../'), `${file}: ${spec}`);
      }
    }
  });
});
