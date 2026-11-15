import { useState } from "react";
import { ChevronRight, RefreshCw } from "lucide-react";
import { api, type Exposure, type Overview, type Status } from "../api";
import { Sunset, type Sun } from "../Sunset";
import { ConfidenceTag, Notice, ShareBar, Spinner, StatusTag, VerdictTag, ago, longDate, modelName, money, num, pct, windowLabel } from "../ui";

const stageText: Record<string, string> = { armed: "Safety net armed", canary: "Canary", promoted: "Cut over" };

function atRisk(e: Exposure): boolean {
  return e.finding?.status === "deprecated" || e.finding?.status === "retired";
}

/** The horizon: every retiring model placed by how long it has left, sized by how much traffic rides on it. */
function Horizon({ exposures, onOpen }: { exposures: Exposure[]; onOpen: (id: string) => void }) {
  const suns: Sun[] = exposures
    .filter((e) => atRisk(e) && e.days_left !== undefined)
    .map((e) => ({ id: `${e.provider}/${e.model}`, label: e.model, days: e.days_left ?? 0, share: e.traffic?.request_share ?? 0 }));
  if (suns.length === 0) return null;
  return (
    <div className="horizon">
      <div className="horizon-head">
        <div className="panel-title">Shutdown horizon</div>
        <div className="muted" style={{ fontSize: 13.5 }}>The lower a model sits, the sooner it is shut down. Size is its share of requests today.</div>
      </div>
      <Sunset suns={suns} onOpen={onOpen} />
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
  const waking = !!status?.simulated && !!status.gateway && !status.gateway.healthy;

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

      {waking ? (
        <div style={{ marginBottom: 18 }}>
          <Notice kind="info"><span className="row"><Spinner size={14} /> <span><strong>The demo gateway is starting.</strong> This takes about 20 seconds after a quiet period. The page updates by itself.</span></span></Notice>
        </div>
      ) : null}
      {!waking && error ? <div style={{ marginBottom: 18 }}><Notice kind="bad">{error}</Notice></div> : null}
      {scanError ? <div style={{ marginBottom: 18 }}><Notice kind="bad">{scanError}</Notice></div> : null}
      {!waking && overview?.inventory_error ? (
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
            <div className="cards">
              {exposures.map((e) => {
                const id = `${e.provider}/${e.model}`;
                const f = e.finding;
                const d = e.days_left;
                return (
                  <button key={id} className="card-row" onClick={() => onOpen(id)}>
                    <div className="row between" style={{ flexWrap: "nowrap", alignItems: "flex-start" }}>
                      <div className="cell-model" style={{ minWidth: 0 }}><span className="mono">{e.model}</span><span className="muted">{e.provider}</span></div>
                      {f?.shutdown_date ? (
                        <div className={`days right ${e.risk === "critical" ? "crit" : e.risk === "high" ? "high" : ""}`}>
                          {d !== undefined && d >= 0 ? <><b>{d}</b> <span className="soft">{d === 1 ? "day" : "days"}</span></> : <b>Past</b>}
                        </div>
                      ) : null}
                    </div>
                    <div className="row" style={{ marginTop: 10 }}>
                      <StatusTag status={f?.status} risk={e.risk} />
                      {f?.shutdown_date ? <span className="muted" style={{ fontSize: 13 }}>{longDate(f.shutdown_date, f.date_precision)}</span> : null}
                    </div>
                    {e.traffic ? <div style={{ marginTop: 12 }}><ShareBar share={e.traffic.request_share} risk={atRisk(e)} /></div> : null}
                    <div className="row between" style={{ marginTop: 12 }}>
                      <div className="chips">{(e.traffic?.consumers ?? []).slice(0, 3).map((c) => <span className="chip" key={c.id}>{c.name}</span>)}</div>
                      <NextStep e={e} />
                    </div>
                  </button>
                );
              })}
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
