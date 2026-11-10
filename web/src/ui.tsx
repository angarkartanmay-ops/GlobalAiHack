import { useEffect, useRef, useState, type ReactNode } from "react";
import { AlertTriangle, Check, CircleHelp, ExternalLink, Info, Loader2, ShieldCheck, X } from "lucide-react";
import type { Evidence } from "./api";

// ------------------------------------------------------------- formatting

export function pct(share: number, digits = 0): string {
  const v = share * 100;
  if (v > 0 && v < 1 && digits === 0) return "<1%";
  return `${v.toFixed(digits)}%`;
}

export function money(v: number): string {
  if (v === 0) return "$0";
  if (v < 0.01) return `$${v.toFixed(4)}`;
  if (v < 100) return `$${v.toFixed(2)}`;
  return `$${Math.round(v).toLocaleString("en-US")}`;
}

export function num(v: number): string {
  return Math.round(v).toLocaleString("en-US");
}

export function signed(v: number, unit = "%"): string {
  const r = Math.round(v);
  return `${r > 0 ? "+" : r < 0 ? "−" : ""}${Math.abs(r)}${unit}`;
}

export function longDate(iso?: string, precision?: string): string {
  if (!iso) return "";
  const d = new Date(`${iso.slice(0, 10)}T00:00:00Z`);
  if (Number.isNaN(d.getTime())) return iso;
  const opts: Intl.DateTimeFormatOptions = precision === "month"
    ? { month: "long", year: "numeric", timeZone: "UTC" }
    : { day: "numeric", month: "short", year: "numeric", timeZone: "UTC" };
  return d.toLocaleDateString("en-GB", opts);
}

export function ago(iso?: string): string {
  if (!iso) return "";
  const t = new Date(iso).getTime();
  if (Number.isNaN(t) || t < 86400000) return "";
  const s = Math.max(0, (Date.now() - t) / 1000);
  if (s < 50) return "just now";
  if (s < 3600) return `${Math.round(s / 60)} min ago`;
  if (s < 86400) return `${Math.round(s / 3600)} h ago`;
  return `${Math.round(s / 86400)} d ago`;
}

export function windowLabel(days: number): string {
  if (days >= 1.5) return `${Math.round(days)} days`;
  if (days >= 0.96) return "24 hours";
  const h = Math.max(1, Math.round(days * 24));
  return h === 1 ? "hour" : `${h} hours`;
}

export function modelName(id: string): string {
  const i = id.indexOf("/");
  return i > 0 ? id.slice(i + 1) : id;
}

// ------------------------------------------------------------------- tags

const statusText: Record<string, string> = {
  deprecated: "Shutdown scheduled", retired: "Shut down", active: "Current", no_notice: "No notice found", unknown: "Not checked",
};

export function StatusTag({ status, risk }: { status?: string; risk?: string }) {
  const s = status ?? "unknown";
  const cls = s === "retired" ? "critical" : s === "deprecated" ? (risk === "critical" ? "critical" : risk === "medium" || risk === "low" ? "medium" : "high") : s === "active" ? "ok" : "";
  return <span className={`tag ${cls}`}>{statusText[s] ?? s}</span>;
}

export function ConfidenceTag({ level }: { level?: string }) {
  if (!level || level === "none") return <span className="muted">—</span>;
  const label = level === "high" ? "High" : level === "medium" ? "Medium" : "Low";
  return (
    <span className="tag outline" title={confidenceHelp[level]}>
      {level === "high" ? <ShieldCheck size={12} /> : null}
      {label}
    </span>
  );
}

const confidenceHelp: Record<string, string> = {
  high: "An official source names this exact model and gives a full date, quoted verbatim.",
  medium: "Official, but the notice names an alias or gives only a month; or two independent third-party sources agree.",
  low: "A single third-party source. No official confirmation was retrieved.",
};

const outcomeText: Record<string, string> = {
  equivalent: "Equivalent", better: "Better", acceptable: "Acceptable", regression: "Regression", error: "Failed",
};

export function OutcomeTag({ outcome }: { outcome: string }) {
  const cls = outcome === "regression" || outcome === "error" ? "critical" : outcome === "better" ? "medium" : outcome === "equivalent" ? "ok" : "";
  return <span className={`tag ${cls}`}>{outcomeText[outcome] ?? outcome}</span>;
}

