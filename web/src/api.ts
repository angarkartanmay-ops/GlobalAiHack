// Types mirror internal/core/types.go and the handlers in internal/server/api.go.

export interface Consumer { id: string; name: string; requests: number; cost: number }

export interface Traffic {
  provider: string; model: string; requests: number; success_rate: number; tokens: number; cost: number;
  avg_latency_ms: number; request_share: number; cost_share: number; daily_requests: number; daily_cost: number;
  consumers: Consumer[] | null;
}

export interface Evidence {
  id: string; kind: "web" | "gateway"; via: string; url?: string; domain?: string; title?: string; quote: string;
  grounded: boolean; official: boolean; applies: string; status?: string; shutdown_date?: string;
  date_precision?: string; replacement?: string; published_date?: string; retrieved_at: string;
}

export interface Change { at: string; field: string; from: string; to: string }

export interface Finding {
  provider: string; model: string; status: string; shutdown_date?: string; date_precision?: string;
  replacement?: string; replacement_basis?: string; replacement_on_gateway: boolean; confidence: string;
  evidence: Evidence[]; inference: string[]; discarded_ungrounded: number;
  conflict?: { dates: string[]; summary: string }; first_seen: string; last_verified: string;
  changes?: Change[]; error?: string;
}

export interface CanaryHealth {
  since: string; old_requests: number; new_requests: number; old_success_rate: number; new_success_rate: number;
  old_latency_ms: number; new_latency_ms: number; fallbacks: number; checked_at: string;
}

export interface Migration {
  model: string; replacement: string; stage: string; canary_pct: number; rule_id?: string; rule_name?: string;
  updated_at: string; health?: CanaryHealth; history?: Change[]; guard_note?: string;
}

export interface RehearsalRef { id: string; candidate: string; verdict: string; score: number; at: string }

export interface Exposure {
  provider: string; model: string; finding?: Finding; traffic?: Traffic; days_left?: number; risk: string;
  migration?: Migration; rehearsal?: RehearsalRef;
}

export interface Summary {
  models: number; at_risk: number; critical: number; unchecked: number; share_at_risk: number;
  daily_cost_at_risk: number; daily_requests_at_risk: number; consumers_at_risk: number;
  nearest?: { model: string; days: number; date: string };
}

export interface Overview {
  summary: Summary; exposures: Exposure[]; scanning: boolean; now: string; inventory_error?: string;
  window?: { days: number; requests: number; cost: number; taken_at: string; lookback_hours: number };
}

export interface RehearsalSummary {
  id: string; baseline: string; candidate: string; status: string; started_at: string; finished_at: string;
  requested: number; sampled: number; completed: number; settled: number; outcomes: Record<string, number> | null;
  score: number; verdict: string; summary: string; cost_delta_pct: number; latency_delta_pct: number; error?: string;
}

export interface Output {
  content: string; tool_calls?: { name: string; arguments: string }[]; finish_reason?: string;
  latency_ms: number; prompt_tokens: number; output_tokens: number; cost: number; error?: string;
}

export interface Tiebreak {
  topic: string; baseline_claim: string; candidate_claim: string; baseline_verdict: string; candidate_verdict: string;
  winner: string; evidence: Evidence[];
}

export interface Sample {
  log_id: string; at: string; consumer?: string; prompt: string; messages: number; has_tools: boolean;
  baseline: Output; candidate: Output; checks: { name: string; pass: boolean; detail?: string }[];
  outcome: string; judge_reason?: string; tiebreaks?: Tiebreak[];
}

export interface Rehearsal {
  id: string; baseline: string; candidate: string; status: string; started_at: string; finished_at: string;
  requested: number; sampled: number; completed: number; outcomes: Record<string, number>;
  check_failures: Record<string, number>; score: number; verdict: string; summary: string;
  baseline_cost: number; candidate_cost: number; cost_delta_pct: number;
  baseline_latency_p50_ms: number; candidate_latency_p50_ms: number; latency_delta_pct: number;
  candidate_fresher: number; candidate_staler: number; spend_usd: number; budget_usd: number;
  stopped_early?: string; samples: Sample[]; error?: string; judge_model?: string;
}

export interface Claim {
  id: string; text: string; time_sensitive: boolean; category: string; query?: string; checked: boolean;
  verdict?: string; current_value?: string; confidence?: string; evidence: Evidence[]; inference?: string;
}

