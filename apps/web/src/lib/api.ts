/**
 * Typed API client.
 *
 * The access token lives in memory only. It is never written to localStorage,
 * because anything in localStorage is readable by any script that gets onto
 * the page. The refresh token is an httpOnly cookie the browser handles and
 * this code never sees.
 */

const BASE = "/api/v1";

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
    readonly fields?: Record<string, unknown>,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

let accessToken: string | null = null;
let onSessionLost: (() => void) | null = null;

export function setAccessToken(token: string | null) {
  accessToken = token;
}

export function onSessionExpired(handler: () => void) {
  onSessionLost = handler;
}

/**
 * A single in-flight refresh.
 *
 * Refresh tokens rotate and the server revokes the whole family when one is
 * replayed, which is correct: a replay normally means the token leaked. That
 * makes a double refresh fatal rather than merely wasteful, and two of them
 * are easy to cause by accident, so every path goes through this one promise.
 */
let refreshing: Promise<boolean> | null = null;

export async function refreshSession(): Promise<boolean> {
  refreshing ??= (async () => {
    try {
      const res = await fetch(`${BASE}/auth/refresh`, {
        method: "POST",
        credentials: "same-origin",
      });
      if (!res.ok) return false;
      const body = (await res.json()) as SessionResponse;
      accessToken = body.access_token;
      lastSession = body;
      return true;
    } catch {
      return false;
    } finally {
      refreshing = null;
    }
  })();
  return refreshing;
}

/** The session payload from the most recent successful refresh or login. */
let lastSession: SessionResponse | null = null;

export function currentSession(): SessionResponse | null {
  return lastSession;
}

export function rememberSession(session: SessionResponse) {
  lastSession = session;
  accessToken = session.access_token;
}

type RequestOptions = {
  method?: string;
  body?: unknown;
  /** FormData is sent as-is; the browser sets the multipart boundary. */
  form?: FormData;
  signal?: AbortSignal;
  /** Skip the refresh-and-retry dance, used by login and refresh themselves. */
  anonymous?: boolean;
};

async function send(path: string, options: RequestOptions): Promise<Response> {
  const headers: Record<string, string> = {};
  if (options.body !== undefined) headers["Content-Type"] = "application/json";
  if (accessToken && !options.anonymous) headers.Authorization = `Bearer ${accessToken}`;

  return fetch(`${BASE}${path}`, {
    method: options.method ?? "GET",
    credentials: "same-origin",
    headers,
    body: options.form ?? (options.body !== undefined ? JSON.stringify(options.body) : undefined),
    signal: options.signal,
  });
}

export async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  let res = await send(path, options);

  if (res.status === 401 && !options.anonymous) {
    if (await refreshSession()) {
      res = await send(path, options);
    } else {
      onSessionLost?.();
    }
  }

  if (res.status === 204) return undefined as T;

  if (!res.ok) {
    let code = "request_failed";
    let message = `Request failed with status ${res.status}.`;
    let fields: Record<string, unknown> | undefined;
    try {
      const body = (await res.json()) as {
        error?: { code?: string; message?: string; fields?: Record<string, unknown> };
      };
      if (body.error) {
        code = body.error.code ?? code;
        message = body.error.message ?? message;
        fields = body.error.fields;
      }
    } catch {
      // A non-JSON error body means a proxy or a crash, not the API.
    }
    throw new ApiError(res.status, code, message, fields);
  }

  return (await res.json()) as T;
}

export const api = {
  get: <T>(path: string, signal?: AbortSignal) => request<T>(path, { signal }),
  post: <T>(path: string, body?: unknown) => request<T>(path, { method: "POST", body }),
  put: <T>(path: string, body?: unknown) => request<T>(path, { method: "PUT", body }),
  patch: <T>(path: string, body?: unknown) => request<T>(path, { method: "PATCH", body }),
  del: <T>(path: string) => request<T>(path, { method: "DELETE" }),
  upload: <T>(path: string, form: FormData) => request<T>(path, { method: "POST", form }),
};

/* ----------------------------------------------------------------------------
   Response types. These mirror the Go handlers; they are hand-written until
   the protobuf contracts generate them.
---------------------------------------------------------------------------- */

export type User = {
  id: string;
  email: string;
  full_name: string;
  organization_id: string;
  organization?: string;
};

export type SessionResponse = {
  access_token: string;
  expires_at: string;
  user: User;
};

export type Me = { user: User; permissions: string[] };

