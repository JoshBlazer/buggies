// Thin fetch wrapper over the Buggies admin JSON API. One place owns the base
// path, the Bearer-token header, the error shape, and JSON parsing so the hooks
// stay declarative.

import { getToken, rejectToken } from "./auth";

export class ApiError extends Error {
  status: number;
  body: unknown;
  constructor(status: number, message: string, body: unknown) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.body = body;
  }
}

// Relative base: the SPA is served by the same daemon under the same origin,
// and base "./" in vite keeps this working behind a subpath. In dev, vite
// proxies /api to the running daemon.
const BASE = "api";

export type Params = Record<string, string | number | boolean | undefined>;

function url(path: string, params?: Params): string {
  const clean = path.replace(/^\/+/, "");
  const u = new URL(`${BASE}/${clean}`, window.location.href);
  if (params) {
    for (const [k, v] of Object.entries(params)) {
      if (v !== undefined && v !== "") u.searchParams.set(k, String(v));
    }
  }
  return u.toString();
}

function authHeaders(extra?: Record<string, string>): Record<string, string> {
  const h: Record<string, string> = { Accept: "application/json", ...extra };
  const token = getToken();
  if (token) h.Authorization = `Bearer ${token}`;
  return h;
}

async function parse<T>(res: Response): Promise<T> {
  const text = await res.text();
  const body = text ? safeJson(text) : null;
  if (!res.ok) {
    let message = `${res.status} ${res.statusText}`;
    if (body && typeof body === "object" && "error" in body) {
      message = String((body as { error: unknown }).error);
    }
    if (res.status === 401) {
      // Token missing or rejected — fall back to the gate with a reason.
      rejectToken("Admin token was rejected. Enter a valid BUGGIES_ADMIN_TOKEN.");
    }
    throw new ApiError(res.status, message, body);
  }
  return body as T;
}

function safeJson(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

export const api = {
  get<T>(path: string, params?: Params): Promise<T> {
    return fetch(url(path, params), { headers: authHeaders() }).then((r) => parse<T>(r));
  },

  put<T>(path: string, body: unknown, params?: Params): Promise<T> {
    return fetch(url(path, params), {
      method: "PUT",
      headers: authHeaders({ "Content-Type": "application/json" }),
      body: JSON.stringify(body),
    }).then((r) => parse<T>(r));
  },

  post<T>(path: string, body?: unknown, params?: Params): Promise<T> {
    return fetch(url(path, params), {
      method: "POST",
      headers: authHeaders({ "Content-Type": "application/json" }),
      body: body === undefined ? undefined : JSON.stringify(body),
    }).then((r) => parse<T>(r));
  },
};
