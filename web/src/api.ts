export type ApiError = { error: string; message: string; status: number };

const state = {
  csrf: "",
  stream: "unknown" as "live" | "paused" | "unknown",
};

export function csrf(): string {
  return state.csrf;
}
export function setCsrf(v: string) {
  state.csrf = v;
}
export function streamState() {
  return state.stream;
}
export function setStream(v: typeof state.stream) {
  state.stream = v;
}

export function newKey(): string {
  if (crypto.randomUUID) return crypto.randomUUID();
  return Math.random().toString(16).slice(2) + Date.now().toString(16);
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  if (init.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  if (init.method && init.method !== "GET" && init.method !== "HEAD" && state.csrf) {
    headers.set("X-CSRF-Token", state.csrf);
  }
  const res = await fetch(path, { ...init, headers, credentials: "same-origin" });
  const text = await res.text();
  let data: unknown = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = { error: "non_json", message: text };
    }
  }
  if (!res.ok) {
    const err = (data || {}) as { error?: string; message?: string };
    throw { error: err.error || "http", message: err.message || res.statusText, status: res.status } satisfies ApiError;
  }
  return data as T;
}

export function get<T>(path: string) {
  return api<T>(path);
}
export function post<T>(path: string, body: unknown, extra: HeadersInit = {}) {
  return api<T>(path, { method: "POST", body: JSON.stringify(body), headers: extra });
}

export async function submitOp(action: string, params: Record<string, unknown> = {}, targetIds: string[] = [], targetMode = "") {
  const key = newKey();
  sessionStorage.setItem("monik:last-key", key);
  try {
    const op = await post<Record<string, unknown>>("/api/v1/operations", {
      action,
      client_request_key: key,
      params,
      target_ids: targetIds,
      target_mode: targetMode || undefined,
    });
    sessionStorage.removeItem("monik:last-key");
    return { key, op, unknown: false as const };
  } catch (e) {
    const err = e as ApiError;
    if (err.status === 0 || err.status >= 500 || err.error === "http") {
      try {
        const looked = await post<{ found: boolean; operation?: Record<string, unknown> }>("/api/v1/operations/lookup", {
          client_request_key: key,
          action,
          target_ids: targetIds,
          params,
        });
        if (looked.found && looked.operation) {
          sessionStorage.removeItem("monik:last-key");
          return { key, op: looked.operation, unknown: false as const };
        }
      } catch {
        /* keep unknown */
      }
      return { key, op: null, unknown: true as const };
    }
    throw e;
  }
}
