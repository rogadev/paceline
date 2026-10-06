// Fails CI on any high or critical npm advisory, except the ones listed in
// ALLOWED, and only where every affected copy is at the location that entry
// names: inside the npm package's own bundle (node_modules/npm/node_modules/...)
// or one exact package path.
//
// Usage: npm audit --json | node .github/scripts/npm-audit.mjs
//
// Why the bundle: semantic-release depends on @semantic-release/npm, which
// depends on npm, which ships its dependencies bundled, so neither `npm audit
// fix` nor `overrides` can replace them; only a new npm release can. paceline
// never loads that plugin (release.config.js lists the plugins it runs), so
// advisories in npm's bundle are installed in CI but never executed.
//
// An exact path is allowed only when no patched version exists and the reason
// explains why no untrusted input reaches the vulnerable code. Remove an entry
// once `npm audit` stops reporting it.

import fs from 'node:fs';

const BLOCKING = new Set(['high', 'critical']);
const BUNDLED_IN_NPM = 'node_modules/npm/node_modules/';

const bundled = (reason) => ({ where: BUNDLED_IN_NPM, reason });

const ALLOWED = new Map([
  ['GHSA-q2hr-2g5m-vwhr', bundled('brace-expansion bundled in npm, reached only through the unused @semantic-release/npm')],
  ['GHSA-qhr7-859c-m2p7', bundled('brace-expansion bundled in npm, reached only through the unused @semantic-release/npm')],
  ['GHSA-6j4f-fj2g-mc7p', bundled('brace-expansion bundled in npm, reached only through the unused @semantic-release/npm')],
  ['GHSA-3wwx-pv8p-q78v', bundled('undici bundled in npm, reached only through the unused @semantic-release/npm')],
  ['GHSA-r53p-7pc4-xj5r', bundled('undici bundled in npm, reached only through the unused @semantic-release/npm')],
  ['GHSA-rfgv-xxqx-mfg5', bundled('undici bundled in npm, reached only through the unused @semantic-release/npm')],
  ['GHSA-ch52-4w7c-c8xp', bundled('http-cache-semantics bundled in npm, reached only through the unused @semantic-release/npm')],
  [
    'GHSA-vfj7-8cjw-p6xm',
    {
      where: 'node_modules/braces',
      reason:
        'braces under semantic-release (via micromatch) only expands the branch patterns in release.config.js, ' +
        'so no untrusted pattern reaches it; no patched version exists yet',
    },
  ],
]);

// A bundle location covers everything under it; an exact path covers only itself.
const covers = (where, node) => (where.endsWith('/') ? node.startsWith(where) : node === where);

const report = JSON.parse(fs.readFileSync(0, 'utf8'));
if (!report.vulnerabilities) {
  console.error('npm audit produced no vulnerability report:', JSON.stringify(report).slice(0, 500));
  process.exit(1);
}

const advisoryId = (via) => via.url?.match(/GHSA-[\w-]+$/)?.[0] ?? `source ${via.source}`;

let failed = false;
for (const [name, vuln] of Object.entries(report.vulnerabilities)) {
  if (!BLOCKING.has(vuln.severity)) continue;
  // Entries that only inherit an advisory from a dependency name it as a string.
  const advisories = vuln.via.filter((via) => typeof via === 'object').map(advisoryId);
  if (advisories.length === 0) continue;
  const unexpected = advisories.filter((id) => {
    const allowed = ALLOWED.get(id);
    return !allowed || !vuln.nodes.every((node) => covers(allowed.where, node));
  });
  if (unexpected.length > 0) {
    failed = true;
    console.error(`${vuln.severity}: ${name} (${unexpected.join(', ')}) in ${vuln.nodes.join(', ')}`);
  } else {
    console.log(`::warning::Allowed ${vuln.severity} advisory in ${name} (${advisories.join(', ')}): ${ALLOWED.get(advisories[0]).reason}`);
  }
}

if (failed) {
  console.error('npm audit found high or critical advisories. Update the dependency, or, if it is unreachable, add it to ALLOWED with the reason.');
  process.exit(1);
}
console.log('npm audit: no blocking advisories.');
