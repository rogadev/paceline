// Untrusted text guard. Everything paceline prints that it did not write
// itself (model names, folder names, git branch names, effort levels) passes
// through here first. A folder or branch name is attacker-controlled when you
// clone a repo, and raw control characters in terminal output can move the
// cursor, rewrite the screen, set the window title, or emit OSC 8 links.

// Code point ranges to strip:
// - C0 controls (includes ESC and BEL) and DEL
// - C1 controls, which some terminals treat as 8-bit escape introducers
// - zero-width characters and bidi overrides, so a name cannot hide text or
//   visually reorder what follows it
const UNSAFE_RANGES = [
  [0x00, 0x1f],
  [0x7f, 0x9f],
  [0x200b, 0x200f],
  [0x202a, 0x202e],
  [0x2066, 0x2069],
  [0xfeff, 0xfeff],
];

const hex = (n) => `\\u${n.toString(16).padStart(4, '0')}`;
const UNSAFE = new RegExp(`[${UNSAFE_RANGES.map(([a, b]) => `${hex(a)}-${hex(b)}`).join('')}]`, 'g');

/** Strip control and bidi characters and cap the length. Non-strings become ''. */
export function safeText(value, maxLength = 64) {
  if (typeof value !== 'string') return '';
  const clean = value.replace(UNSAFE, '');
  const chars = Array.from(clean);
  return chars.length > maxLength ? `${chars.slice(0, maxLength - 1).join('')}…` : clean;
}

/** A finite number, or null. Guards arithmetic against strings and NaN from the payload. */
export function safeNumber(value) {
  return typeof value === 'number' && Number.isFinite(value) ? value : null;
}
