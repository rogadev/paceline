import assert from 'node:assert/strict';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, it } from 'node:test';
import { findGitDir, getGitBranch } from '../src/git.js';
import { tempDir } from './helpers.js';

/** A fake repo: just a .git dir with a HEAD file. No git binary needed. */
function fakeRepo(t, head) {
  const root = tempDir(t);
  mkdirSync(join(root, '.git'));
  writeFileSync(join(root, '.git', 'HEAD'), head);
  return root;
}

describe('getGitBranch', () => {
  it('reads the branch from HEAD', (t) => {
    assert.equal(getGitBranch(fakeRepo(t, 'ref: refs/heads/dev\n')), 'dev');
  });

  it('handles CRLF and slashes in branch names', (t) => {
    assert.equal(getGitBranch(fakeRepo(t, 'ref: refs/heads/feature/x\r\n')), 'feature/x');
  });

  it('finds the repo from a nested folder', (t) => {
    const root = fakeRepo(t, 'ref: refs/heads/main\n');
    const nested = join(root, 'a', 'b');
    mkdirSync(nested, { recursive: true });
    assert.equal(getGitBranch(nested), 'main');
  });

  it('shows a detached HEAD as the short commit', (t) => {
    assert.equal(getGitBranch(fakeRepo(t, `${'a01112d'.padEnd(40, '0')}\n`)), 'detached a01112d');
  });

  it('truncates long branch names', (t) => {
    const branch = getGitBranch(fakeRepo(t, `ref: refs/heads/${'x'.repeat(80)}\n`));
    assert.equal(Array.from(branch).length, 32);
    assert.ok(branch.endsWith('…'));
  });

  it('follows a worktree .git file, relative or absolute', (t) => {
    const real = tempDir(t);
    writeFileSync(join(real, 'HEAD'), 'ref: refs/heads/wt-branch\n');
    const wt = tempDir(t);
    writeFileSync(join(wt, '.git'), `gitdir: ${real}\n`);
    assert.equal(getGitBranch(wt), 'wt-branch');

    const rel = tempDir(t);
    mkdirSync(join(rel, 'gd'));
    writeFileSync(join(rel, 'gd', 'HEAD'), 'ref: refs/heads/rel\n');
    writeFileSync(join(rel, '.git'), 'gitdir: gd\n');
    assert.equal(getGitBranch(rel), 'rel');
  });

  it('returns null for unknown HEAD contents, empty gitdir files, and missing HEAD', (t) => {
    assert.equal(getGitBranch(fakeRepo(t, 'garbage\n')), null);
    const empty = tempDir(t);
    writeFileSync(join(empty, '.git'), 'gitdir:\n');
    assert.equal(findGitDir(empty), null);
    const noHead = tempDir(t);
    mkdirSync(join(noHead, '.git'));
    assert.equal(getGitBranch(noHead), null);
  });

  it('reads only the start of an oversized HEAD file', (t) => {
    const root = fakeRepo(t, `ref: refs/heads/${'y'.repeat(10_000_000)}`);
    const branch = getGitBranch(root);
    assert.ok(branch === null || Array.from(branch).length <= 32);
  });
});
