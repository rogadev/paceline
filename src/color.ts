/** Headroom thresholds and per-project colors. */

import { md5 } from './md5.ts'

/** How many project colors there are. */
export const SLOTS = 12

const PROJECT_LIGHTNESS = 0.8
const PROJECT_MAX_CHROMA = 0.14

/** An sRGB color with 0-255 channels. */
export type RGB = readonly [number, number, number]

/** A headroom color band, from most to least headroom. */
export type Level = 'green' | 'yellow' | 'red'

/** Buckets a "% left" value using the green and yellow thresholds. */
export function headroomLevel(left: number, green: number, yellow: number): Level {
  if (left >= green) return 'green'
  if (left >= yellow) return 'yellow'
  return 'red'
}

const cube = (x: number) => x * x * x

/**
 * Converts an OKLCH color to sRGB, or returns null when sRGB cannot show it.
 * OKLCH is perceptually uniform: equal lightness looks equally bright across
 * hues, which HSL does not manage.
 */
export function oklchToRGB(lightness: number, chroma: number, hueDegrees: number): RGB | null {
  const h = (hueDegrees * Math.PI) / 180
  const a = chroma * Math.cos(h)
  const b = chroma * Math.sin(h)
  const l = cube(lightness + 0.3963377774 * a + 0.2158037573 * b)
  const m = cube(lightness - 0.1055613458 * a - 0.0638541728 * b)
  const s = cube(lightness - 0.0894841775 * a - 1.291485548 * b)
  const linear = [
    4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
    -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
    -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s,
  ]
  const rgb: number[] = []
  for (const v of linear) {
    if (v < -0.0001 || v > 1.0001) return null
    let c = Math.min(1, Math.max(0, v))
    c = c <= 0.0031308 ? c * 12.92 : 1.055 * Math.pow(c, 1 / 2.4) - 0.055
    rgb.push(Math.round(c * 255))
  }
  return [rgb[0], rgb[1], rgb[2]]
}

/**
 * Picks a project's color slot: an override when one matches the name
 * ignoring case, otherwise an MD5 hash of the lowercased name, so the slot is
 * the same on every run and machine.
 */
export function projectSlot(name: string, overrides: Readonly<Record<string, number>>): number {
  const lower = name.toLowerCase()
  for (const [key, slot] of Object.entries(overrides)) {
    if (key.toLowerCase() === lower) return ((slot % SLOTS) + SLOTS) % SLOTS
  }
  return md5(new TextEncoder().encode(lower))[0] % SLOTS
}

/**
 * Gets the color for a slot: evenly spaced hues at a fixed high lightness, so
 * every project reads clearly on a dark background. Chroma backs off per hue
 * until the color fits in sRGB, since blues and purples cannot be as vivid as
 * other hues at this lightness.
 */
export function slotColor(slot: number): RGB {
  const hue = 15 + (360 / SLOTS) * slot
  for (let chroma = PROJECT_MAX_CHROMA; chroma > 0; chroma -= 0.01) {
    const rgb = oklchToRGB(PROJECT_LIGHTNESS, chroma, hue)
    if (rgb !== null) return rgb
  }
  return oklchToRGB(PROJECT_LIGHTNESS, 0, hue) ?? [204, 204, 204]
}
