/**
 * Finds a folder's git directory, its current branch, and the shas refs point
 * at by reading `.git`, `HEAD`, the ref files, and `packed-refs` directly. No
 * git process starts here, so a repository's config, hooks, or fsmonitor can
 * never run code because the branch was drawn. The batch segment's narrow
 * exception, which does run git, is recorded in docs/notes.md.
 */

/** The file reads gitinfo needs, so tests can stand in for a disk. */
export type GitFs = {
  /** `'file'`, `'dir'`, or `'other'`; null when the path does not exist. */
  kind: (path: string) => Promise<'file' | 'dir' | 'other' | null>
  /** The file's text; null when it cannot be read. */
  read: (path: string) => Promise<string | null>
}

/** A repository found from a folder: its git directory and the work tree that holds `.git`. */
export type Repo = { gitDir: string; workTree: string }

/** What HEAD names: a branch, with a null sha while the branch has no commit yet, or a detached commit. */
export type Head =
  | {
      kind: 'branch'
      /** The full ref name, for example `refs/heads/dev`. */
      ref: string
      sha: string | null
    }
  | { kind: 'detached'; sha: string }

/** A full SHA-1 or SHA-256 object id, in git's lowercase hex. */
export const FULL_SHA = /^[0-9a-f]{40}(?:[0-9a-f]{24})?$/