const verdictText: Record<string, string> = { ready: "Ready", ready_with_caveats: "Ready with caveats", not_ready: "Not ready" };

export function VerdictTag({ verdict }: { verdict: string }) {
  const cls = verdict === "ready" ? "ok" : verdict === "ready_with_caveats" ? "warn" : verdict === "not_ready" ? "critical" : "";
  return <span className={`tag ${cls}`}>{verdictText[verdict] ?? "Running"}</span>;
}

export function verdictTitle(verdict: string): string {
  return verdictText[verdict] ?? "Rehearsal";
}

const claimText: Record<string, string> = { supported: "Still true", changed: "Out of date", unverifiable: "Could not verify" };

export function ClaimTag({ verdict }: { verdict?: string }) {
  if (!verdict) return <span className="tag">Not time-sensitive</span>;
  const cls = verdict === "supported" ? "ok" : verdict === "changed" ? "critical" : "";
  return (
    <span className={`tag ${cls}`}>
      {verdict === "supported" ? <Check size={12} /> : verdict === "changed" ? <X size={12} /> : <CircleHelp size={12} />}
      {claimText[verdict] ?? verdict}
    </span>
  );
}

// --------------------------------------------------------------- evidence

const dateRe = /\b(?:\d{4}-\d{2}-\d{2}|(?:January|February|March|April|May|June|July|August|September|October|November|December|Jan|Feb|Mar|Apr|Jun|Jul|Aug|Sept?|Oct|Nov|Dec)\.? \d{1,2}(?:st|nd|rd|th)?,? \d{4}|\d{1,2} (?:January|February|March|April|May|June|July|August|September|October|November|December) \d{4}|(?:January|February|March|April|May|June|July|August|September|October|November|December) \d{4})\b/g;

const months = ["jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"];

/** Normalises a date as written in a source to YYYY-MM-DD (or YYYY-MM for a bare month). */
function isoOf(written: string): string {
  if (/^\d{4}-\d{2}-\d{2}$/.test(written)) return written;
  const year = written.match(/\d{4}/)?.[0] ?? "";
  const month = months.findIndex((m) => written.toLowerCase().includes(m)) + 1;
  const day = written.replace(year, "").match(/\d{1,2}/)?.[0];
  if (!year || !month) return "";
  const mm = String(month).padStart(2, "0");
  return day ? `${year}-${mm}-${day.padStart(2, "0")}` : `${year}-${mm}`;
}

/**
 * Renders a quote with the date that matters marked. When the evidence
 * carries a shutdown date, only that date is marked, so an announcement date
 * in the same table row is not mistaken for it.
 */
function Quote({ text, highlight }: { text: string; highlight?: string }) {
  const parts: ReactNode[] = [];
  let last = 0;
  for (const m of text.matchAll(dateRe)) {
    const iso = isoOf(m[0]);
    if (highlight && !(iso === highlight || (iso.length === 7 && highlight.startsWith(iso)))) continue;
    const i = m.index ?? 0;
    if (i > last) parts.push(text.slice(last, i));
    parts.push(<mark key={i}>{m[0]}</mark>);
    last = i + m[0].length;
  }
  if (last < text.length) parts.push(text.slice(last));
  return <blockquote>{parts}</blockquote>;
}

const viaText: Record<string, string> = {
  "tavily.search": "Tavily search", "tavily.extract": "Tavily extract", "bifrost.models": "Bifrost model API",
};

