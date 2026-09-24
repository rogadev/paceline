import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { stripAnsi } from '../src/ansi.js';
import { mergeConfig } from '../src/config.js';
import { render } from '../src/render.js';
import { localDate, localEpoch, memoryContext } from './helpers.js';

const DOT = ' \u00b7 ';

function payload(extra = {}) {
  return {
    model: { display_name: 'Opus 5.5 (1M context)' },
    workspace: { current_dir: '/home/u/techcentral', project_dir: '/home/u/techcentral' },
    effort: { level: 'medium' },
    ...extra,
  };
}

describe('render', () => {
  it('renders the full line in order', () => {
    const ctx = memoryContext({ gitBranch: () => 'dev' });
    const line = render(
      payload({
        rate_limits: {
          five_hour: { used_percentage: 16, resets_at: localEpoch(2026, 9, 24, 13) },
          seven_day: { used_percentage: 4, resets_at: localEpoch(2026, 9, 27, 9) },
        },
        cost: { total_duration_ms: 16 * 60_000 },
      }),
      ctx,
    );
    assert.equal(
      line,
      [
        'Opus 5.5',
        'techcentral (dev)',
        '84% session',
        '96% week',
        "\u25b2 100% left of today's 28% budget",
        '16m',
      ].join(DOT),
    );
  });

  it('counts today down across renders and flips to % used when over', () => {
    const ctx = memoryContext();
    const at = (used) =>
      render({ rate_limits: { seven_day: { used_percentage: used, resets_at: localEpoch(2026, 10, 1) } } }, ctx);
    assert.match(at(0), /100% left of today's 14% budget/);
    assert.match(at(10), /30% left of today's 14% budget/);
    assert.match(at(20), /140% used of today's 14% budget/);
  });

  it('shows the last-day state with the reset time', () => {
    const line = render(
      { rate_limits: { seven_day: { used_percentage: 92, resets_at: localEpoch(2026, 9, 24, 21) } } },
      memoryContext(),
    );
    assert.equal(line, '8% week' + DOT + '\u23f3 last day, resets 9pm');
  });

  it('adds the session reset time only once session headroom leaves green', () => {
    const at = (used) =>
      render(
        { rate_limits: { five_hour: { used_percentage: used, resets_at: localEpoch(2026, 9, 24, 12, 52) } } },
        memoryContext(),
      );
    assert.equal(at(50), '50% session');
    assert.equal(at(82), '18% session (resets 12:52pm)');
  });

  it('shows effort only above everyday levels, and fast mode only when on', () => {
    const ctx = memoryContext();
    assert.ok(!render(payload(), ctx).includes('effort'));
    const line = render(payload({ effort: { level: 'high' }, fast_mode: true }), ctx);
    assert.match(line, /high effort/);
    assert.match(line, /fast mode/);
  });

  it('warns on context use and a cold cache', () => {
    const ctx = memoryContext();
    assert.match(render({ context_window: { used_percentage: 72 } }, ctx), /^ctx 72%$/);
    assert.equal(render({ context_window: { used_percentage: 50 } }, ctx), '');
    const cold = { prompt_cache: { expires_at: localEpoch(2026, 9, 24, 9), recache_tokens_if_cold: 82336 } };
    assert.equal(render(cold, ctx), 'cache cold: 82k @ ~2x');
    const warm = { prompt_cache: { expires_at: localEpoch(2026, 9, 24, 11) } };
    assert.equal(render(warm, ctx), '');
  });

  it('colors a project by its root, so subfolders keep the project color', () => {
    const ctx = memoryContext({ env: {} });
    const root = render({ workspace: { current_dir: '/r/techcentral', project_dir: '/r/techcentral' } }, ctx);
    const sub = render({ workspace: { current_dir: '/r/techcentral/src', project_dir: '/r/techcentral' } }, ctx);
    const color = (s) => /38;2;\d+;\d+;\d+/.exec(s)[0];
    assert.equal(color(root), color(sub));
    assert.equal(stripAnsi(sub), 'src');
  });

  it('handles Windows paths', () => {
    const line = render({ workspace: { current_dir: 'C:\\Users\\me\\tctools\\' } }, memoryContext());
    assert.equal(line, 'tctools');
  });

  it('respects segment toggles', () => {
    const config = mergeConfig({ segments: { model: false, project: false } });
    assert.equal(render(payload(), memoryContext({ config })), '');
  });

  it('emits no ANSI codes under NO_COLOR', () => {
    const line = render(payload({ rate_limits: { five_hour: { used_percentage: 90 } } }), memoryContext());
    assert.ok(!line.includes('\x1b'));
  });

  it('survives an empty or malformed payload', () => {
    const ctx = memoryContext();
    for (const p of [null, {}, [], 'x', { rate_limits: { five_hour: { used_percentage: '90' } } }]) {
      assert.doesNotThrow(() => render(p, ctx));
    }
  });

  it('keeps rendering when reading the branch throws', () => {
    const ctx = memoryContext({
      gitBranch: () => {
        throw new Error('EACCES');
      },
    });
    assert.equal(render({ workspace: { current_dir: '/r/app' } }, ctx), 'app');
  });

  it('uses the snapshot only through the provided store', () => {
    const writes = [];
    const ctx = memoryContext({ now: localDate(2026, 9, 24, 10), writeSnapshot: (s) => writes.push(s) });
    render({ rate_limits: { seven_day: { used_percentage: 5, resets_at: localEpoch(2026, 9, 28) } } }, ctx);
    assert.equal(writes.length, 1);
  });
});
