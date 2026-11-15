import { describe, expect, test } from 'claude-code/testing'

import {
  absoluteDir,
  branchIn,
  commonDir,
  findRepo,
  mayWriteRepo,
  readHead,
  resolveRef,
  type GitFs,
} from '../src/gitinfo.ts'

const SHA_A = '941a425efb86dc5cc3a59082283721566108dbc8'
const SHA_B = 'c69f8f68635e733b01b54cc7316e3248465374f3'
const SHA_C = '6e333d93fd9ea3c0fe09a8edc0c4ced7748071cf'

// A disk with only `files` and `dirs` on it. `reads` records every path read.
function fakeFs(files: Record<string, string>, dirs: string[] = [], reads: string[] = []): GitFs {
  return {
    kind: async path => (path in files ? 'file' : dirs.includes(path) ? 'dir' : null),
    read: async path => {
      reads.push(path)
      return files[path] ?? null
    },
  }
}

describe('git branch', () => {
  test('walks up to the repository and reads the branch', async () => {
    const fs = fakeFs({ 'C:/r/app/.git/HEAD': 'ref: refs/heads/dev\n' }, ['C:/r/app/.git'])
    const repo = await findRepo(fs, 'C:\\r\\app\\src\\deep')
    expect(repo).toEqual({ gitDir: 'C:/r/app/.git', workTree: 'C:/r/app' })
    expect(await branchIn(fs, repo!.gitDir)).toBe('dev')
  })

  test('follows a worktree .git file', async () => {
    const fs = fakeFs({
      '/w/tree/.git': 'gitdir: /r/app/.git/worktrees/tree\n',
      '/r/app/.git/worktrees/tree/HEAD': 'ref: refs/heads/feat/x',
    })
    const repo = await findRepo(fs, '/w/tree')
    expect(repo).toEqual({ gitDir: '/r/app/.git/worktrees/tree', workTree: '/w/tree' })
    expect(await branchIn(fs, repo!.gitDir)).toBe('feat/x')
  })

  test('shows a detached head and caps a long branch', async () => {
    expect(await branchIn(fakeFs({ '/g/HEAD': SHA_A }), '/g')).toBe('detached 941a425')
    const long = await branchIn(fakeFs({ '/g/HEAD': 'ref: refs/heads/' + 'b'.repeat(40) }), '/g')
    expect(long).toBe('b'.repeat(31) + '\u2026')
  })

  test('resolves a relative gitdir against the folder that holds .git', async () => {
    const fs = fakeFs({ 'C:/w/sub/.git': 'gitdir: ../main/.git/worktrees/sub\n' })
    expect(await findRepo(fs, 'C:/w/sub')).toEqual({ gitDir: 'C:/w/sub/../main/.git/worktrees/sub', workTree: 'C:/w/sub' })
  })

  test('follows an absolute Windows gitdir pointer', async () => {
    const fs = fakeFs({ 'D:/wt/feature/.git': 'gitdir: C:\\r\\app\\.git\\worktrees\\feature\r\n' })
    expect(await findRepo(fs, 'D:\\wt\\feature')).toEqual({
      gitDir: 'C:/r/app/.git/worktrees/feature',
      workTree: 'D:/wt/feature',
    })
  })

  test('a .git file without a gitdir line, or with an empty one, is no repo', async () => {
    expect(await findRepo(fakeFs({ '/w/.git': 'not a pointer\n' }), '/w')).toBe(null)
    expect(await findRepo(fakeFs({ '/w/.git': 'gitdir:   \n' }), '/w')).toBe(null)
  })

  test('outside a repository there is no repo and no branch', async () => {
    const fs = fakeFs({})
    expect(await findRepo(fs, 'C:/nowhere/at/all')).toBe(null)
    expect(await findRepo(fs, '/nowhere')).toBe(null)
    expect(await branchIn(fs, '')).toBe('')
  })
})

