// semantic-release: every push to main is analyzed, and if it contains
// releasable commits, a version is tagged and published.
//
//   fix: / perf:                     -> patch  (1.2.3 -> 1.2.4)
//   feat:                            -> minor  (1.2.3 -> 1.3.0)
//   feat!: / BREAKING CHANGE: footer -> major  (1.2.3 -> 2.0.0)
//   docs:, test:, ci:, chore:, ...   -> no release
//
// The version is never committed back to the repo: package.json stays at
// 0.0.0-development and the published tarball gets the real version. The git
// tag and GitHub release are the source of truth, and dev never needs a
// back-merge from main.
//
// npm publishing turns on when the NPM_TOKEN secret exists. Without it, runs
// still tag and create the GitHub release, so the pipeline works before the
// npm account is connected.

const preset = 'conventionalcommits';

export default {
  branches: ['main'],
  plugins: [
    ['@semantic-release/commit-analyzer', { preset }],
    ['@semantic-release/release-notes-generator', { preset }],
    ['@semantic-release/npm', { npmPublish: Boolean(process.env.NPM_TOKEN) }],
    '@semantic-release/github',
  ],
};
