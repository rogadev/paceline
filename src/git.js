// Current git branch, read from .git/HEAD directly. No git process is spawned:
// that keeps each refresh fast and means a repo's git config (hooks, fsmonitor,
// aliases) can never run code just because paceline rendered.

import { closeSync, openSync, readSync, statSync } from 'node:fs';
import { dirname, isAbsolute, join, resolve } from 'node:path';

const MAX_HEAD_BYTES = 512;
const MAX_BRANCH_LENGTH = 32;

/** First line of a small file, reading at most `maxBytes`. */
function readFirstLine(path, maxBytes = MAX_HEAD_BYTES) {
  const fd = openSync(path, 'r');
  try {
    const buf = Buffer.alloc(maxBytes);
    const n = readSync(fd, buf, 0, maxBytes, 0);
    return buf.toString('utf8', 0, n).split(/\r?\n/, 1)[0];
  } finally {
    closeSync(fd);
  }
}

function kindOf(path) {
  try {
    const s = statSync(path);
    return s.isDirectory() ? 'dir' : s.isFile() ? 'file' : null;
  } catch {
    return null;
  }
}

/** The git directory for `dir`, walking up to the filesystem root. */
export function findGitDir(dir) {
  let current = resolve(dir);
  for (;;) {
    const dotGit = join(current, '.git');
    const kind = kindOf(dotGit);
    if (kind === 'dir') return dotGit;
    if (kind === 'file') {
      // Worktrees and submodules: .git is a file holding "gitdir: <path>".
      const target = readFirstLine(dotGit)
        .replace(/^gitdir:\s*/, '')
        .trim();
      if (!target) return null;
      return isAbsolute(target) ? target : resolve(current, target);
    }
    const parent = dirname(current);
    if (parent === current) return null;
    current = parent;
  }
}

/**
 * The branch name, "detached <sha7>", or null outside a repo. The result is
 * raw repo data; callers must pass it through safeText before printing.
 */
export function getGitBranch(dir) {
  const gitDir = findGitDir(dir);
  if (!gitDir) return null;
  let head;
  try {
    head = readFirstLine(join(gitDir, 'HEAD'));
  } catch {
    return null;
  }
  const ref = /^ref:\s*refs\/heads\/(.+)$/.exec(head);
  if (ref) {
    const branch = ref[1].trim();
    const chars = Array.from(branch);
    return chars.length > MAX_BRANCH_LENGTH ? `${chars.slice(0, MAX_BRANCH_LENGTH - 1).join('')}…` : branch;
  }
  if (/^[0-9a-f]{40,64}$/.test(head.trim())) return `detached ${head.slice(0, 7)}`;
  return null;
}