describe('readHead', () => {
  test('reads a branch and the sha it points at', async () => {
    const fs = fakeFs({ '/g/HEAD': 'ref: refs/heads/dev\n', '/g/refs/heads/dev': SHA_A + '\n' })
    expect(await readHead(fs, '/g')).toEqual({ kind: 'branch', ref: 'refs/heads/dev', sha: SHA_A })
  })

  test('reads an unborn branch with no sha', async () => {
    expect(await readHead(fakeFs({ '/g/HEAD': 'ref: refs/heads/main\n' }), '/g')).toEqual({
      kind: 'branch',
      ref: 'refs/heads/main',
      sha: null,
    })
  })

  test('reads a detached head', async () => {
    expect(await readHead(fakeFs({ '/g/HEAD': SHA_B + '\n' }), '/g')).toEqual({ kind: 'detached', sha: SHA_B })
  })

  test('reads a detached SHA-256 head', async () => {
    const sha256 = SHA_A + SHA_B.slice(0, 24)
    expect(await readHead(fakeFs({ '/g/HEAD': sha256 + '\n' }), '/g')).toEqual({ kind: 'detached', sha: sha256 })
  })

  test('reads garbage or a missing HEAD as null', async () => {
    expect(await readHead(fakeFs({ '/g/HEAD': 'not a head' }), '/g')).toBe(null)
    expect(await readHead(fakeFs({}), '/g')).toBe(null)
    expect(await readHead(fakeFs({}), '')).toBe(null)
  })

  test('reads a symbolic HEAD outside refs/heads as no branch', async () => {
    const fs = (target: string) =>
      fakeFs({ '/g/HEAD': `ref: ${target}\n`, [`/g/${target}`]: SHA_A + '\n' })
    expect(await readHead(fs('refs/tags/v1'), '/g')).toBe(null)
    expect(await readHead(fs('refs/remotes/origin/HEAD'), '/g')).toBe(null)
  })

  test('reads a hex HEAD that is not a full sha as null', async () => {
    expect(await readHead(fakeFs({ '/g/HEAD': 'a'.repeat(41) + '\n' }), '/g')).toBe(null)
    expect(await readHead(fakeFs({ '/g/HEAD': 'a'.repeat(63) + '\n' }), '/g')).toBe(null)
  })

  test('reads a HEAD that names an invalid ref as null', async () => {
    expect(await readHead(fakeFs({ '/g/HEAD': 'ref: refs/heads/../../../x\n' }), '/g')).toBe(null)
  })
})

describe('resolveRef', () => {
  const PACKED = [
    '# pack-refs with: peeled fully-peeled sorted ',
    `${SHA_A} refs/remotes/origin/dev`,
    `${SHA_B} refs/remotes/origin/main`,
    `^${SHA_C}`,
    '',
  ].join('\n')
  const MAIN = 'refs/remotes/origin/main'

  test('reads the sha from a loose ref file', async () => {
    const fs = fakeFs({ '/g/refs/remotes/origin/main': SHA_A + '\n', '/g/packed-refs': PACKED })
    expect(await resolveRef(fs, '/g', MAIN)).toBe(SHA_A)
  })

  test('falls back to packed-refs when there is no loose file', async () => {
    expect(await resolveRef(fakeFs({ '/g/packed-refs': PACKED }), '/g', MAIN)).toBe(SHA_B)
    const crlf = fakeFs({ '/g/packed-refs': PACKED.replaceAll('\n', '\r\n') })
    expect(await resolveRef(crlf, '/g', MAIN)).toBe(SHA_B)
  })

  test('returns null when neither names the ref', async () => {
    expect(await resolveRef(fakeFs({ '/g/packed-refs': PACKED }), '/g', 'refs/remotes/origin/feature')).toBe(null)
    expect(await resolveRef(fakeFs({}), '/g', MAIN)).toBe(null)
  })

  test('ignores a loose file that does not hold a sha', async () => {
    const fs = fakeFs({ '/g/refs/remotes/origin/main': 'ref: refs/remotes/origin/dev\n', '/g/packed-refs': PACKED })
    expect(await resolveRef(fs, '/g', MAIN)).toBe(SHA_B)
    expect(await resolveRef(fakeFs({ '/g/refs/remotes/origin/main': 'garbage' }), '/g', MAIN)).toBe(null)
  })

  test('does not match a ref whose name only starts with the one asked for', async () => {
    const fs = fakeFs({ '/g/packed-refs': `${SHA_A} refs/remotes/origin/main-old\n` })
    expect(await resolveRef(fs, '/g', MAIN)).toBe(null)
  })

  test('skips a packed line whose sha is not a full sha', async () => {
    const fs = fakeFs({ '/g/packed-refs': `${SHA_A.slice(0, 12)} ${MAIN}\nzz${SHA_A.slice(2)} ${MAIN}\n` })
    expect(await resolveRef(fs, '/g', MAIN)).toBe(null)
  })

  test('reads branches, remotes, and packed-refs from the common dir of a worktree', async () => {
    const fs = fakeFs({
      '/r/app/.git/worktrees/tree/HEAD': 'ref: refs/heads/dev\n',
      '/r/app/.git/worktrees/tree/commondir': '../..\n',
      '/r/app/.git/refs/heads/dev': SHA_A + '\n',
      '/r/app/.git/packed-refs': PACKED,
    })
    const gitDir = '/r/app/.git/worktrees/tree'
    expect(await commonDir(fs, gitDir)).toBe('/r/app/.git')
    expect(await resolveRef(fs, gitDir, 'refs/heads/dev')).toBe(SHA_A)
    expect(await resolveRef(fs, gitDir, MAIN)).toBe(SHA_B)
    expect(await readHead(fs, gitDir)).toEqual({ kind: 'branch', ref: 'refs/heads/dev', sha: SHA_A })
  })

  test('follows an absolute commondir', async () => {
    const fs = fakeFs({
      'C:/w/.git/worktrees/x/commondir': 'C:\\r\\app\\.git\r\n',
      'C:/r/app/.git/refs/remotes/origin/main': SHA_C,
    })
    expect(await commonDir(fs, 'C:/w/.git/worktrees/x')).toBe('C:/r/app/.git')
    expect(await resolveRef(fs, 'C:/w/.git/worktrees/x', MAIN)).toBe(SHA_C)
  })

  test('folds a relative commondir under a drive-letter git dir', async () => {
    const fs = fakeFs({
      'C:/r/app/.git/worktrees/tree/commondir': '../..\r\n',
      'C:/r/app/.git/packed-refs': `${SHA_B} ${MAIN}\n`,
    })
    expect(await commonDir(fs, 'C:/r/app/.git/worktrees/tree')).toBe('C:/r/app/.git')
    expect(await resolveRef(fs, 'C:/r/app/.git/worktrees/tree', MAIN)).toBe(SHA_B)
  })

  test('without a commondir file the git dir is its own common dir', async () => {
    expect(await commonDir(fakeFs({}), '/g')).toBe('/g')
    expect(await commonDir(fakeFs({ '/g/commondir': '  \n' }), '/g')).toBe('/g')
  })

  test('refuses a ref name that could leave the git directory, without reading it', async () => {
    for (const bad of ['refs/../../etc/passwd', 'refs\\heads\\dev', 'HEAD', 'refs/heads//dev', 'refs/heads/a b', '']) {
      const reads: string[] = []
      expect(await resolveRef(fakeFs({ '/g/packed-refs': PACKED }, [], reads), '/g', bad)).toBe(null)
      expect(reads.filter(p => p !== '/g/commondir')).toEqual([])
    }
  })
})