export function EvidenceItem({ ev, showApplies = true }: { ev: Evidence; showApplies?: boolean }) {
  const cls = ev.kind === "gateway" ? "gateway" : ev.official ? "official" : "";
  return (
    <div className={`evidence ${cls}`}>
      <Quote text={ev.quote} highlight={ev.shutdown_date} />
      {showApplies && (ev.shutdown_date || ev.replacement) ? (
        <div className="evidence-read">
          Read as:{ev.shutdown_date ? <> shutdown <b>{longDate(ev.shutdown_date, ev.date_precision)}</b></> : null}
          {ev.shutdown_date && ev.replacement ? "," : ""}{ev.replacement ? <> replacement <b className="mono">{ev.replacement}</b></> : null}
        </div>
      ) : null}
      <div className="evidence-src">
        {ev.kind === "gateway" ? (
          <span className="tag medium">Provider API</span>
        ) : ev.official ? (
          <span className="tag ok">Official source</span>
        ) : showApplies ? (
          <span className="tag">Third party</span>
        ) : null}
        {showApplies && ev.applies === "alias" ? <span className="tag outline">Names an alias</span> : null}
        {showApplies && ev.applies === "family" ? <span className="tag outline">Related model</span> : null}
        {ev.url ? (
          <a href={ev.url} target="_blank" rel="noreferrer">
            {ev.domain || ev.url} <ExternalLink size={11} style={{ verticalAlign: "-1px" }} />
          </a>
        ) : (
          <span>{ev.title}</span>
        )}
        <span>via {viaText[ev.via] ?? ev.via}</span>
        {ev.published_date ? <span>published {longDate(ev.published_date)}</span> : null}
      </div>
    </div>
  );
}

export function InferenceBlock({ items, label = "Heimdall's inference, not from a source" }: { items: string[]; label?: string }) {
  if (!items.length) return null;
  return (
    <div className="inference">
      <div className="inference-label"><Info size={14} /> {label}</div>
      <ul>{items.map((s, i) => <li key={i}>{s}</li>)}</ul>
    </div>
  );
}

// ---------------------------------------------------------------- pieces

export function Notice({ kind = "info", children }: { kind?: "info" | "warn" | "bad" | "good"; children: ReactNode }) {
  const Icon = kind === "good" ? Check : kind === "info" ? Info : AlertTriangle;
  return (
    <div className={`notice ${kind === "info" ? "" : kind}`} role={kind === "bad" ? "alert" : undefined}>
      <Icon size={16} />
      <div>{children}</div>
    </div>
  );
}

export function Spinner({ size = 16 }: { size?: number }) {
  return <Loader2 size={size} className="spin" aria-label="Loading" />;
}

export function ShareBar({ share, risk }: { share: number; risk?: boolean }) {
  return (
    <div className="share">
      <div className={`share-bar ${risk ? "risk" : ""}`}><i style={{ width: `${Math.max(2, Math.min(100, share * 100))}%` }} /></div>
      <span className="share-num">{pct(share)}</span>
    </div>
  );
}

export function Ring({ score, tone }: { score: number; tone: "ok" | "warn" | "bad" }) {
  const r = 58;
  const c = 2 * Math.PI * r;
  const color = tone === "ok" ? "var(--aurora)" : tone === "warn" ? "var(--beacon)" : "var(--ember)";
  return (
    <div className="ring" role="img" aria-label={`Score ${Math.round(score)} percent`}>
      <svg width="132" height="132" viewBox="0 0 132 132">
        <circle cx="66" cy="66" r={r} fill="none" stroke="var(--surface-3)" strokeWidth="8" />
        <circle cx="66" cy="66" r={r} fill="none" stroke={color} strokeWidth="8" strokeLinecap="round"
          strokeDasharray={`${(Math.max(0, Math.min(100, score)) / 100) * c} ${c}`} />
      </svg>
      <div className="ring-num"><span>{Math.round(score)}<small>%</small></span></div>
    </div>
  );
}

// ------------------------------------------------------------------ hooks

/** Calls load now and every intervalMs while enabled. Keeps the last good value on error. */
export function usePoll<T>(load: () => Promise<T>, intervalMs: number, deps: unknown[] = [], enabled = true) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string>("");
  const [loading, setLoading] = useState(true);
  const loadRef = useRef(load);
  loadRef.current = load;
  const tick = useRef(0);

  const refresh = async () => {
    const mine = ++tick.current;
    try {
      const d = await loadRef.current();
      if (mine === tick.current) { setData(d); setError(""); }
    } catch (e) {
      if (mine === tick.current) setError(e instanceof Error ? e.message : String(e));
    } finally {
      if (mine === tick.current) setLoading(false);
    }
  };

  useEffect(() => {
    if (!enabled) return;
    setLoading(true);
    void refresh();
    if (intervalMs <= 0) return;
    const id = window.setInterval(() => { if (!document.hidden) void refresh(); }, intervalMs);
    return () => window.clearInterval(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [intervalMs, enabled, ...deps]);

  return { data, error, loading, refresh };
}
