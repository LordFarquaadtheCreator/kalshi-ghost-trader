/** @param {string} t */
export function fmtTicker(t) {
  if (!t) return '';
  const parts = t.split('-');
  if (parts.length <= 1) return t;
  return parts.slice(1).join(' vs ');
}

/** @param {string} t */
export function seriesFromTicker(t) {
  if (!t) return '';
  const idx = t.indexOf('-');
  return idx > 0 ? t.substring(0, idx) : '';
}

/** @param {number} ts */
export function fmtTime(ts) {
  return new Date(ts).toLocaleString('en-US', {
    month: 'short', day: 'numeric',
    hour: '2-digit', minute: '2-digit', second: '2-digit',
  });
}

/** @param {number} ts */
export function fmtTimeShort(ts) {
  return new Date(ts).toLocaleTimeString('en-US', {
    hour: '2-digit', minute: '2-digit', second: '2-digit',
  });
}

/** @param {number} bytes */
export function fmtBytes(bytes) {
  if (bytes < 1024) return bytes + ' B';
  if (bytes < 1048576) return (bytes / 1024).toFixed(1) + ' KB';
  return (bytes / 1048576).toFixed(1) + ' MB';
}

/** @param {number} n */
export function fmtPct(n) {
  return (n >= 0 ? '+' : '') + n.toFixed(1) + '%';
}

/** @param {number} n */
export function fmtNum(n) {
  if (n >= 1e6) return (n / 1e6).toFixed(1) + 'M';
  if (n >= 1e3) return (n / 1e3).toFixed(1) + 'K';
  return String(n);
}

/** @param {number} cents */
export function fmtPrice(cents) {
  return cents + 'c';
}

/** @param {number} n */
export function fmtPnL(n) {
  return (n >= 0 ? '+' : '') + '$' + n.toFixed(2);
}

const VIBRANT_PALETTE = [
  '#60a5fa', '#a78bfa', '#34d399', '#fbbf24', '#f472b0',
  '#f87171', '#22d3ee', '#c084fc', '#fb923c', '#4ade80',
  '#e879f9', '#facc15', '#38bdf8', '#fde047', '#2dd4bf',
  '#f9a8d4', '#a3e635', '#fca5a5', '#d8b4fe', '#7dd3fc',
];

/** @param {string} name */
export function vibrantColor(name) {
  let hash = 0;
  for (let i = 0; i < name.length; i++) {
    hash = ((hash << 5) - hash + name.charCodeAt(i)) | 0;
  }
  return VIBRANT_PALETTE[Math.abs(hash) % VIBRANT_PALETTE.length];
}

/** @param {number} index */
export function vibrantColorByIndex(index) {
  return VIBRANT_PALETTE[index % VIBRANT_PALETTE.length];
}
