// Color math: headroom thresholds and per-project colors.

import { createHash } from 'node:crypto';

export const PROJECT_SLOTS = 12;
const PROJECT_LIGHTNESS = 0.8;
const PROJECT_MAX_CHROMA = 0.14;

/** 'green' | 'yellow' | 'red' for a "% left" value. */
export function headroomLevel(left, { green, yellow }) {
  if (left >= green) return 'green';
  if (left >= yellow) return 'yellow';
  return 'red';
}

/**
 * OKLCH -> sRGB [r, g, b] in 0-255, or null when the color is outside what
 * sRGB can display. OKLCH is perceptually uniform: equal lightness looks
 * equally bright across hues, which HSL does not manage.
 */
export function oklchToRgb(lightness, chroma, hueDegrees) {
  const h = (hueDegrees * Math.PI) / 180;
  const a = chroma * Math.cos(h);
  const b = chroma * Math.sin(h);
  const l = (lightness + 0.3963377774 * a + 0.2158037573 * b) ** 3;
  const m = (lightness - 0.1055613458 * a - 0.0638541728 * b) ** 3;
  const s = (lightness - 0.0894841775 * a - 1.291485548 * b) ** 3;
  const linear = [
    4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
    -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
    -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s,
  ];
  if (linear.some((v) => v < -0.0001 || v > 1.0001)) return null;
  return linear.map((v) => {
    const c = Math.min(1, Math.max(0, v));
    const srgb = c <= 0.0031308 ? 12.92 * c : 1.055 * c ** (1 / 2.4) - 0.055;
    return Math.round(srgb * 255);
  });
}

/** WCAG relative luminance of an sRGB color. */
export function relativeLuminance([r, g, b]) {
  const lin = (v) => {
    const c = v / 255;
    return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b);
}

/** WCAG contrast ratio between two sRGB colors. */
export function contrastRatio(fg, bg) {
  const [hi, lo] = [relativeLuminance(fg), relativeLuminance(bg)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

/** Which of the 12 hue slots a project lands in. MD5 so it is stable across runs. */
export function projectSlot(name, overrides = {}) {
  const key = Object.keys(overrides).find((k) => k.toLowerCase() === name.toLowerCase());
  if (key !== undefined) return ((overrides[key] % PROJECT_SLOTS) + PROJECT_SLOTS) % PROJECT_SLOTS;
  return createHash('md5').update(name.toLowerCase()).digest()[0] % PROJECT_SLOTS;
}

/**
 * The color for a hue slot: evenly spaced hues at a fixed high lightness, so
 * every project reads clearly on a dark terminal. Chroma backs off per hue
 * until the color fits in sRGB (blues and purples cannot be as vivid).
 */
export function slotColor(slot) {
  const hue = 15 + (360 / PROJECT_SLOTS) * slot;
  for (let chroma = PROJECT_MAX_CHROMA; chroma > 0; chroma -= 0.01) {
    const rgb = oklchToRgb(PROJECT_LIGHTNESS, chroma, hue);
    if (rgb) return rgb;
  }
  return oklchToRgb(PROJECT_LIGHTNESS, 0, hue);
}
