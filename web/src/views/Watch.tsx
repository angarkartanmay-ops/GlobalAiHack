import { useState } from "react";
import { ChevronRight, RefreshCw } from "lucide-react";
import { api, type Exposure, type Overview, type Status } from "../api";
import { ConfidenceTag, Notice, ShareBar, Spinner, StatusTag, VerdictTag, ago, longDate, modelName, money, num, pct, windowLabel } from "../ui";

const stageText: Record<string, string> = { armed: "Safety net armed", canary: "Canary", promoted: "Cut over" };

function atRisk(e: Exposure): boolean {
  return e.finding?.status === "deprecated" || e.finding?.status === "retired";
}

function riskColor(risk: string): string {
  return risk === "critical" ? "var(--ember)" : risk === "high" ? "var(--beacon)" : "var(--fjord)";
}

/** The horizon: every retiring model placed by how long it has left, sized by how much traffic rides on it. */
function Horizon({ exposures, onOpen }: { exposures: Exposure[]; onOpen: (id: string) => void }) {
  const items = exposures.filter((e) => atRisk(e) && e.days_left !== undefined);
  if (items.length === 0) return null;
  const W = 1000, H = 172, axisY = 96, x0 = 118, x1 = 968;
  const maxDays = Math.min(365, Math.max(120, Math.ceil(Math.max(...items.map((e) => e.days_left ?? 0)) / 30) * 30 + 15));
  const xOf = (d: number) => x0 + (Math.min(d, maxDays) / maxDays) * (x1 - x0);
  const ticks: number[] = [];
  for (let d = 30; d <= maxDays; d += maxDays > 200 ? 60 : 30) ticks.push(d);

  const past = items.filter((e) => (e.days_left ?? 0) < 0);
  const future = items.filter((e) => (e.days_left ?? 0) >= 0).sort((a, b) => (a.days_left ?? 0) - (b.days_left ?? 0));

  // Alternate labels above and below the line; push apart labels that would collide.
  let lastAbove = -Infinity, lastBelow = -Infinity;
  const placed = future.map((e, i) => {
    const x = xOf(e.days_left ?? 0);
    let above = i % 2 === 0;
    const sameSide = above ? lastAbove : lastBelow;
    const otherSide = above ? lastBelow : lastAbove;
    if (x - sameSide < 170 && x - otherSide > x - sameSide) above = !above;
    if (above) lastAbove = x; else lastBelow = x;
    return { e, x, above };
  });

  const mark = (e: Exposure, x: number, above: boolean, anchor: "start" | "middle" | "end", key: string) => {
    const share = e.traffic?.request_share ?? 0;
    const r = 6 + 15 * Math.sqrt(share);
    const color = riskColor(e.risk);
    const id = `${e.provider}/${e.model}`;
    const labelY = above ? axisY - r - 26 : axisY + r + 22;
    const d = e.days_left ?? 0;
    const when = d < 0 ? `shut down ${-d} d ago` : d === 0 ? "shuts down today" : `${d} days`;
    return (
      <g key={key} className="hz-mark" role="button" tabIndex={0} aria-label={`${id}, ${when}, ${pct(share)} of requests`}
        onClick={() => onOpen(id)} onKeyDown={(ev) => { if (ev.key === "Enter" || ev.key === " ") { ev.preventDefault(); onOpen(id); } }}>
        <circle cx={x} cy={axisY} r={r + 5} fill={color} opacity="0.14" />
        <circle className="core" cx={x} cy={axisY} r={r} fill={color} stroke="var(--surface)" strokeWidth="2" />
        <line x1={x} y1={above ? axisY - r - 3 : axisY + r + 3} x2={x} y2={above ? labelY + 20 : labelY - 14} stroke="var(--line)" strokeWidth="1" />
        <text className="hz-model" x={x} y={labelY} textAnchor={anchor}>{e.model}</text>
        <text className="hz-days" x={x} y={labelY + 15} textAnchor={anchor}>{when} · {pct(share)} of requests</text>
      </g>
    );
  };

  return (
    <div className="horizon">
      <div className="horizon-head">
        <div className="panel-title">Shutdown horizon</div>
        <div className="muted" style={{ fontSize: 13 }}>Circle size is the share of requests the model carries today</div>
      </div>
      <svg viewBox={`0 0 ${W} ${H}`} role="group" aria-label="Timeline of scheduled model shutdowns">
        <line x1={24} y1={axisY} x2={x0} y2={axisY} stroke="var(--ember)" strokeWidth="2" strokeDasharray="2 5" opacity="0.55" />
        <line x1={x0} y1={axisY} x2={x1} y2={axisY} stroke="var(--line)" strokeWidth="2" />
        <line x1={x0} y1={axisY - 30} x2={x0} y2={axisY + 30} stroke="var(--text-2)" strokeWidth="1.5" />
        <text className="hz-tick" x={x0} y={axisY + 46} textAnchor="middle" style={{ fill: "var(--text-2)", fontWeight: 600 }}>Today</text>
        {ticks.map((d) => (
          <g key={d}>
            <line x1={xOf(d)} y1={axisY - 5} x2={xOf(d)} y2={axisY + 5} stroke="var(--line)" strokeWidth="1.5" />
            <text className="hz-tick" x={xOf(d)} y={H - 4} textAnchor="middle">{d} d</text>
          </g>
        ))}
        {past.map((e, i) => mark(e, 62 - i * 6, i % 2 === 0, "start", `p${i}`))}
        {placed.map(({ e, x, above }, i) => mark(e, x, above, x > x1 - 110 ? "end" : x < x0 + 40 ? "start" : "middle", `f${i}`))}
      </svg>
    </div>
  );
}

