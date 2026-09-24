// Local-time formatting for reset times.

/** Local midnight at the start of the day containing `date`. */
export function startOfLocalDay(date) {
  const d = new Date(date);
  d.setHours(0, 0, 0, 0);
  return d;
}

/** yyyymmdd for the local day containing `date`. */
export function localDateKey(date) {
  const y = date.getFullYear();
  const m = String(date.getMonth() + 1).padStart(2, '0');
  const d = String(date.getDate()).padStart(2, '0');
  return `${y}${m}${d}`;
}

/**
 * Epoch seconds -> "9pm" / "3:40pm", prefixed with the weekday ("Thu 9pm")
 * when the time is not on the same local day as `now`.
 */
export function formatClock(epochSeconds, now) {
  const t = new Date(epochSeconds * 1000);
  const hour12 = t.getHours() % 12 || 12;
  const suffix = t.getHours() < 12 ? 'am' : 'pm';
  const minutes = t.getMinutes();
  let text = minutes === 0 ? `${hour12}${suffix}` : `${hour12}:${String(minutes).padStart(2, '0')}${suffix}`;
  if (localDateKey(t) !== localDateKey(now)) {
    text = `${t.toLocaleDateString('en-US', { weekday: 'short' })} ${text}`;
  }
  return text;
}

/** Milliseconds -> "1h5m" / "12m" / "40s". */
export function formatDuration(ms) {
  const sec = Math.floor(ms / 1000);
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  if (h > 0) return `${h}h${m}m`;
  if (m > 0) return `${m}m`;
  return `${sec}s`;
}
