#!/usr/bin/env node
// Renders the status line from sample payloads for /deep-review's review-output
// lane: builds the working tree (and, with --base, the base ref from git
// archive), pipes each payload through both binaries, and writes summary.md with
// the colored line (escapes made visible), the NO_COLOR line, and whether the
// base rendered it differently. Every run uses a scratch CLAUDE_CONFIG_DIR, so
// the user's real ~/.claude is never read or written.
//
// Usage (run from the repo root):
//   node render.mjs --out <dir> [--base <ref>]

import { execFileSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, writeFileSync, existsSync } from 'node:fs';
import { join } from 'node:path';

const ESC = String.fromCharCode(27);
const NL = String.fromCharCode(10);
const args = {};
for (let i = 2; i < process.argv.length; i++) if (process.argv[i].startsWith('--')) args[process.argv[i].slice(2)] = process.argv[++i];
if (!args.out) {
	process.stderr.write('render: missing --out <dir>' + NL);
	process.exit(2);
}
mkdirSync(args.out, { recursive: true });
const exe = process.platform === 'win32' ? '.exe' : '';

function build(srcDir, name) {
	const bin = join(args.out, name + exe);
	execFileSync('go', ['build', '-o', bin, './cmd/paceline'], { cwd: srcDir, stdio: ['ignore', 'pipe', 'pipe'] });
	return bin;
}

function buildBase(ref) {
	const dir = join(args.out, 'base-src');
	mkdirSync(dir, { recursive: true });
	const tar = execFileSync('git', ['archive', '--format=tar', ref], { maxBuffer: 256 * 1024 * 1024 });
	// cwd rather than -C: GNU tar on Windows reads "C:" in a path as a remote host.
	execFileSync('tar', ['-x'], { cwd: dir, input: tar });
	if (!existsSync(join(dir, 'go.mod'))) return { skip: `${ref} has no go.mod (not the Go version)` };
	return { bin: build(dir, 'paceline-base') };
}

const now = Math.floor(Date.now() / 1000);
const H = 3600;
const D = 24 * H;
const repoName = execFileSync('git', ['rev-parse', '--show-toplevel'], { encoding: 'utf8' }).trim();
const limits = (sessionUsed, sessionReset, weekUsed, weekReset) => ({
	five_hour: { used_percentage: sessionUsed, resets_at: now + sessionReset },
	seven_day: { used_percentage: weekUsed, resets_at: now + weekReset },
});

// Each case is one or more payloads rendered in order against one config dir,
// so a later payload sees the pace snapshot the earlier one wrote.
const CASES = [
	{
		name: 'typical',
		note: 'healthy session and week, effort high',
		payloads: [{ model: { display_name: 'Opus 5.5 (1M context)' }, effort: { level: 'high' }, workspace: { current_dir: repoName, project_dir: repoName }, rate_limits: limits(16, 3 * H, 4, 3 * D), context_window: { used_percentage: 30 }, cost: { total_duration_ms: 16 * 60 * 1000 } }],
	},
	{
		name: 'pressure',
		note: 'low session headroom (reset time shown), heavy week, context warning, fast mode, cold cache',
		payloads: [{ model: { display_name: 'Sonnet 5' }, fast_mode: true, workspace: { current_dir: repoName }, rate_limits: limits(85, 2 * H, 70, 2 * D), context_window: { used_percentage: 90 }, prompt_cache: { expires_at: now - 60, recache_tokens_if_cold: 82000 }, cost: { total_duration_ms: 95 * 60 * 1000 } }],
	},
	{
		name: 'over-budget',
		note: 'second refresh of the day after spending past today\'s budget',
		payloads: [
			{ model: { display_name: 'Opus 5.5' }, workspace: { current_dir: repoName }, rate_limits: limits(10, 4 * H, 40, 4 * D) },
			{ model: { display_name: 'Opus 5.5' }, workspace: { current_dir: repoName }, rate_limits: limits(60, 4 * H, 60, 4 * D) },
		],
	},
	{
		name: 'last-day',
		note: 'weekly reset in 10 hours',
		payloads: [{ model: { display_name: 'Opus 5.5' }, workspace: { current_dir: repoName }, rate_limits: limits(20, 4 * H, 50, 10 * H) }],
	},
	{
		name: 'long-names',
		note: 'long model and project names (truncation)',
		payloads: [{ model: { display_name: 'A very long experimental model display name that keeps going' }, workspace: { current_dir: '/home/user/projects/an-extremely-long-project-folder-name-for-width-testing' }, rate_limits: limits(5, 4 * H, 5, 6 * D) }],
	},
	{
		name: 'hostile',
		note: 'escape sequences, bidi override, and zero-width space in the model and folder names',
		payloads: [{ model: { display_name: 'Opus' + ESC + ']0;pwned' + String.fromCharCode(7) + String.fromCharCode(0x202e) + 'evil' }, workspace: { current_dir: '/tmp/repo' + ESC + '[2J' + String.fromCharCode(0x200b) + 'name' } }],
	},
	{
		name: 'wrong-types',
		note: 'numbers as strings and a null limit',
		payloads: [{ model: { display_name: 42 }, rate_limits: { five_hour: { used_percentage: '90', resets_at: 'soon' }, seven_day: null } }],
	},
	{ name: 'empty', note: 'empty object', payloads: [{}] },
	{ name: 'invalid-json', note: 'not JSON at all', payloads: ['this is not json'] },
];