function NextStep({ e }: { e: Exposure }) {
  if (e.migration) {
    const m = e.migration;
    const label = m.stage === "canary" ? `Canary ${m.canary_pct}%` : stageText[m.stage] ?? m.stage;
    return <span className={`tag ${m.guard_note ? "critical" : m.stage === "promoted" ? "ok" : "warn"}`}>{label}</span>;
  }
  if (e.rehearsal) {
    return <span className="row" style={{ gap: 6, flexWrap: "nowrap" }}><VerdictTag verdict={e.rehearsal.verdict} /><span className="muted tabular">{Math.round(e.rehearsal.score)}%</span></span>;
  }
  if (atRisk(e)) return <span className="soft">Rehearse a replacement</span>;
  return <span className="muted">—</span>;
}

export function Watch({ overview, status, loading, error, onOpen, onNavigate, refresh }: {
  overview: Overview | null; status: Status | null; loading: boolean; error: string;
  onOpen: (id: string) => void; onNavigate: (view: "setup") => void; refresh: () => void;
}) {
  const [starting, setStarting] = useState(false);
  const [scanError, setScanError] = useState("");
  const scanning = starting || !!overview?.scanning;

  const scan = async () => {
    setStarting(true); setScanError("");
    try { await api.scan(); refresh(); } catch (e) { setScanError(e instanceof Error ? e.message : String(e)); }
    window.setTimeout(() => { setStarting(false); refresh(); }, 1500);
  };

  const s = overview?.summary;
  const win = overview?.window;
  const lastScan = status?.scan?.last;
  const exposures = overview?.exposures ?? [];
  const retiredLive = exposures.filter((e) => e.finding?.status === "retired" && (e.traffic?.requests ?? 0) > 0).length;
  const partialDay = !!win && win.days < 1;

  return (
    <>
      <div className="page-head">
        <div>
          <h1 className="page-title">The watch</h1>
          <p className="page-sub">
            Every model serving traffic through this gateway, checked against what its provider has published about retiring it.
          </p>
        </div>
        <div className="row">
          {lastScan ? <span className="muted" style={{ fontSize: 13 }}>Last scan {ago(lastScan.finished_at) || "in progress"}</span> : null}
          <button className="btn primary" onClick={scan} disabled={scanning}>
            {scanning ? <Spinner /> : <RefreshCw size={15} />} {scanning ? "Scanning" : "Scan now"}
          </button>
        </div>
      </div>

      {error ? <div style={{ marginBottom: 18 }}><Notice kind="bad">{error}</Notice></div> : null}
      {scanError ? <div style={{ marginBottom: 18 }}><Notice kind="bad">{scanError}</Notice></div> : null}
      {overview?.inventory_error ? (
        <div style={{ marginBottom: 18 }}>
          <Notice kind="bad"><strong>Could not read traffic from the gateway.</strong> {overview.inventory_error}{" "}
            <button className="btn ghost sm" onClick={() => onNavigate("setup")}>Check setup</button></Notice>
        </div>
      ) : null}
      {lastScan?.status === "failed" && lastScan.error ? (
        <div style={{ marginBottom: 18 }}><Notice kind="warn"><strong>The last scan did not finish.</strong> {lastScan.error}</Notice></div>
      ) : null}
      {status?.tavily?.last_error ? (
        <div style={{ marginBottom: 18 }}><Notice kind="warn"><strong>Some models could not be checked.</strong> {status.tavily.last_error}</Notice></div>
      ) : null}

      {loading && !overview ? (
        <div className="skeleton" style={{ height: 132 }} />
      ) : exposures.length === 0 ? (
        <div className="panel">
          <div className="empty">
            <h3>No traffic seen yet</h3>
            <p>
              {status?.simulated
                ? "The simulated applications are starting up. Their first requests appear here within a few seconds."
                : "Heimdall lists a model once the gateway has logged a request to it. Point an application at the gateway, or send sample traffic from Setup."}
            </p>
            {!status?.simulated ? <div style={{ marginTop: 18 }}><button className="btn" onClick={() => onNavigate("setup")}>Open setup</button></div> : null}
          </div>
        </div>
      ) : s ? (
        <>
          <div className="figures">
            <div className="figure">
              <div className="figure-label">Models serving traffic</div>
              <div className="figure-value">{s.models}</div>
              <div className="figure-note">{win ? `${num(win.requests)} requests in the last ${windowLabel(win.days)}` : ""}</div>
            </div>
            <div className="figure">
              <div className="figure-label">Scheduled for shutdown</div>
              <div className={`figure-value ${s.at_risk ? "alert" : ""}`}>{s.at_risk}</div>
              <div className="figure-note">
                {retiredLive ? `${retiredLive} already shut down and still called` : s.at_risk ? "None has been shut down yet" : s.unchecked ? `${s.unchecked} not checked yet` : "No retirement notices found"}
              </div>
            </div>
            <div className="figure">
              <div className="figure-label">Nearest shutdown</div>
              <div className={`figure-value ${s.nearest && s.nearest.days <= 14 ? "crit" : s.nearest && s.nearest.days <= 45 ? "alert" : ""}`}>
                {s.nearest ? <>{s.nearest.days}<small>{s.nearest.days === 1 ? "day" : "days"}</small></> : "—"}
              </div>
              <div className="figure-note">{s.nearest ? <><span className="mono">{modelName(s.nearest.model)}</span> on {longDate(s.nearest.date)}</> : "Nothing scheduled"}</div>
            </div>
            <div className="figure">
              <div className="figure-label">Requests on retiring models</div>
              <div className={`figure-value ${s.share_at_risk > 0 ? "alert" : ""}`}>{pct(s.share_at_risk)}</div>
              <div className="figure-note">
                {s.at_risk ? `${money(s.daily_cost_at_risk)} a day${partialDay ? " at the current rate" : ""} · ${s.consumers_at_risk} ${s.consumers_at_risk === 1 ? "application" : "applications"}` : "Nothing exposed"}
              </div>
            </div>
          </div>

          <Horizon exposures={exposures} onOpen={onOpen} />

          <div className="section">
            <div className="section-head">
              <div>
                <h2 className="section-title">Models</h2>
                <p className="section-note">Most urgent first. Traffic figures come from the gateway's own logs.</p>
              </div>
            </div>
            <div className="table-wrap">
              <table className="grid">
                <thead>
                  <tr>
                    <th>Model</th><th>Status</th><th>Shutdown</th><th>Share of requests</th><th>Used by</th><th>Confidence</th><th>Next step</th><th aria-label="Open" />
                  </tr>
                </thead>
                <tbody>
                  {exposures.map((e) => {
                    const id = `${e.provider}/${e.model}`;
                    const f = e.finding;
                    const d = e.days_left;
                    return (
                      <tr key={id} tabIndex={0} onClick={() => onOpen(id)} onKeyDown={(ev) => { if (ev.key === "Enter") onOpen(id); }}>
                        <td><div className="cell-model"><span className="mono">{e.model}</span><span className="muted">{e.provider}</span></div></td>
                        <td><StatusTag status={f?.status} risk={e.risk} /></td>
                        <td>
                          {f?.shutdown_date ? (
                            <div className={`days ${e.risk === "critical" ? "crit" : e.risk === "high" ? "high" : ""}`}>
                              {d !== undefined && d >= 0 ? <><b>{d}</b> <span className="soft">{d === 1 ? "day" : "days"}</span></> : <b>Past</b>}
                              <div className="muted" style={{ fontSize: 12.5 }}>{longDate(f.shutdown_date, f.date_precision)}</div>
                            </div>
                          ) : <span className="muted">—</span>}
                        </td>
                        <td>{e.traffic ? <ShareBar share={e.traffic.request_share} risk={atRisk(e)} /> : <span className="muted">No longer called</span>}</td>
                        <td>
                          <div className="chips">
                            {(e.traffic?.consumers ?? []).slice(0, 3).map((c) => <span className="chip" key={c.id}>{c.name}</span>)}
                            {(e.traffic?.consumers?.length ?? 0) > 3 ? <span className="chip">+{(e.traffic?.consumers?.length ?? 0) - 3}</span> : null}
                            {!(e.traffic?.consumers?.length) ? <span className="muted">—</span> : null}
                          </div>
                        </td>
                        <td><ConfidenceTag level={atRisk(e) ? f?.confidence : undefined} /></td>
                        <td><NextStep e={e} /></td>
                        <td><ChevronRight size={16} className="muted" /></td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          </div>
        </>
      ) : null}
    </>
  );
}
