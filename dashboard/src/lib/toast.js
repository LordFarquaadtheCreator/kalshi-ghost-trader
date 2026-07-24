import { writable } from 'svelte/store';

/** @typedef {{ id: number, type: 'ok' | 'err' | 'info', text: string }} Toast */

/** @type {import('svelte/store').Writable<Toast[]>} */
const toasts = writable([]);

let nextID = 1;

/**
 * Push a toast. Auto-dismisses after ms (default 4s).
 * @param {'ok' | 'err' | 'info'} type
 * @param {string} text
 * @param {number} [ms]
 */
export function pushToast(type, text, ms = 4000) {
  const id = nextID++;
  toasts.update((list) => [...list, { id, type, text }]);
  setTimeout(() => dismissToast(id), ms);
}

/** @param {number} id */
export function dismissToast(id) {
  toasts.update((list) => list.filter((t) => t.id !== id));
}

export const toastStore = toasts;