function render(bin, input, configDir, noColor) {
	const env = { ...process.env, CLAUDE_CONFIG_DIR: configDir };
	delete env.NO_COLOR;
	if (noColor) env.NO_COLOR = '1';
	try {
		const out = execFileSync(bin, [], { input, env, encoding: 'utf8', timeout: 10000, stdio: ['pipe', 'pipe', 'pipe'] });
		return { out, code: 0 };
	} catch (e) {
		return { out: e.stdout || '', code: e.status ?? -1, err: (e.stderr || String(e)).slice(0, 300) };
	}
}

// Makes escapes and invisible characters visible without changing the rest.
function visible(s) {
	let r = '';
	for (const ch of s) {
		const c = ch.codePointAt(0);
		if (c === 27) r += '<ESC>';
		else if (c < 32 || c === 127 || (c >= 0x80 && c < 0xa0)) r += `<0x${c.toString(16)}>`;
		else if ((c >= 0x200b && c <= 0x200f) || (c >= 0x202a && c <= 0x202e) || (c >= 0x2066 && c <= 0x2069) || c === 0xfeff) r += `<U+${c.toString(16).toUpperCase()}>`;
		else r += ch;
	}
	return r;
}

function runAll(bin, tag) {
	const results = {};
	for (const c of CASES) {
		const color = mkdtempSync(join(args.out, `cfg-${tag}-${c.name}-`));
		const plain = mkdtempSync(join(args.out, `cfg-${tag}-${c.name}-nc-`));
		const steps = c.payloads.map((p) => {
			const input = typeof p === 'string' ? p : JSON.stringify(p);
			return { colored: render(bin, input, color, false), plain: render(bin, input, plain, true) };
		});
		results[c.name] = steps;
	}
	return results;
}

const lines = ['# Status line renders', ''];
let head;
try {
	head = runAll(build('.', 'paceline-head'), 'head');
} catch (e) {
	lines.push(`Build of the working tree failed: ${String(e.stderr || e).slice(0, 500)}`);
	writeFileSync(join(args.out, 'summary.md'), lines.join(NL) + NL);
	process.stdout.write(lines.join(NL) + NL);
	process.exit(0);
}
let base = null;
let baseNote = 'no base requested';
if (args.base) {
	try {
		const b = buildBase(args.base);
		if (b.skip) baseNote = `base not rendered: ${b.skip}`;
		else {
			base = runAll(b.bin, 'base');
			baseNote = `base: ${args.base}`;
		}
	} catch (e) {
		baseNote = `base not rendered: ${String(e.stderr || e).slice(0, 200)}`;
	}
}
lines.push(`Built from the working tree. ${baseNote}. Rendered at ${new Date().toISOString()} in the local time zone; times in the output are relative to now. ESC is shown as <ESC>; invisible characters as <U+XXXX>.`);
lines.push('');
let changed = 0;
for (const c of CASES) {
	lines.push(`## ${c.name}: ${c.note}`);
	head[c.name].forEach((step, i) => {
		const tag = c.payloads.length > 1 ? ` (refresh ${i + 1})` : '';
		lines.push('```');
		lines.push(`colored${tag}: ${visible(step.colored.out)}`);
		lines.push(`NO_COLOR${tag}: ${visible(step.plain.out)}`);
		if (step.colored.code !== 0) lines.push(`exit ${step.colored.code}: ${visible(step.colored.err || '')}`);
		if (base) {
			const b = base[c.name][i];
			if (b.plain.out !== step.plain.out || b.colored.out !== step.colored.out) {
				changed++;
				lines.push(`base colored${tag}: ${visible(b.colored.out)}`);
				lines.push(`base NO_COLOR${tag}: ${visible(b.plain.out)}`);
			} else lines.push('base: identical');
		}
		lines.push('```');
	});
	lines.push('');
}
if (base) lines.push(`${changed} render(s) differ from the base.`);
writeFileSync(join(args.out, 'summary.md'), lines.join(NL) + NL);
process.stdout.write(lines.join(NL) + NL);
