#!/usr/bin/env node
// Preview which part of the version a merge will bump, using the same rules
// semantic-release applies on main (see release.config.js).
//
//   node scripts/next-bump.js [from] [to]    default: <latest tag>..HEAD
//
// In GitHub Actions the result is also written to the job summary, so every
// dev -> main pull request shows "this merge releases 1.3.0 (minor)".

import { execFileSync } from 'node:child_process';
import { appendFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';

const RANK = { none: 0, patch: 1, minor: 2, major: 3 };
const HEADER = /^(\w+)(?:\([^)]*\))?(!)?: \S/;
const BREAKING_FOOTER = /^BREAKING[ -]CHANGE: /m;

/** 'major' | 'minor' | 'patch' | 'none' for one full commit message. */
export function classifyCommit(message) {
  const [subject = '', ...rest] = message.split('\n');
  const m = HEADER.exec(subject);
  if (!m) return 'none';
  if (m[2] || BREAKING_FOOTER.test(rest.join('\n'))) return 'major';
  if (m[1] === 'feat') return 'minor';
  if (m[1] === 'fix' || m[1] === 'perf') return 'patch';
  return 'none';
}

/** The largest bump among commit messages. */
export function highestBump(messages) {
  return messages.map(classifyCommit).reduce((a, b) => (RANK[b] > RANK[a] ? b : a), 'none');
}

/** Apply a bump to "x.y.z". A first release is 1.0.0 regardless of type. */
export function nextVersion(current, bump) {
  if (bump === 'none') return null;
  if (!current) return '1.0.0';
  const [maj, min, pat] = current.split('.').map(Number);
  if (bump === 'major') return `${maj + 1}.0.0`;
  if (bump === 'minor') return `${maj}.${min + 1}.0`;
  return `${maj}.${min}.${pat + 1}`;
}

function git(...args) {
  return execFileSync('git', args, { encoding: 'utf8' }).trim();
}

function latestTag() {
  try {
    return git('describe', '--tags', '--abbrev=0', '--match', 'v[0-9]*');
  } catch {
    return null;
  }
}

function main([from, to = 'HEAD']) {
  const tag = latestTag();
  const base = from ?? tag;
  const range = base ? `${base}..${to}` : to;
  const SEP = '\u001e';
  const raw = git('log', '--no-merges', `--format=%B${SEP}`, range);
  const messages = raw
    .split(SEP)
    .map((s) => s.trim())
    .filter(Boolean);
  const bump = highestBump(messages);
  const current = tag ? tag.replace(/^v/, '') : null;
  const next = nextVersion(current, bump);

  const lines = [
    bump === 'none'
      ? `No release: none of the ${messages.length} commit(s) are feat, fix, perf, or breaking.`
      : `This merge releases **${next}** (${bump} bump from ${current ?? 'nothing yet'}).`,
    '',
    ...messages.map((msg) => `- \`${classifyCommit(msg)}\` ${msg.split('\n')[0]}`),
  ];
  const text = lines.join('\n');
  console.log(text);
  if (process.env.GITHUB_STEP_SUMMARY)
    appendFileSync(process.env.GITHUB_STEP_SUMMARY, `## Release preview\n\n${text}\n`);
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) main(process.argv.slice(2));