const MAX_HEAD_CHARS = 512
const MAX_BRANCH_CHARS = 32
const MAX_REF_CHARS = 256
const BRANCH_PREFIX = 'refs/heads/'
// Characters git never allows in a ref name, plus the separators that could
// turn a name read from HEAD into a path outside the git directory.
const REF_FORBIDDEN = /[\x00-\x20\x7f~^:?*[\\]/

/** Normalizes a path to forward slashes without a trailing separator. */
export function slashPath(p: string): string {
  const s = p.replaceAll('\\', '/')
  return s.length > 1 && s.endsWith('/') && !/^[A-Za-z]:\/$/.test(s) ? s.replace(/\/+$/, '') : s
}

function parent(p: string): string {
  const i = p.lastIndexOf('/')
  if (i < 0) return p
  if (i === 0) return '/'
  if (/^[A-Za-z]:$/.test(p.slice(0, i))) return p.slice(0, i + 1)
  return p.slice(0, i)
}

const join = (dir: string, name: string) => (dir.endsWith('/') ? dir + name : `${dir}/${name}`)
const isAbsolute = (p: string) => p.startsWith('/') || /^[A-Za-z]:\//.test(p)

// Joins a relative path onto `dir`, folding `.` and `..` segments.
function resolvePath(dir: string, rel: string): string {
  let out = dir
  for (const part of rel.split('/')) {
    if (part === '' || part === '.') continue
    out = part === '..' ? parent(out) : join(out, part)
  }
  return out
}

/**
 * Resolves `dir` against `base`, as Go's `filepath.Abs` resolves a path
 * against the working directory: an absolute `dir` stands, and a relative one
 * is joined onto `base` with its `.` and `..` segments folded. The result uses
 * forward slashes.
 */
export function absoluteDir(base: string, dir: string): string {
  const d = slashPath(dir)
  return isAbsolute(d) ? d : resolvePath(slashPath(base), d)
}

async function firstLine(fs: GitFs, path: string): Promise<string | null> {
  const text = await fs.read(path)
  if (text === null) return null
  return text.slice(0, MAX_HEAD_CHARS).split('\n')[0].replace(/\r$/, '')
}

function isRefName(name: string): boolean {
  return (
    name.length <= MAX_REF_CHARS &&
    name.startsWith('refs/') &&
    !name.endsWith('/') &&
    !name.includes('..') &&
    !name.includes('//') &&
    !REF_FORBIDDEN.test(name)
  )
}

// HEAD's first line as the ref it names or the commit it holds.
function parseHead(line: string): { ref: string } | { sha: string } | null {
  const ref = /^ref:\s*(.+)$/.exec(line)
  if (ref) return { ref: ref[1].trim() }
  const sha = line.trim()
  return /^[0-9a-f]{40,64}$/.test(sha) ? { sha } : null
}

/**
 * Finds the repository holding `dir`, walking up to the filesystem root. In a
 * worktree or submodule, `.git` is a file that holds `gitdir: <path>`.
 *
 * @returns The repository, or null outside one.
 */
export async function findRepo(fs: GitFs, dir: string): Promise<Repo | null> {
  let current = slashPath(dir)
  for (;;) {
    const dotGit = join(current, '.git')
    const kind = await fs.kind(dotGit)
    if (kind === 'dir') return { gitDir: dotGit, workTree: current }
    if (kind === 'file') {
      const line = await firstLine(fs, dotGit)
      if (line === null || !line.startsWith('gitdir:')) return null
      const target = slashPath(line.slice('gitdir:'.length).trim())
      if (target === '') return null
      return { gitDir: isAbsolute(target) ? target : join(current, target), workTree: current }
    }
    const up = parent(current)
    if (up === current) return null
    current = up
  }
}

// A Windows path (drive letter or UNC share), whose names match in any case.
const WINDOWS_PATH = /^(?:[A-Za-z]:\/|\/\/)/

// Folds the case of a Windows path, so two spellings of one folder compare equal.
const foldPath = (p: string) => (WINDOWS_PATH.test(p) ? p.toLowerCase() : p)

// Reports whether `path` is `root` or a folder under it.
function isWithin(path: string, root: string): boolean {
  const p = foldPath(slashPath(path))
  const r = foldPath(slashPath(root))
  return p === r || p.startsWith(r.endsWith('/') ? r : `${r}/`)
}

/**
 * Checks whether a repository found from `startDir` may take a write. A `.git`
 * in `startDir` itself always may. One found by walking up may only when the
 * folder holding it sits inside one of `trustedRoots` (the user's home and the
 * session's folder), because another account on the machine can plant a
 * `.git` above them, for example at `C:\.git` or `/tmp/.git`.
 *
 * @param trustedRoots - Absolute folders; empty strings are ignored.
 */
export function mayWriteRepo(repo: Repo, startDir: string, trustedRoots: readonly string[]): boolean {
  if (foldPath(slashPath(repo.workTree)) === foldPath(slashPath(startDir))) return true
  return trustedRoots.some(root => root !== '' && isWithin(repo.workTree, root))
}

/**
 * Returns the branch checked out in `gitDir`, `detached <sha7>`, or `''`.
 * The result is raw repository data: clean it before drawing it.
 */
export async function branchIn(fs: GitFs, gitDir: string): Promise<string> {
  if (gitDir === '') return ''
  const line = await firstLine(fs, join(gitDir, 'HEAD'))
  const head = line === null ? null : parseHead(line)
  if (head === null) return ''
  if ('sha' in head) return `detached ${head.sha.slice(0, 7)}`
  if (!head.ref.startsWith(BRANCH_PREFIX)) return ''
  const branch = [...head.ref.slice(BRANCH_PREFIX.length).trim()]
  return branch.length > MAX_BRANCH_CHARS
    ? branch.slice(0, MAX_BRANCH_CHARS - 1).join('') + '\u2026'
    : branch.join('')
}

/**
 * Gets the common git directory: `gitDir` itself, or, in a linked worktree,
 * where its `commondir` file points. Branches, remotes, and `packed-refs` live
 * there; `HEAD` stays in `gitDir`.
 */
export async function commonDir(fs: GitFs, gitDir: string): Promise<string> {
  const line = await firstLine(fs, join(gitDir, 'commondir'))
  const target = slashPath(line?.trim() ?? '')
  if (target === '') return gitDir
  return isAbsolute(target) ? target : resolvePath(gitDir, target)
}

/**
 * Resolves a full ref name to its sha from the ref's loose file in the common
 * git directory, falling back to that directory's `packed-refs`, as git does.
 *
 * @param refName - The full ref name, for example `refs/remotes/origin/main`. A
 *     name git would refuse, or one that could lead outside the git directory,
 *     resolves to null without a read.
 * @returns The sha, or null when neither file names the ref.
 */
export async function resolveRef(fs: GitFs, gitDir: string, refName: string): Promise<string | null> {
  if (gitDir === '' || !isRefName(refName)) return null
  const common = await commonDir(fs, gitDir)
  const loose = (await fs.read(join(common, refName)))?.trim() ?? ''
  if (FULL_SHA.test(loose)) return loose
  const packed = await fs.read(join(common, 'packed-refs'))
  if (packed === null) return null
  for (const lineText of packed.split('\n')) {
    const [sha, name] = lineText.trim().split(' ')
    if (name === refName && FULL_SHA.test(sha)) return sha
  }
  return null
}

/**
 * Reads what HEAD names in `gitDir`, and the sha of the branch it names.
 *
 * @returns The head, or null when HEAD is missing, unreadable, or names
 *     something other than a valid branch under `refs/heads/` or a full sha.
 */
export async function readHead(fs: GitFs, gitDir: string): Promise<Head | null> {
  if (gitDir === '') return null
  const line = await firstLine(fs, join(gitDir, 'HEAD'))
  const head = line === null ? null : parseHead(line)
  if (head === null) return null
  if ('sha' in head) return FULL_SHA.test(head.sha) ? { kind: 'detached', sha: head.sha } : null
  if (!head.ref.startsWith(BRANCH_PREFIX) || !isRefName(head.ref)) return null
  return { kind: 'branch', ref: head.ref, sha: await resolveRef(fs, gitDir, head.ref) }
}
