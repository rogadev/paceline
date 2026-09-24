# Contributing

## Branches

- **`dev`** is where work happens. Commit or merge feature branches into `dev`.
- **`main`** is what's released. It only changes through a pull request from `dev`, and only when CI passes.

Merging a pull request into `main` releases automatically. Nobody picks the version number: it comes from the commit messages.

## How commit messages choose the version

paceline uses [Conventional Commits](https://www.conventionalcommits.org) and [semantic-release](https://semantic-release.gitbook.io). A version has three numbers, `MAJOR.MINOR.PATCH`. When `dev` is merged, every commit since the last release is read, and the largest change wins:

| Commit | Example | Version change | 1.2.3 becomes |
|---|---|---|---|
| Breaking change | `feat!: rename config keys` or a `BREAKING CHANGE:` footer | **Major**: users may need to change something | **2.0.0** |
| New feature | `feat: add a cost segment` | **Minor**: new, backward-compatible | **1.3.0** |
| Bug fix or speedup | `fix: handle CRLF in HEAD`, `perf: read fewer bytes` | **Patch**: nothing new, something works better | **1.2.4** |
| Anything else | `docs:`, `test:`, `ci:`, `chore:`, `refactor:`, `style:`, `build:` | No release | stays 1.2.3 |

When a number goes up, the numbers to its right reset to zero. A pull request with one `feat:` and three `fix:` commits is a minor release.

**What counts as breaking** for paceline: removing or renaming a config key or segment name, changing the `install` or `uninstall` behavior in a way that affects existing users, or raising the minimum Node.js version.

### Before you merge

Every pull request into `main` shows a **Release preview** in the CI job summary, for example "This merge releases **1.3.0** (minor bump from 1.2.3)", with each commit labeled by the bump it causes. To see the same preview locally:

```sh
npm run bump
```

Commit messages are checked twice: by a local `commit-msg` hook (installed when you run `npm install`), and by CI on every pull request. A message semantic-release can't read fails the build.

## Development

```sh
npm install
npm test              # the full suite
npm run test:coverage # with coverage thresholds
npm run lint          # Biome
npm run format        # Biome, applying fixes
```

The runtime has zero dependencies. Anything added to `dependencies` fails `test/package.test.js`. So does shipped code that imports `child_process`, a network module, `eval`, or `new Function`.

To try a local build as your own status line:

```sh
node bin/paceline.js install --force
```

## Release setup (maintainers)

- The release workflow tags the version and creates the GitHub release with notes generated from the commits.
- Publishing to npm turns on when the repository has an `NPM_TOKEN` secret. Without it, releases still tag and appear on GitHub.
- `package.json` stays at `0.0.0-development` on purpose. The real version lives in the git tag and is written into the package only at publish time, so `dev` never needs a back-merge from `main`.
