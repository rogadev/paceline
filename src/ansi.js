// ANSI escape helpers. NO_COLOR (https://no-color.org) turns every style off.

const ESC = '\x1b[';

export function createStyle(env = process.env) {
  const enabled = !('NO_COLOR' in env);
  const wrap = (code) => (text) => (enabled ? `${ESC}${code}m${text}${ESC}0m` : text);
  return {
    enabled,
    red: wrap('31'),
    green: wrap('32'),
    yellow: wrap('33'),
    cyan: wrap('36'),
    dim: wrap('2'),
    rgb: (r, g, b) => wrap(`38;2;${r};${g};${b}`),
  };
}

export function stripAnsi(text) {
  // biome-ignore lint/suspicious/noControlCharactersInRegex: matching ESC is the point
  return text.replace(/\x1b\[[0-9;]*m/g, '');
}
