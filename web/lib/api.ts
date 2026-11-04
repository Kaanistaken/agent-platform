// Small fetch wrapper. All calls go to /api on the same origin (Next.js
// forwards them to the Go backend), so the HttpOnly session cookie and the
// Origin header are sent by the browser itself.

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
    public body: any,
  ) {
    super(message);
  }
}

export async function api<T = any>(path: string, init?: { method?: string; body?: unknown }): Promise<T> {
  const res = await fetch(`/api${path}`, {
    method: init?.method ?? "GET",
    headers: init?.body !== undefined ? { "Content-Type": "application/json" } : undefined,
    body: init?.body !== undefined ? JSON.stringify(init.body) : undefined,
    credentials: "same-origin",
    cache: "no-store",
  });
  if (res.status === 401 && typeof window !== "undefined" && !window.location.pathname.startsWith("/login")) {
    window.location.href = "/login";
  }
  const text = await res.text();
  const body = text ? JSON.parse(text) : null;
  if (!res.ok) {
    throw new ApiError(res.status, body?.error ?? res.statusText, body);
  }
  return body as T;
}

export type User = { id: number; username: string; team_id: number; team_name: string; role: "member" | "lead" };

export type ToolRef = { id: number; name: string; requires_approval: boolean; deleted: boolean };

export type Agent = {
  id: number;
  name: string;
  system_prompt: string;
  provider: string;
  model: string;
  owner: { id: number; username: string };
  tools: ToolRef[];
  deleted: boolean;
};

export type Tool = {
  id: number;
  name: string;
  description: string;
  method: string;
  url_template: string;
  headers_template: Record<string, string>;
  body_template: string;
  params_schema: unknown;
  requires_approval: boolean;
  owner: { id: number; username: string };
  deleted: boolean;
};

export type RunSummary = {
  id: number;
  agent: { id: number; name: string; deleted: boolean };
  started_by: { id: number; username: string };
  task: string;
  status: string;
  error_code?: string;
  created_at: string;
  finished_at?: string;
};

export function fmtTime(s?: string) {
  if (!s) return "";
  const d = new Date(s);
  return isNaN(d.getTime()) ? s : d.toLocaleString();
}
