/**
 * Formats reset times and durations, and finds day boundaries, in one time
 * zone: the machine's own in a session, UTC in the parity tests.
 */

/** Which clock calendar math reads: the machine's local time or UTC. */
export type Zone = 'local' | 'utc'

const WEEKDAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']

type Parts = {
  year: number
  month: number
  day: number
  hour: number
  minute: number
  second: number
  weekday: number
}

function parts(ms: number, zone: Zone): Parts {
  const d = new Date(ms)
  return zone === 'utc'
    ? {
        year: d.getUTCFullYear(),
        month: d.getUTCMonth(),
        day: d.getUTCDate(),
        hour: d.getUTCHours(),
        minute: d.getUTCMinutes(),
        second: d.getUTCSeconds(),
        weekday: d.getUTCDay(),
      }
    : {
        year: d.getFullYear(),
        month: d.getMonth(),
        day: d.getDate(),
        hour: d.getHours(),
        minute: d.getMinutes(),
        second: d.getSeconds(),
        weekday: d.getDay(),
      }
}

const pad = (n: number, width: number) => String(n).padStart(width, '0')

/** Gets the midnight at the start of the day that holds `ms`, in epoch milliseconds. */
export function startOfDay(ms: number, zone: Zone): number {
  const p = parts(ms, zone)
  return zone === 'utc'
    ? Date.UTC(p.year, p.month, p.day)
    : new Date(p.year, p.month, p.day).getTime()
}

/**
 * Gets the next midnight after the day that holds `ms`. It steps a calendar
 * day, not 24 hours, so a 23- or 25-hour day at a daylight saving change is
 * still one day.
 */
export function startOfNextDay(ms: number, zone: Zone): number {
  const p = parts(ms, zone)
  return zone === 'utc'
    ? Date.UTC(p.year, p.month, p.day + 1)
    : new Date(p.year, p.month, p.day + 1).getTime()
}

/**
 * Rounds `ms` down to a whole number of `stepMs` steps counted from
 * `dayStartMs`, a midnight from `startOfDay`. Counting from the day's own
 * midnight rather than the epoch keeps a zone with a half-hour offset on its
 * own hours.
 */
export function floorInDay(ms: number, stepMs: number, dayStartMs: number): number {
  return dayStartMs + Math.floor((ms - dayStartMs) / stepMs) * stepMs
}

/** Formats the day that holds `ms` as `yyyymmdd`. */
export function dateKey(ms: number, zone: Zone): string {
  const p = parts(ms, zone)
  return pad(p.year, 4) + pad(p.month + 1, 2) + pad(p.day, 2)
}

/**
 * Formats epoch seconds as an RFC 3339 timestamp to the whole second, as Go's
 * `time.RFC3339` layout does: `2026-10-08T21:00:00Z` in UTC, or the local
 * time with its offset, such as `2026-10-08T14:00:00-07:00`. The offset is the
 * one in effect at that instant, so a time across a daylight saving change
 * carries that day's offset, not today's.
 */
export function rfc3339(epochSeconds: number, zone: Zone): string {
  const ms = Math.trunc(epochSeconds) * 1000
  const p = parts(ms, zone)
  const date = `${pad(p.year, 4)}-${pad(p.month + 1, 2)}-${pad(p.day, 2)}`
  const time = `${pad(p.hour, 2)}:${pad(p.minute, 2)}:${pad(p.second, 2)}`
  return `${date}T${time}${utcOffset(ms, zone)}`
}

// The zone's offset from UTC at `ms` as RFC 3339 writes it: `Z` for none,
// otherwise `+hh:mm` or `-hh:mm`.
function utcOffset(ms: number, zone: Zone): string {
  const minutes = zone === 'utc' ? 0 : -new Date(ms).getTimezoneOffset()
  if (minutes === 0) return 'Z'
  const sign = minutes < 0 ? '-' : '+'
  const abs = Math.abs(minutes)
  return `${sign}${pad(Math.trunc(abs / 60), 2)}:${pad(abs % 60, 2)}`
}

/**
 * Formats epoch seconds as `9pm` or `3:40pm`, with the weekday in front
 * (`Thu 9pm`) when the time falls on a different day than `nowMs`.
 */
export function clock(epochSeconds: number, nowMs: number, zone: Zone): string {
  const ms = Math.trunc(epochSeconds) * 1000
  const p = parts(ms, zone)
  const hour = p.hour % 12 === 0 ? 12 : p.hour % 12
  const minute = p.minute === 0 ? '' : ':' + String(p.minute).padStart(2, '0')
  const text = `${hour}${minute}${p.hour < 12 ? 'am' : 'pm'}`
  return dateKey(ms, zone) === dateKey(nowMs, zone) ? text : `${WEEKDAYS[p.weekday]} ${text}`
}

/**
 * Gets the wall-clock minute of the day that `ms` falls on, 0 to 1439: 17:00
 * is 1020. It reads the clock, not the time since midnight, so a daylight
 * saving day still puts 5 p.m. at 1020.
 */
export function minuteOfDay(ms: number, zone: Zone): number {
  const p = parts(ms, zone)
  return p.hour * 60 + p.minute
}

/** Gets the short weekday name (`Tue`) of the day that holds `ms`. */
export function weekday(ms: number, zone: Zone): string {
  return WEEKDAYS[parts(ms, zone).weekday]
}

/** Formats milliseconds as `1h5m`, `12m`, or `40s`. */
export function duration(ms: number): string {
  const sec = Math.trunc(ms / 1000)
  const h = Math.trunc(sec / 3600)
  const m = Math.trunc((sec % 3600) / 60)
  if (h > 0) return `${h}h${m}m`
  if (m > 0) return `${m}m`
  return `${sec}s`
}
