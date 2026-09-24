// Renders release notes through the real semantic-release plugins with this
// repo's config. The changelog preset and the writer semantic-release bundles
// have to agree on a major version; when they don't, notes fail to render and
// the release job dies after the merge. This catches that on the pull request.
//
// Needs dev dependencies, so it skips in the zero-install test matrix and runs
// in the quality job, which installs them.

import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { describe, it } from 'node:test';
import releaseConfig from '../release.config.js';

const require = createRequire(import.meta.url);
const hasDevDeps = (() => {
  try {
    require.resolve('@semantic-release/release-notes-generator');
    return true;
  } catch {
    return false;
  }
})();

const pluginConfig = (name) => {
  const entry = releaseConfig.plugins.find((p) => (Array.isArray(p) ? p[0] : p) === name);
  return Array.isArray(entry) ? entry[1] : {};
};

const logger = { log() {}, warn() {}, error() {}, success() {} };

const context = (messages) => ({
  cwd: process.cwd(),
  env: {},
  logger,
  options: { repositoryUrl: 'https://github.com/rogadev/paceline.git' },
  lastRelease: { gitTag: 'v1.2.3', version: '1.2.3' },
  nextRelease: { gitTag: 'v1.3.0', version: '1.3.0' },
  commits: messages.map((message, i) => ({
    hash: `${i}`.padStart(40, 'a'),
    message,
    committerDate: '2026-09-24T00:00:00Z',
  })),
});

describe('release config', { skip: !hasDevDeps && 'dev dependencies not installed' }, () => {
  it('renders release notes with the configured preset', async () => {
    const { generateNotes } = await import('@semantic-release/release-notes-generator');
    const notes = await generateNotes(
      pluginConfig('@semantic-release/release-notes-generator'),
      context(['feat: add a segment', 'fix: handle CRLF']),
    );
    assert.match(notes, /### Features/);
    assert.match(notes, /add a segment/);
    assert.match(notes, /### Bug Fixes/);
  });

  it('analyzes commits the way CONTRIBUTING.md documents', async () => {
    const { analyzeCommits } = await import('@semantic-release/commit-analyzer');
    const config = pluginConfig('@semantic-release/commit-analyzer');
    const bump = (messages) => analyzeCommits(config, context(messages));
    assert.equal(await bump(['fix: a']), 'patch');
    assert.equal(await bump(['perf: a']), 'patch');
    assert.equal(await bump(['feat: a']), 'minor');
    assert.equal(await bump(['feat!: a']), 'major');
    assert.equal(await bump(['refactor: a\n\nBREAKING CHANGE: b']), 'major');
    assert.equal(await bump(['docs: a', 'chore: b']), null);
  });
});
