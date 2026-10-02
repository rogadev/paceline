// Fails CI on any high or critical npm advisory, except the ones listed in
// ALLOWED, and only where every affected copy is inside the npm package's own
// bundle (node_modules/npm/node_modules/...).
//
// Usage: npm audit --json | node .github/scripts/npm-audit.mjs
//
// Why the bundle: semantic-release depends on @semantic-release/npm, which
// depends on npm, which ships its dependencies bundled, so neither `npm audit
// fix` nor `overrides` can replace them; only a new npm release can. paceline
// never loads that plugin (release.config.js lists the plugins it runs), so
// advisories in npm's bundle are installed in CI but never executed. Remove an
// entry once `npm audit` stops reporting it.

import fs from 'node:fs';

const BLOCKING = new Set(['high', 'critical']);
const BUNDLED_IN_NPM = 'node_modules/npm/node_modules/';

const ALLOWED = new Map([
  ['GHSA-q2hr-2g5m-vwhr', 'brace-expansion bundled in npm, reached only through the unused @semantic-release/npm'],
  ['GHSA-qhr7-859c-m2p7', 'brace-expansion bundled in npm, reached only through the unused @semantic-release/npm'],
  ['GHSA-6j4f-fj2g-mc7p', 'brace-expansion bundled in npm, reached only through the unused @semantic-release/npm'],
  ['GHSA-3wwx-pv8p-q78v', 'undici bundled in npm, reached only through the unused @semantic-release/npm'],
  ['GHSA-r53p-7pc4-xj5r', 'undici bundled in npm, reached only through the unused @semantic-release/npm'],
  ['GHSA-rfgv-xxqx-mfg5', 'undici bundled in npm, reached only through the unused @semantic-release/npm'],
]);

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
  const bundled = vuln.nodes.every((node) => node.startsWith(BUNDLED_IN_NPM));
  const unexpected = advisories.filter((id) => !bundled || !ALLOWED.has(id));
  if (unexpected.length > 0) {
    failed = true;
    console.error(`${vuln.severity}: ${name} (${unexpected.join(', ')}) in ${vuln.nodes.join(', ')}`);
  } else {
    console.log(`::warning::Allowed ${vuln.severity} advisory in ${name} (${advisories.join(', ')}): ${ALLOWED.get(advisories[0])}`);
  }
}

if (failed) {
  console.error('npm audit found high or critical advisories. Update the dependency, or, if it is unreachable, add it to ALLOWED with the reason.');
  process.exit(1);
}
console.log('npm audit: no blocking advisories.');
