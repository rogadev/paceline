#!/usr/bin/env node
// Builds the review handoff for /deep-review in paceline: the diff, the
// changed-file list, a per-file surface classification, a size tier, suggested
// lanes, whether the status line needs rendering, and intent sources. Read-only against the repo; writes only to --out.
//
// Usage (run from the repo root):
//   node change-map.mjs --out <dir> [--base <ref> | --range <a..b> | --pr <n> | --working]
//
// Writes to <dir>: diff.patch, files.txt, stat.txt, change-map.json, change-map.md

import { execFileSync } from 'node:child_process';
import { existsSync, mkdirSync, readFileSync, writeFileSync, statSync } from 'node:fs';
import { basename, dirname, join } from 'node:path';

const args = parseArgs(process.argv.slice(2));
let root, app, branch, target, files;

// ---------------------------------------------------------------------------

function main() {
	if (!args.out) die('missing --out <dir>');
	mkdirSync(args.out, { recursive: true });

	root = git(['rev-parse', '--show-toplevel']).trim();
	process.chdir(root);

	app = detectApp();
	branch = git(['rev-parse', '--abbrev-ref', 'HEAD']).trim();
	target = resolveTarget();
	const patch = target.patch;
	writeFileSync(join(args.out, 'diff.patch'), patch);

	files = parsePatch(patch);
	for (const f of files) classify(f);
	const counted = files.filter((f) => !f.ignoredForSize);
	const size = {
		files: files.length,
		countedFiles: counted.length,
		added: counted.reduce((n, f) => n + f.added, 0),
		removed: counted.reduce((n, f) => n + f.removed, 0),
		newFiles: files.filter((f) => f.status === 'added').length,
	};
	size.changed = size.added + size.removed;

	const surfaces = {};
	for (const f of files) for (const s of f.surfaces) (surfaces[s] ||= []).push(f.path);

	const blast = blastRadius(files);
	const tier = suggestTier(size, surfaces, blast);
	const lanes = suggestLanes(surfaces, files, tier);
	const renders = renderTargets(files);
	const intent = gatherIntent();

	writeFileSync(join(args.out, 'files.txt'), files.map((f) => f.path).join('\n') + '\n');
	writeFileSync(join(args.out, 'stat.txt'), statText(files, size));

	const map = { app, branch, target: target.label, base: target.base, size, tier, surfaces, blast, lanes, renders, intent, files };
	writeFileSync(join(args.out, 'change-map.json'), JSON.stringify(map, null, '\t'));
	writeFileSync(join(args.out, 'change-map.md'), summaryMarkdown(map));
	process.stdout.write(summaryMarkdown(map));
}


function parseArgs(argv) {
	const out = {};
	for (let i = 0; i < argv.length; i++) {
		const a = argv[i];
		if (a === '--working') out.working = true;
		else if (a.startsWith('--')) out[a.slice(2)] = argv[++i];
	}
	return out;
}

function die(msg) {
	process.stderr.write(`change-map: ${msg}\n`);
	process.exit(2);
}

function git(argv, opts = {}) {
	try {
		return execFileSync('git', argv, { encoding: 'utf8', maxBuffer: 256 * 1024 * 1024, stdio: ['ignore', 'pipe', 'pipe'], ...opts });
	} catch (e) {
		// `git diff --no-index` exits 1 when the files differ; that output is the point.
		if (e.status === 1 && typeof e.stdout === 'string') return e.stdout;
		if (opts.soft) return '';
		throw e;
	}
}

function refExists(ref) {
	return git(['rev-parse', '--verify', '--quiet', ref], { soft: true }).trim() !== '';
}

function detectApp() {
	return 'paceline';
}

function untrackedPatch() {
	const list = git(['ls-files', '--others', '--exclude-standard']).split('\n').filter(Boolean);
	let out = '';
	for (const f of list) {
		try {
			if (statSync(f).size > 2 * 1024 * 1024) continue;
		} catch {
			continue;
		}
		out += git(['diff', '--no-index', '--', '/dev/null', f], { soft: true });
	}
	return out;
}

