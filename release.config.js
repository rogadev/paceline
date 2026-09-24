// semantic-release: every push to main is analyzed, and if it contains
// releasable commits, a version is tagged and released.
//
//   fix: / perf:                     -> patch  (1.2.3 -> 1.2.4)
//   feat:                            -> minor  (1.2.3 -> 1.3.0)
//   feat!: / BREAKING CHANGE: footer -> major  (1.2.3 -> 2.0.0)
//   docs:, test:, ci:, chore:, ...   -> no release
//
// Order matters in the publish step: the GitHub plugin creates the release
// with generated notes, then GoReleaser builds every platform and uploads the
// binaries and checksums into that release (.goreleaser.yaml keeps the notes).
//
// The version is never committed back to the repo. The git tag is the source
// of truth, GoReleaser stamps it into the binary, and dev never needs a
// back-merge from main.

const preset = 'conventionalcommits';

export default {
  branches: ['main'],
  plugins: [
    ['@semantic-release/commit-analyzer', { preset }],
    ['@semantic-release/release-notes-generator', { preset }],
    '@semantic-release/github',
    ['@semantic-release/exec', { publishCmd: 'goreleaser release --clean' }],
  ],
};