describe('absoluteDir', () => {
  test('keeps an absolute directory, in forward slashes', () => {
    expect(absoluteDir('C:/r/app', 'D:\\other\\repo')).toBe('D:/other/repo')
    expect(absoluteDir('/r/app', '/w/tree/')).toBe('/w/tree')
  })

  test('resolves a relative directory against the base', () => {
    expect(absoluteDir('C:\\r\\app', 'src\\deep')).toBe('C:/r/app/src/deep')
    expect(absoluteDir('/r/app', './src/../docs')).toBe('/r/app/docs')
    expect(absoluteDir('/r/app', '..')).toBe('/r')
  })

  test('lets findRepo walk up from a relative directory', async () => {
    const fs = fakeFs({}, ['C:/r/app/.git'])
    expect(await findRepo(fs, absoluteDir('C:/r/app', 'src/deep'))).toEqual({
      gitDir: 'C:/r/app/.git',
      workTree: 'C:/r/app',
    })
  })
})

describe('mayWriteRepo', () => {
  const roots = ['C:\\Users\\me', '', 'C:/Users/me/proj/app']

  test('allows a .git in the folder it was looked up from, wherever that is', () => {
    expect(mayWriteRepo({ gitDir: 'C:/.git', workTree: 'C:/' }, 'C:\\', roots)).toBe(true)
    expect(mayWriteRepo({ gitDir: 'D:/code/.git', workTree: 'D:/code' }, 'd:/Code/', roots)).toBe(true)
  })

  test('allows a walk-up that ends inside the home folder', () => {
    expect(mayWriteRepo({ gitDir: 'C:/Users/me/proj/.git', workTree: 'C:/Users/me/proj' }, 'C:/Users/me/proj/src', roots)).toBe(true)
    expect(mayWriteRepo({ gitDir: 'c:/users/ME/p/.git', workTree: 'c:/users/ME/p' }, 'c:/users/ME/p/x', roots)).toBe(true)
  })

  test('refuses a walk-up to a .git planted above the home folder', () => {
    expect(mayWriteRepo({ gitDir: 'C:/.git', workTree: 'C:/' }, 'C:/Users/me/notes', roots)).toBe(false)
    expect(mayWriteRepo({ gitDir: 'D:/evil', workTree: 'D:/' }, 'D:/scratch/a', roots)).toBe(false)
    expect(mayWriteRepo({ gitDir: '/tmp/.git', workTree: '/tmp' }, '/tmp/work', ['/home/me'])).toBe(false)
  })

  test('does not take a sibling whose name starts with a trusted folder', () => {
    expect(mayWriteRepo({ gitDir: 'C:/Users/me2/.git', workTree: 'C:/Users/me2' }, 'C:/Users/me2/x', roots)).toBe(false)
  })

  test('matches case only on Windows paths', () => {
    expect(mayWriteRepo({ gitDir: '/Home/me/p/.git', workTree: '/Home/me/p' }, '/Home/me/p/x', ['/home/me'])).toBe(false)
  })

  test('ignores empty roots rather than trusting everything', () => {
    expect(mayWriteRepo({ gitDir: '/srv/.git', workTree: '/srv' }, '/srv/a', [''])).toBe(false)
  })
})
