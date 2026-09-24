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

**What counts as breaking** for paceline: removing or renaming a config key or segment name, changing the `install` or `uninstall` behavior in a way that affects existing users, or raising the minimum Go version for `go install`.

### Before you merge

Every pull request into `main` shows a **Release preview** in the CI job summary, for example "This merge releases **1.3.0** (minor bump from 1.2.3)", with each commit labeled by the bump it causes. To see the same preview locally:

```sh
go run ./tools/nextbump
```

Commit messages are checked twice: by a local `commit-msg` hook (installed when you run `npm install`), and by CI on every pull request. A message semantic-release can't read fails the build.

## Development

paceline is Go (1.26 or later) with no dependencies. Node.js runs only the release tooling: commitlint and semantic-release.

```sh
npm install            # release tooling, and the commit-msg hook
go test ./...          # the full suite
go test -race ./...    # with the race detector
go vet ./...
gofmt -l .             # lists unformatted files; gofmt -w . fixes them
```

CI also runs [golangci-lint](https://golangci-lint.run) (config in `.golangci.yml`), `govulncheck`, a 90% coverage floor, and a GoReleaser snapshot build of every platform.

The code layout follows Go convention:

- `cmd/paceline` is the binary: argument handling, stdin, and the install commands.
- `internal/` holds one package per concern: `payload` (decoding Claude Code's JSON), `render`, `pace`, `color`, `gitinfo`, `config`, `install`, `sanitize`, and `timefmt`. Tests sit next to the code they cover, as `*_test.go`.
- `internal/policy` holds repository-wide rules tested like behavior: no dependencies, no dangerous imports, and ASCII-only Go source.
- `internal/render/testdata/parity.json` holds outputs recorded from the 1.0 JavaScript version. The render tests must reproduce them byte for byte.
- `tools/nextbump` is the release preview, a development tool that isn't part of the binary.

To try a local build as your own status line:

```sh
go build -o paceline ./cmd/paceline
./paceline install --force
```

`paceline install` refuses a `go run` build, because that binary is deleted when the command exits.

## Release setup (maintainers)

- The release workflow tags the version and creates the GitHub release with notes generated from the commits.
- After tagging, semantic-release runs GoReleaser, which builds the six platform binaries and uploads them with `checksums.txt` into the release. The workflow then attaches a signed build provenance attestation to every archive.
- The version lives only in the git tag. GoReleaser stamps it into the binary with `-ldflags`, and `go install ...@v1.2.3` records it in the build info, so nothing is committed back and `dev` never needs a back-merge from `main`.
