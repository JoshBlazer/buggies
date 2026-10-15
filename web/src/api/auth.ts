// Admin-token store for the dashboard.
//
// The daemon's admin API is fail-closed: every /api/* request needs
// `Authorization: Bearer <BUGGIES_ADMIN_TOKEN>`, compared constant-time on the
// Go side. There is no login endpoint and no cookie — the token is held only in
// memory and (for convenience across reloads) in localStorage, and sent in a
// header so there is zero CSRF surface and the browser caches no credential.
//
// This module is a tiny external store so React can subscribe to it with
// useSyncExternalStore: setting or clearing the token re-renders the gate.

const KEY = "buggies.adminToken";

let token: string | null = readInitial();
let lastError: string | null = null;
const listeners = new Set<() => void>();

function readInitial(): string | null {
  try {
    return window.localStorage.getItem(KEY);
  } catch {
    return null;
  }
}

function emit() {
  for (const fn of listeners) fn();
}

export function getToken(): string | null {
  return token;
}

export function setToken(value: string) {
  token = value.trim() || null;
  lastError = null;
  try {
    if (token) window.localStorage.setItem(KEY, token);
    else window.localStorage.removeItem(KEY);
  } catch {
    /* private mode / blocked storage: in-memory token still works */
  }
  emit();
}

/** Called by the client on a 401 so the gate reappears with a reason. */
export function rejectToken(message: string) {
  token = null;
  lastError = message;
  try {
    window.localStorage.removeItem(KEY);
  } catch {
    /* ignore */
  }
  emit();
}

export function clearToken() {
  lastError = null;
  setToken("");
}

export function getLastError(): string | null {
  return lastError;
}

export function subscribe(fn: () => void): () => void {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
}