export type StageStatus =
  | "pending"
  | "active"
  | "grace"
  | "completed"
  | "expired"
  | "skipped"
  | "failed";

export type StageKind =
  | "wait"
  | "artifact_submission"
  | "live_turn"
  | "automated_evaluation"
  | "human_review";

export type AssignmentStage = {
  id: string;
  stage_id: string;
  stage_kind: StageKind;
  session_id?: string;
  label?: string;
  status: StageStatus;
  sort_order: number;
  opens_at: string | null;
  due_at: string | null;
  grace_until: string | null;
  started_at: string | null;
  completed_at: string | null;
};

export type Assignment = {
  id: string;
  assessment_id: string;
  assessment_title?: string;
  team_id: string;
  team_name?: string;
  side: string;
  status:
    | "assigned"
    | "in_progress"
    | "awaiting_review"
    | "finalized"
    | "abandoned"
    | "withdrawn";
  current_stage_id: string | null;
  current_stage_kind: string | null;
  stages?: AssignmentStage[];
  assigned_at: string;
};

export type Resource = {
  id: string;
  kind: "problem" | "authority" | "statute" | "evidence" | "guidance" | "other";
  title: string;
  body?: string;
  available: boolean;
};

export type ComplianceFinding = {
  rule: string;
  section?: string;
  status: "pass" | "fail" | "not_checkable";
  severity: "error" | "warning";
  message: string;
  expected?: string;
  actual?: string;
};

export type ComplianceReport = {
  rule_set_key: string;
  findings: ComplianceFinding[];
  sections: { key: string; name: string; heading: string; word_count: number }[];
  passed: number;
  failed: number;
  warnings: number;
  fraction: number;
  page_count: number;
  word_count: number;
};

export type Submission = {
  id: string;
  stage_id: string;
  version: number;
  filename: string;
  byte_size: number;
  pages: number | null;
  word_count: number;
  is_late: boolean;
  locked_at: string | null;
  submitted_at: string | null;
  compliance?: ComplianceReport;
  uploaded_by: string | null;
};

export type Assessment = {
  id: string;
  template_version_id: string;
  template_name: string;
  assessment_type: string;
  title: string;
  description: string;
  status: "draft" | "published" | "running" | "closed" | "archived";
  opens_at: string | null;
  closes_at: string | null;
  published_at: string | null;
  assignment_count: number;
  created_at: string;
};

export type Team = {
	 membership_locked?: boolean;
  id: string;
  name: string;
  members: {
    user_id: string;
    full_name: string;
    email: string;
    role: "speaker" | "researcher";
    speaking_order: number | null;
  }[];
  created_at: string;
};

/* ------------------------------------------------------------------- models */

export type ModelTier = "monitor" | "judge" | "grader" | "embedding";

/** The API key is never returned. `api_key_hint` is the last four characters. */
export type ModelProvider = {
  id: string;
  key: string;
  name: string;
  kind: "openai_compatible" | "anthropic";
  base_url: string;
  api_key_hint: string;
  has_api_key: boolean;
  is_active: boolean;
  created_at: string;
  updated_at: string;
};

export type ModelBinding = {
  tier: ModelTier;
  provider_id: string;
  provider_key: string;
  model: string;
  temperature: number;
  top_p: number;
  max_tokens: number;
  timeout_ms: number;
  /** "" leaves the provider's own default alone; it is not the same as "none". */
  reasoning_effort: ReasoningEffort;
  updated_at: string;
};

export const reasoningEfforts = ["", "none", "low", "medium", "high"] as const;
export type ReasoningEffort = (typeof reasoningEfforts)[number];

/** A failed test is a 200 with ok=false: the request worked, the provider did not. */
export type ConnectionTest = {
  ok: boolean;
  reachable: boolean;
  models: string[];
  model: string;
  sample: string;
  latency_ms: number;
  error: string;
  unauthorized: boolean;
};

export type AiProfile = {
  id: string;
  key: string;
  name: string;
  role: "judge" | "examiner" | "interviewer" | "opponent" | "moderator" | "evaluator";
  version: number;
  model_tier: "monitor" | "judge" | "grader";
  system_prompt: string;
  temperature: number;
  voice: string;
  personality: Record<string, unknown>;
  interruption_policy: Record<string, unknown>;
  focus: string[];
  capabilities: string[];
  rag_sources: string[];
  is_active: boolean;
  created_at: string;
  updated_at: string;
};

