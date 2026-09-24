import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, it } from 'node:test';
import { fileURLToPath } from 'node:url';
import { classifyCommit, highestBump, nextVersion } from '../scripts/next-bump.js';
import { tempDir } from './helpers.js';

const SCRIPT = fileURLToPath(new URL('../scripts/next-bump.js', import.meta.url));

describe('classifyCommit', () => {
  const cases = [
    ['fix: handle CRLF in HEAD', 'patch'],
    ['perf(git): read fewer bytes', 'patch'],
    ['feat: add a cost segment', 'minor'],
    ['feat(render): add a cost segment', 'minor'],
    ['feat!: rename config keys', 'major'],
    ['fix(config)!: drop legacy thresholds', 'major'],
    ['refactor: split render\n\nBREAKING CHANGE: render() takes a ctx object', 'major'],
    ['chore: tidy\n\nBREAKING-CHANGE: node 22 required', 'major'],
    ['docs: explain versioning', 'none'],
    ['test: cover worktrees', 'none'],
    ['ci: pin actions', 'none'],
    ['Merge pull request #1 from rogadev/dev', 'none'],
    ['feat:missing space', 'none'],
    ['', 'none'],
  ];
  for (const [msg, bump] of cases) {
    it(`${JSON.stringify(msg.split('\n')[0])} -> ${bump}`, () => assert.equal(classifyCommit(msg), bump));
  }

  it('only counts BREAKING CHANGE as a footer, not in the subject', () => {
    assert.equal(classifyCommit('docs: mention BREAKING CHANGE: policy'), 'none');
  });
});

describe('highestBump', () => {
  it('takes the largest bump across commits', () => {
    assert.equal(highestBump(['docs: a', 'fix: b']), 'patch');
    assert.equal(highestBump(['fix: a', 'feat: b', 'docs: c']), 'minor');
    assert.equal(highestBump(['feat: a', 'fix!: b']), 'major');
    assert.equal(highestBump(['docs: a', 'chore: b']), 'none');
    assert.equal(highestBump([]), 'none');
  });
});

describe('nextVersion', () => {
  it('moves the right number and resets the lower ones', () => {
    assert.equal(nextVersion('1.2.3', 'patch'), '1.2.4');
    assert.equal(nextVersion('1.2.3', 'minor'), '1.3.0');
    assert.equal(nextVersion('1.2.3', 'major'), '2.0.0');
    assert.equal(nextVersion('1.2.3', 'none'), null);
  });

  it('starts at 1.0.0', () => {
    assert.equal(nextVersion(null, 'patch'), '1.0.0');
  });
});

describe('next-bump CLI', () => {
  it('previews the bump for commits since the latest tag', (t) => {
    const repo = tempDir(t);
    const git = (...args) =>
      execFileSync('git', ['-c', 'user.name=t', '-c', 'user.email=t@t', '-c', 'commit.gpgsign=false', ...args], {
        cwd: repo,
        encoding: 'utf8',
      });
    git('init', '-q', '-b', 'main');
    git('commit', '-q', '--allow-empty', '-m', 'feat: first');
    git('tag', 'v1.2.3');
    git('commit', '-q', '--allow-empty', '-m', 'fix: a bug');
    git('commit', '-q', '--allow-empty', '-m', 'feat: a feature');
    git('commit', '-q', '--allow-empty', '-m', 'docs: words');
    const summary = join(repo, 'summary.md');
    const out = execFileSync(process.execPath, [SCRIPT], {
      cwd: repo,
      encoding: 'utf8',
      env: { ...process.env, GITHUB_STEP_SUMMARY: summary },
    });
    assert.match(out, /releases \*\*1\.3\.0\*\* \(minor bump from 1\.2\.3\)/);
    assert.match(out, /`minor` feat: a feature/);
    assert.match(readFileSync(summary, 'utf8'), /## Release preview/);
  });

  it('reports no release when nothing is releasable', (t) => {
    const repo = tempDir(t);
    const git = (...args) =>
      execFileSync('git', ['-c', 'user.name=t', '-c', 'user.email=t@t', '-c', 'commit.gpgsign=false', ...args], {
        cwd: repo,
      });
    git('init', '-q', '-b', 'main');
    git('commit', '-q', '--allow-empty', '-m', 'docs: only docs');
    const out = execFileSync(process.execPath, [SCRIPT, '', 'HEAD'], {
      cwd: repo,
      encoding: 'utf8',
      env: { ...process.env, GITHUB_STEP_SUMMARY: '' },
    });
    assert.match(out, /No release/);
  });
});