function resolveTarget() {
	if (args.pr) {
		const n = String(args.pr).replace(/^#/, '');
		const patch = execFileSync('gh', ['pr', 'diff', n], { encoding: 'utf8', maxBuffer: 256 * 1024 * 1024 });
		let base = 'PR base';
		try {
			base = JSON.parse(execFileSync('gh', ['pr', 'view', n, '--json', 'baseRefName'], { encoding: 'utf8' })).baseRefName;
		} catch {
			// gh pr view failed; keep the generic base label
		}
		return { label: `PR #${n}`, base, patch };
	}
	if (args.range) return { label: args.range, base: args.range.split('..')[0], patch: git(['diff', '-M', args.range]) };
	if (args.base) {
		const mb = git(['merge-base', 'HEAD', args.base], { soft: true }).trim() || args.base;
		return { label: `${branch} + working tree vs ${args.base}`, base: args.base, patch: git(['diff', '-M', mb]) + untrackedPatch() };
	}
	if (args.working) return { label: 'uncommitted changes', base: 'HEAD', patch: git(['diff', '-M', 'HEAD']) + untrackedPatch() };

	// Auto: review what this branch would bring to its integration branch.
	// Feature branches integrate into dev; dev ships to main (see the repo's git flow).
	let baseRef;
	if (branch === 'main' || branch === 'master') baseRef = null;
	else if (branch === 'dev') baseRef = refExists('origin/main') ? 'origin/main' : 'main';
	else baseRef = ['origin/dev', 'dev', 'origin/main', 'main'].find(refExists);

	if (baseRef) {
		const mb = git(['merge-base', 'HEAD', baseRef], { soft: true }).trim();
		if (mb) {
			const patch = git(['diff', '-M', mb]) + untrackedPatch();
			if (patch.trim()) return { label: `${branch} (commits + working tree) vs ${baseRef}`, base: baseRef, patch };
		}
	}
	const working = git(['diff', '-M', 'HEAD']) + untrackedPatch();
	if (working.trim()) return { label: 'uncommitted changes', base: 'HEAD', patch: working };
	const note = baseRef ? `nothing new vs ${baseRef} and no uncommitted changes; reviewing the last commit` : 'last commit';
	return { label: note, base: 'HEAD~1', patch: git(['diff', '-M', 'HEAD~1']) };
}

function parsePatch(text) {
	const out = [];
	let cur = null;
	for (const line of text.split('\n')) {
		if (line.startsWith('diff --git ')) {
			const m = line.match(/^diff --git a\/(.+?) b\/(.+)$/);
			cur = { path: m ? m[2] : line.slice(11), status: 'modified', added: 0, removed: 0, plus: [], minus: [], surfaces: [] };
			out.push(cur);
		} else if (!cur) continue;
		else if (line.startsWith('new file mode') || line.startsWith('--- /dev/null')) cur.status = 'added';
		else if (line.startsWith('deleted file mode') || line.startsWith('+++ /dev/null')) cur.status = 'deleted';
		else if (line.startsWith('rename from ')) cur.status = 'renamed';
		else if (line.startsWith('+++ b/')) cur.path = line.slice(6);
		else if (line.startsWith('+') && !line.startsWith('+++')) {
			cur.added++;
			if (cur.plus.length < 4000) cur.plus.push(line.slice(1));
		} else if (line.startsWith('-') && !line.startsWith('---')) {
			cur.removed++;
			if (cur.minus.length < 4000) cur.minus.push(line.slice(1));
		}
	}
	return out;
}

const RX = {
	lock: /(^|\/)(package-lock\.json|go\.sum)$/,
	generated: /(^|\/)(dist\/|coverage\/|node_modules\/)|coverage\.out$/,
	reviewTooling: /^\.claude\//,
	docs: /\.(md|txt)$/i,
	userDocs: /^(README|SECURITY)\.md$/,
	goTest: /_test\.go$/,
	jsTest: /^test\/.*\.test\.js$/,
	parity: /(^|\/)testdata\/parity\.json$/,
	testdata: /(^|\/)testdata\//,
	goFile: /\.go$/,
	cli: /^cmd\/paceline\//,
	output: /^internal\/(render|color|timefmt)\//,
	timeMath: /^internal\/(pace|timefmt)\//,
	untrusted: /^internal\/(payload|gitinfo|config|sanitize)\//,
	installer: /^internal\/install\//,
	policy: /^internal\/policy\//,
	tools: /^tools\//,
	ci: /^\.github\/workflows\//,
	release: /^(\.goreleaser\.ya?ml|release\.config\.js|commitlint\.config\.js|package\.json|\.github\/dependabot\.yml|\.githooks\/)/,
	goMod: /^go\.mod$/,
	repoConfig: /^(\.golangci\.ya?ml|\.gitattributes|\.gitignore|\.editorconfig)$/,
};

// Paths on the status line's render path: they run on every refresh.
const RENDER_PATH = /^(cmd\/paceline\/main\.go|internal\/(render|pace|color|timefmt|payload|gitinfo|config|sanitize)\/)/;

const FILE_IO = /\bos\.(WriteFile|ReadFile|Open|OpenFile|Create|CreateTemp|Rename|Remove|RemoveAll|MkdirAll|Mkdir|Symlink|Readlink|Lstat|Stat|Chmod|ReadDir)\b|\bio\.ReadAll\b|\bfilepath\.(EvalSymlinks|Walk|WalkDir|Glob)\b/;
const PRINTS = /\bfmt\.(Fprint|Fprintf|Fprintln|Print|Printf|Println|Sprintf)\b|\.WriteString\(|\bstyle\(|\.rgb\(/;
const ENV = /\bos\.(Getenv|LookupEnv|Environ|UserHomeDir|Executable)\b/;
const TIME = /\btime\.(Now|Date|Local|LoadLocation|Unix|Until|Since)\b|\.(Add|AddDate|Sub|Truncate|In)\(/;
const PERF_CONTENT = /\bregexp\.(MustCompile|Compile)\b|\bfor\b|\brange\b|\bio\.ReadAll\b|\bstrings\.Builder\b|\bappend\(|\bsort\.|\bslices\.Sort/;
const IMPORT_LINE = /^\s*(import\s+)?(\w+\s+)?"[a-z0-9_.\/-]+"\s*$/;
const SANITIZE = /\bsanitize\.Text\b/;
const COMMENT_LINE = /^\s*(\/\/|\/\*|\*|#(?!!))/;
const COPY_LINE = /"[A-Za-z][^"]{8,}"/;

function classify(f) {
	const p = f.path;
	const add = (s) => f.surfaces.includes(s) || f.surfaces.push(s);
	const plus = f.plus.join('\n');
	const both = plus + '\n' + f.minus.join('\n');

	if (RX.lock.test(p)) {
		add('deps');
		f.ignoredForSize = true;
		return;
	}
	if (RX.generated.test(p)) {
		add('generated');
		f.ignoredForSize = true;
		return;
	}
	if (RX.reviewTooling.test(p)) {
		add('review-tooling');
		f.ignoredForSize = true;
		return;
	}
	if (RX.parity.test(p)) {
		add('test');
		add('parity-fixture');
		return;
	}
	if (RX.goTest.test(p) || RX.jsTest.test(p) || RX.testdata.test(p)) {
		add('test');
		if (RX.policy.test(p)) add('policy');
		return;
	}
	if (RX.docs.test(p)) {
		add(RX.userDocs.test(p) ? 'user-docs' : 'docs');
		return;
	}
	if (RX.ci.test(p)) add('ci');
	if (RX.release.test(p)) add('release');
	if (/^package\.json$/.test(p) && /"(dependencies|devDependencies)"|^\s*"[@\w/.-]+":\s*"[\^~]?\d/m.test(both)) add('deps');
	if (RX.goMod.test(p)) {
		add('deps');
		add('release');
	}
	if (RX.repoConfig.test(p)) add('repo-config');
	if (RX.tools.test(p)) add('tools');

	if (RX.goFile.test(p) && !RX.tools.test(p)) {
		add('go');
		if (RX.cli.test(p)) add('cli');
		if (RX.output.test(p)) add('output');
		if (RX.timeMath.test(p) || TIME.test(plus)) add('time-math');
		if (RX.untrusted.test(p) || (RX.cli.test(p) && /stdin|Decode/.test(both))) add('untrusted-input');
		if (RX.installer.test(p) || (RX.cli.test(p) && /install|Install/.test(both))) add('installer');
		if (RX.policy.test(p)) add('policy');
		if (RENDER_PATH.test(p)) add('render-path');
		if (FILE_IO.test(plus)) add('file-io');
		if (ENV.test(plus)) add('env');
		if (PRINTS.test(plus) || f.plus.some((l) => COPY_LINE.test(l) && !COMMENT_LINE.test(l))) add('prints');
		if (SANITIZE.test(f.minus.join('\n')) && !SANITIZE.test(plus)) add('sanitize-removed');
		if (f.plus.some((l) => IMPORT_LINE.test(l)) || f.minus.some((l) => IMPORT_LINE.test(l))) add('imports');
		if (PERF_CONTENT.test(plus)) add('perf-shaped');
		if (f.plus.some((l) => COMMENT_LINE.test(l))) add('comments');
	}
	if (f.status === 'added') add('new-file');
	if (f.status === 'renamed' || f.status === 'deleted') add('move-or-delete');
	if (!f.surfaces.length) add('other');
}

// For each changed internal package, how many other packages import it.
function blastRadius(list) {
	const out = [];
	const pkgs = new Set();
	for (const f of list) {
		const m = f.path.match(/^(internal\/[^/]+)\/[^/]+\.go$/);
		if (m && !RX.goTest.test(f.path) && f.status !== 'added') pkgs.add(m[1]);
	}
	for (const pkg of pkgs) {
		const hits = git(['grep', '-l', '-F', `paceline/${pkg}"`, '--', '*.go'], { soft: true })
			.split('\n')
			.filter((x) => x && !x.startsWith(pkg + '/') && !RX.goTest.test(x));
		const importers = [...new Set(hits.map((h) => dirname(h)))];
		out.push({ path: pkg, importers: importers.length, sample: importers.slice(0, 5) });
	}
	return out;
}

function suggestTier(sz, surf, br) {
	const has = (k) => Boolean(surf[k]);
	const code = Object.keys(surf).filter((k) => !['docs', 'user-docs', 'generated', 'review-tooling', 'test', 'parity-fixture', 'comments'].includes(k));
	if (!code.length && !has('parity-fixture')) return { tier: 0, name: 'trivial', why: 'docs, review tooling, or tests only' };
	let t = sz.countedFiles <= 3 && sz.changed <= 60 ? 1 : sz.countedFiles <= 12 && sz.changed <= 400 ? 2 : 3;
	const why = [`${sz.countedFiles} files, ${sz.changed} changed lines`];
	const risky = ['installer', 'untrusted-input', 'file-io', 'deps', 'ci', 'release', 'policy', 'imports', 'sanitize-removed', 'parity-fixture'].filter(has);
	if (risky.length && t < 2) {
		t = 2;
		why.push(`risk surface: ${risky.join(', ')}`);
	}
	const wide = br.filter((b) => b.importers >= 2);
	if (wide.length && t < 2) {
		t = 2;
		why.push(`shared package: ${wide.map((b) => b.path).join(', ')}`);
	}
	if ((sz.newFiles >= 4 || list().length >= 2) && t < 3) {
		t = 3;
		why.push('new feature surface (several new files or a new package)');
	}
	if (sz.countedFiles > 40 || sz.changed > 2500) {
		t = 4;
		why.push('very large change; shard the lanes');
	}
	return { tier: t, name: ['trivial', 'small', 'medium', 'large', 'very large'][t], why: why.join('; ') };
	// A new package: a directory where every changed file is newly added.
	function list() {
		const dirs = new Map();
		for (const f of files.filter((x) => /^(internal|cmd|tools)\/.+\.go$/.test(x.path))) {
			const d = dirname(f.path);
			dirs.set(d, (dirs.get(d) ?? true) && f.status === 'added');
		}
		return [...dirs].filter(([, allNew]) => allNew);
	}
}

function suggestLanes(surf, list, tier) {
	const has = (k) => Boolean(surf[k]);
	const lanes = [];
	const push = (lane, strength, why) => lanes.push({ lane, strength, why });

	if (tier.tier === 0) {
		const out = [{ lane: 'none', strength: 'strong', why: 'no shipped code changed; the orchestrator reads it directly' }];
		if (has('test') || has('parity-fixture')) out.push({ lane: 'review-gate', strength: 'strong', why: 'tests changed' });
		return out;
	}

	push('review-gate', 'strong', 'any code, config, or workflow change');
	const sec = ['installer', 'untrusted-input', 'file-io', 'deps', 'ci', 'release', 'policy', 'imports', 'sanitize-removed', 'env'].filter(has);
	if (sec.length || (has('prints') && has('render-path'))) push('review-security', 'strong', `touches ${sec.length ? sec.join(', ') : 'printed output on the render path'}`);
	if (has('output') || has('parity-fixture') || (has('cli') && has('prints')) || has('user-docs')) push('review-output', has('output') || has('parity-fixture') ? 'strong' : 'medium', 'status line output, CLI messages, or user docs');
	if (has('go')) push('review-logic', 'strong', 'shipped Go code changed');
	if (has('render-path') && has('perf-shaped') && tier.tier >= 2) push('review-perf', 'medium', 'loops, reads, or regexps on the render path');
	if (has('imports') && has('render-path')) push('review-perf', 'medium', 'new imports can grow the binary and startup');
	if (has('go') || has('test')) push('review-tests', has('go') ? 'strong' : 'medium', has('go') ? 'logic changed' : 'tests changed');
	if (tier.tier >= 2 && (has('go') || has('tools'))) push('review-quality', 'medium', 'readability, duplication, Go idiom');
	if ((has('new-file') && tier.tier >= 2) || has('move-or-delete') || has('policy') || (has('imports') && tier.tier >= 2) || tier.tier >= 3) push('review-architecture', tier.tier >= 3 ? 'strong' : 'medium', 'new files, moves, imports, or a large change');
	if (has('ci') || has('release') || has('deps') || has('tools')) push('review-release', 'strong', 'CI, release, or tooling changed');
	else if (target.base && /main$/.test(target.base)) push('review-release', 'medium', 'merging into main releases; commit types decide the version');
	if (tier.tier >= 2) push('review-intent', 'medium', 'check the change against its issue, PR, or commits');

	const seen = new Map();
	for (const l of lanes) {
		const prev = seen.get(l.lane);
		if (!prev || (prev.strength !== 'strong' && l.strength === 'strong')) seen.set(l.lane, l);
	}
	return [...seen.values()];
}

// Whether the change can alter the rendered status line, so Phase 3 renders it.
function renderTargets(list) {
	return list.some((f) => RENDER_PATH.test(f.path) && !RX.goTest.test(f.path)) || list.some((f) => RX.parity.test(f.path)) ? ['status line'] : [];
}

function gatherIntent() {
	const out = { branch, issueNumbers: [], commits: [], docsTouched: [] };
	const range = target.base && target.base !== 'HEAD' && target.base !== 'PR base' ? `${git(['merge-base', 'HEAD', target.base], { soft: true }).trim()}..HEAD` : null;
	if (range && !range.startsWith('..')) out.commits = git(['log', '--format=%s', range], { soft: true }).split('\n').filter(Boolean).slice(0, 40);
	const text = [branch, ...out.commits].join('\n');
	out.issueNumbers = [...new Set([...text.matchAll(/(?:#|\bissue[-_/]?|\bgh-)(\d{1,5})\b/gi)].map((m) => m[1]))].slice(0, 5);
	out.docsTouched = files.filter((f) => /\.md$/.test(f.path) && !RX.reviewTooling.test(f.path)).map((f) => f.path);
	return out;
}

function statText(list, sz) {
	const rows = list.map((f) => `${String(f.added).padStart(5)} +${String(f.removed).padStart(5)} -  ${f.status.padEnd(8)} ${f.path}`);
	return rows.join('\n') + `\n${sz.files} files (${sz.countedFiles} counted), +${sz.added} -${sz.removed}\n`;
}

function summaryMarkdown(m) {
	const lines = [];
	lines.push(`# Change map`);
	lines.push(`App: ${m.app} | Branch: ${m.branch} | Target: ${m.target}`);
	lines.push(`Size: ${m.size.countedFiles} files counted (${m.size.files} total), +${m.size.added} -${m.size.removed}, ${m.size.newFiles} new`);
	lines.push(`Suggested tier: ${m.tier.tier} (${m.tier.name}) - ${m.tier.why}`);
	lines.push('');
	lines.push('## Surfaces');
	for (const [k, v] of Object.entries(m.surfaces).sort()) lines.push(`- ${k} (${v.length}): ${v.slice(0, 6).join(', ')}${v.length > 6 ? ` +${v.length - 6} more` : ''}`);
	if (m.blast.length) {
		lines.push('');
		lines.push('## Blast radius (changed internal packages)');
		for (const b of m.blast) lines.push(`- ${b.path}: imported by ${b.importers} other package(s)${b.sample.length ? ` (${b.sample.join(', ')})` : ''}`);
	}
	lines.push('');
	lines.push('## Suggested lanes');
	for (const l of m.lanes) lines.push(`- ${l.lane} [${l.strength}] - ${l.why}`);
	lines.push('');
	lines.push(`## Render: ${m.renders.length ? 'yes, the change can alter the status line' : 'no, the status line output cannot change'}`);
	lines.push('');
	lines.push('## Intent sources');
	lines.push(`- Issues referenced: ${m.intent.issueNumbers.join(', ') || 'none'}`);
	lines.push(`- Docs touched: ${m.intent.docsTouched.join(', ') || 'none'}`);
	lines.push(`- Commits (${m.intent.commits.length}): ${m.intent.commits.slice(0, 8).join(' | ') || 'none'}`);
	return lines.join('\n') + '\n';
}

main();