export interface FreshnessReport {
  id: string; at: string; question?: string; answer: string; model?: string; claims: Claim[];
  supported: number; changed: number; unverifiable: number; not_checked: number; freshness_score: number;
  rewrite?: string; tavily_searches: number; llm_calls: number; source: string; error?: string;
  elapsed_ms: number; analyst_model?: string;
}

export interface EventItem { id: string; at: string; kind: string; level: string; model?: string; title: string; detail?: string; alerted: boolean }

export interface Scan {
  id: string; started_at: string; finished_at: string; status: string; models: number; at_risk: number;
  tavily_searches: number; tavily_extracts: number; llm_calls: number; error?: string; trigger: string;
}

export interface Status {
  version: string; mode: "sim" | "live"; simulated: boolean; auth_required: boolean; locked: boolean;
  authenticated: boolean; config_problems: string[] | null;
  gateway?: { url: string; healthy: boolean; admin: boolean; virtual_key: boolean; models?: number; error?: string };
  tavily?: { configured: boolean; mode?: string; searches?: number; extracts?: number; last_error?: string };
  analyst?: { model: string; judge: string; auto: boolean; calls: number };
  scan?: { running: boolean; next: string; interval_hours: number; last?: Scan };
  settings?: { lookback_hours: number; rehearsal_samples: number; rehearsal_budget_usd: number; open_web: boolean; auto_rollback: boolean; alerts: boolean; traffic_keys: boolean };
  sim?: { traffic_sent: number; traffic_failed: number; retired: string[] | null; examples: { label: string; question: string; answer: string }[] };
  inventory_error?: string;
}

export interface ModelDetail {
  exposure: Exposure; candidates: { id: string; reason: string }[]; rehearsals: RehearsalSummary[]; migration: Migration | null;
}

const TOKEN_KEY = "heimdall.token";

export function getToken(): string {
  try { return sessionStorage.getItem(TOKEN_KEY) ?? ""; } catch { return ""; }
}

export function setToken(token: string) {
  try { token ? sessionStorage.setItem(TOKEN_KEY, token) : sessionStorage.removeItem(TOKEN_KEY); } catch { /* storage unavailable */ }
}

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) { super(message); this.status = status; }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {};
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const token = getToken();
  if (token) headers["Authorization"] = `Bearer ${token}`;
  let resp: Response;
  try {
    resp = await fetch(path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
  } catch {
    throw new ApiError(0, "Heimdall is not reachable. Check that it is still running.");
  }
  const text = await resp.text();
  let data: unknown = null;
  try { data = text ? JSON.parse(text) : null; } catch { /* not JSON */ }
  if (!resp.ok) {
    const msg = (data as { error?: string } | null)?.error ?? `Request failed (${resp.status})`;
    throw new ApiError(resp.status, msg);
  }
  return data as T;
}

export const api = {
  status: () => request<Status>("GET", "/api/status"),
  overview: () => request<Overview>("GET", "/api/overview"),
  model: (id: string) => request<ModelDetail>("GET", `/api/model?id=${encodeURIComponent(id)}`),
  scan: () => request<{ started: boolean }>("POST", "/api/scan"),
  scans: () => request<{ scans: Scan[] }>("GET", "/api/scans"),
  gatewayModels: () => request<{ models: string[] }>("GET", "/api/gateway/models"),
  startRehearsal: (baseline: string, candidate: string, samples: number) =>
    request<{ id: string }>("POST", "/api/rehearsals", { baseline, candidate, samples }),
  rehearsal: (id: string) => request<Rehearsal>("GET", `/api/rehearsal?id=${encodeURIComponent(id)}`),
  rehearsals: () => request<{ rehearsals: RehearsalSummary[] }>("GET", "/api/rehearsals"),
  migrate: (model: string, replacement: string, stage: string, canary_pct = 0) =>
    request<Migration>("POST", "/api/migrations", { model, replacement, stage, canary_pct }),
  freshness: (question: string, answer: string, rewrite: boolean) =>
    request<FreshnessReport>("POST", "/api/freshness", { question, answer, rewrite }),
  freshnessList: () => request<{ reports: FreshnessReport[] }>("GET", "/api/freshness"),
  events: () => request<{ events: EventItem[] }>("GET", "/api/events"),
  simRetire: (model: string, revive: boolean) => request<{ retired: string[] | null }>("POST", "/api/sim/retire", { model, revive }),
  traffic: (models: { large: string; small: string; other: string; legacy: string }) =>
    request<{ started: boolean; requests: number }>("POST", "/api/traffic", models),
};
