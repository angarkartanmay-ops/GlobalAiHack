import { useEffect, useState } from "react";
import { ArrowLeft, ChevronRight, FlaskConical, Power, RotateCcw, ShieldCheck, Split, Undo2, Zap } from "lucide-react";
import { api, type Migration, type ModelDetail, type Status } from "../api";
import { ConfidenceTag, EvidenceItem, InferenceBlock, Notice, ShareBar, Spinner, StatusTag, VerdictTag, ago, longDate, money, num, pct, usePoll } from "../ui";

const stages = [
  { key: "none", name: "No rule", note: "Gateway routes as before" },
  { key: "armed", name: "Safety net", note: "Replacement is the fallback" },
  { key: "canary", name: "Canary", note: "A share of live traffic" },
  { key: "promoted", name: "Cut over", note: "Replacement serves all" },
];

function Cutover({ id, detail, status, replacement, onChanged, toast }: {
  id: string; detail: ModelDetail; status: Status | null; replacement: string; onChanged: () => void; toast: (msg: string, bad?: boolean) => void;
}) {
  const mig: Migration | null = detail.migration && detail.migration.stage !== "none" ? detail.migration : null;
  const stage = mig?.stage ?? "none";
  const [pct_, setPct] = useState(mig?.canary_pct || 10);
  const [busy, setBusy] = useState("");
  useEffect(() => { if (mig?.canary_pct) setPct(mig.canary_pct); }, [mig?.canary_pct]);

  const target = mig?.replacement || replacement;
  const idx = stages.findIndex((s) => s.key === stage);
  const simRetired = (status?.sim?.retired ?? []).includes(id);

  const apply = async (next: string, p = 0) => {
    setBusy(next);
    try {
      await api.migrate(id, target, next, p);
      toast(next === "none" ? "Routing rule removed from the gateway." : "Routing rule updated on the gateway.");
      onChanged();
    } catch (e) { toast(e instanceof Error ? e.message : String(e), true); }
    setBusy("");
  };

  const retire = async (revive: boolean) => {
    setBusy("sim");
    try { await api.simRetire(id, revive); onChanged(); } catch (e) { toast(e instanceof Error ? e.message : String(e), true); }
    setBusy("");
  };

  const h = mig?.health;
  return (
    <div className="panel">
      <div className="panel-head">
        <div className="panel-title">Cutover</div>
        {mig?.rule_name ? <span className="muted mono" style={{ fontSize: 12 }}>{mig.rule_name}</span> : null}
      </div>
      <div className="panel-body">
        <div className="stepper" aria-label="Migration stage">
          {stages.map((s, i) => (
            <div key={s.key} className={`step ${i < idx ? "done" : ""} ${i === idx ? "now" : ""}`}>
              <b>{s.key === "canary" && stage === "canary" ? `Canary ${mig?.canary_pct}%` : s.name}</b>
              <span>{s.note}</span>
            </div>
          ))}
        </div>

        {!target ? (
          <Notice>Choose a replacement under <strong>Replacement</strong> to enable cutover.</Notice>
        ) : (
          <>
            <p className="soft" style={{ fontSize: 14, marginBottom: 14 }}>
              {stage === "none" && <>Each step is one Bifrost routing rule matched on <span className="mono">{id}</span>, sending traffic to <span className="mono">{target}</span>. Removing it restores the gateway exactly.</>}
              {stage === "armed" && <>Requests still go to <span className="mono">{id}</span>. If it fails, the gateway retries on <span className="mono">{target}</span>.</>}
              {stage === "canary" && <>{mig?.canary_pct}% of requests for <span className="mono">{id}</span> are served by <span className="mono">{target}</span>. Each side falls back to the other.</>}
              {stage === "promoted" && <>All requests for <span className="mono">{id}</span> are served by <span className="mono">{target}</span>. Applications need no change.</>}
            </p>

            {mig?.guard_note ? <div style={{ marginBottom: 14 }}><Notice kind="bad"><strong>Canary guard.</strong> {mig.guard_note}</Notice></div> : null}

            {stage !== "promoted" ? (
              <div className="range" style={{ marginBottom: 14 }}>
                <span className="label">Canary share</span>
                <input type="range" min={1} max={90} value={pct_} onChange={(e) => setPct(Number(e.target.value))} aria-label="Canary percentage" />
                <output>{pct_}%</output>
              </div>
            ) : null}

            <div className="row">
              {stage === "none" ? (
                <button className="btn" disabled={!!busy} onClick={() => apply("armed")}>{busy === "armed" ? <Spinner /> : <ShieldCheck size={15} />} Arm safety net</button>
              ) : null}
              {stage !== "promoted" ? (
                <button className="btn" disabled={!!busy} onClick={() => apply("canary", pct_)}>
                  {busy === "canary" ? <Spinner /> : <Split size={15} />} {stage === "canary" ? `Set canary to ${pct_}%` : `Start canary at ${pct_}%`}
                </button>
              ) : null}
              {stage !== "promoted" ? (
                <button className="btn primary" disabled={!!busy} onClick={() => apply("promoted")}>{busy === "promoted" ? <Spinner /> : <Zap size={15} />} Cut over</button>
              ) : null}
              {stage !== "none" ? (
                <button className="btn danger" disabled={!!busy} onClick={() => apply("none")}>{busy === "none" ? <Spinner /> : <Undo2 size={15} />} Remove rule</button>
              ) : null}
            </div>

            {h && (h.old_requests > 0 || h.new_requests > 0) ? (
              <>
                <div className="health">
                  <div>
                    <span className="label">Current model</span>
                    <b>{h.old_requests ? `${Math.round(h.old_success_rate)}%` : "—"}</b>
                    <small>{num(h.old_requests)} requests · {h.old_requests ? `${Math.round(h.old_latency_ms)} ms` : "no traffic"}</small>
                  </div>
                  <div>
                    <span className="label">Replacement</span>
                    <b>{h.new_requests ? `${Math.round(h.new_success_rate)}%` : "—"}</b>
                    <small>{num(h.new_requests)} requests · {h.new_requests ? `${Math.round(h.new_latency_ms)} ms` : "no traffic"}</small>
                  </div>
                </div>
                <p className="muted" style={{ fontSize: 12.5, marginTop: 8 }}>
                  Success rate of live requests this rule has routed since {ago(h.since) || "it started"}, from gateway logs.
                  {h.fallbacks ? ` ${h.fallbacks} served by a fallback.` : ""}
                </p>
              </>
            ) : stage !== "none" ? (
              <p className="muted" style={{ fontSize: 12.5, marginTop: 12 }}>Waiting for live requests to pass through this rule.</p>
            ) : null}
          </>
        )}

        {status?.simulated ? (
          <div style={{ marginTop: 18, paddingTop: 16, borderTop: "1px solid var(--line-soft)" }}>
            <div className="row between">
              <div className="grow">
                <div style={{ fontWeight: 500, fontSize: 14 }}>Simulate the shutdown</div>
                <div className="muted" style={{ fontSize: 13 }}>
                  {simRetired ? "The simulated provider is returning 404 for this model." : "Make the simulated provider retire this model now, to see what applications get."}
                </div>
              </div>
              <button className="btn sm" disabled={!!busy} onClick={() => retire(simRetired)}>
                {busy === "sim" ? <Spinner size={14} /> : simRetired ? <RotateCcw size={14} /> : <Power size={14} />} {simRetired ? "Bring it back" : "Shut it down"}
              </button>
            </div>
          </div>
        ) : null}
      </div>
    </div>
  );
}

export function Model({ id, status, onBack, onOpenRehearsal, toast }: {
  id: string; status: Status | null; onBack: () => void; onOpenRehearsal: (rid: string) => void; toast: (msg: string, bad?: boolean) => void;
}) {
  const { data, error, loading, refresh } = usePoll(() => api.model(id), 5000, [id]);
  const [candidate, setCandidate] = useState("");
  const [samples, setSamples] = useState(status?.settings?.rehearsal_samples ?? 25);
  const [starting, setStarting] = useState(false);

  useEffect(() => { setCandidate(""); }, [id]);
  useEffect(() => {
    if (!candidate && data?.candidates.length) setCandidate(data.migration?.replacement || data.candidates[0].id);
  }, [data, candidate]);

  if (loading && !data) return <div className="skeleton" style={{ height: 240 }} />;
  if (!data) return <Notice kind="bad">{error || "This model could not be loaded."}</Notice>;

  const ex = data.exposure;
  const f = ex.finding;
  const t = ex.traffic;
  const d = ex.days_left;
  const direct = (f?.evidence ?? []).filter((e) => e.applies !== "family");
  const related = (f?.evidence ?? []).filter((e) => e.applies === "family");
  const running = data.rehearsals.find((r) => r.status === "running");
  const win = status?.settings?.lookback_hours;

  const rehearse = async () => {
    if (!candidate) return;
    setStarting(true);
    try {
      const { id: rid } = await api.startRehearsal(id, candidate, samples);
      onOpenRehearsal(rid);
    } catch (e) { toast(e instanceof Error ? e.message : String(e), true); }
    setStarting(false);
  };

  return (
    <>
      <button className="back" onClick={onBack}><ArrowLeft size={15} /> The watch</button>

      <div className="hero">
        <div style={{ minWidth: 0 }}>
          <div className="hero-id">{id}</div>
          <div className="hero-meta">
            <StatusTag status={f?.status} risk={ex.risk} />
            {f && (f.status === "deprecated" || f.status === "retired") ? <ConfidenceTag level={f.confidence} /> : null}
            {f?.last_verified ? <span className="muted" style={{ fontSize: 13 }}>Checked {ago(f.last_verified)}</span> : <span className="muted" style={{ fontSize: 13 }}>Not checked yet</span>}
          </div>
        </div>
        {f?.shutdown_date ? (
          <div className={`countdown ${ex.risk === "critical" ? "crit" : ex.risk === "high" ? "high" : ""}`}>
            <b>{d !== undefined && d >= 0 ? d : "Past"}</b>
            <span>
              {d !== undefined && d >= 0 ? `${d === 1 ? "day" : "days"} until shutdown on ` : "Shut down on "}
              {longDate(f.shutdown_date, f.date_precision)}
            </span>
          </div>
        ) : null}
      </div>

      {f?.error ? <div style={{ marginBottom: 20 }}><Notice kind="warn"><strong>The last check did not complete.</strong> {f.error}</Notice></div> : null}
      {f?.conflict ? (
        <div style={{ marginBottom: 20 }}>
          <Notice kind="warn"><strong>Sources disagree on the date.</strong> {f.conflict.summary} Dates seen: {f.conflict.dates.map((x) => longDate(x)).join(", ")}.</Notice>
        </div>
      ) : null}

      <div className="detail-grid">
        <div className="stack">
          <div className="panel">
            <div className="panel-head">
              <div className="panel-title">Evidence</div>
              <span className="muted" style={{ fontSize: 13 }}>Quoted word for word from retrieved sources</span>
            </div>
            <div className="panel-body">
              {direct.length === 0 ? (
                <p className="soft">
                  {!f ? "This model has not been checked yet. Run a scan from the watch." :
                    f.status === "unknown" ? "No evidence could be retrieved for this model." :
                      "No retrieved source states a retirement for this model."}
                </p>
              ) : direct.map((e) => <EvidenceItem key={e.id} ev={e} />)}
              {related.length ? (
                <div style={{ marginTop: 22 }}>
                  <div className="label" style={{ marginBottom: 10 }}>Notices about related models</div>
                  {related.map((e) => <EvidenceItem key={e.id} ev={e} />)}
                </div>
              ) : null}
              {f?.discarded_ungrounded ? (
                <div style={{ marginTop: 20 }}>
                  <Notice>
                    <strong>{f.discarded_ungrounded} analyst {f.discarded_ungrounded === 1 ? "notice was" : "notices were"} thrown out.</strong>{" "}
                    The quote given could not be found in the retrieved text, so Heimdall did not use it.
                  </Notice>
                </div>
              ) : null}
              {f?.inference.length ? <div style={{ marginTop: 20 }}><InferenceBlock items={f.inference} /></div> : null}
            </div>
          </div>

          <div className="panel">
            <div className="panel-head">
              <div className="panel-title">Replacement</div>
              {f?.replacement ? (
                <span className="muted" style={{ fontSize: 13 }}>
                  Provider names <span className="mono">{f.replacement}</span>{!f.replacement_on_gateway ? " (not served by this gateway)" : ""}
                </span>
              ) : null}
            </div>
            <div className="panel-body">
              {!t ? (
                <p className="soft">There are no recent requests to this model to replay.</p>
              ) : data.candidates.length === 0 ? (
                <p className="soft">The gateway serves no other model that could replace this one. Add a provider key to the gateway first.</p>
              ) : (
                <>
                  <p className="soft" style={{ fontSize: 14, marginBottom: 16 }}>
                    A rehearsal replays real requests from the gateway's logs against the candidate and compares the answers.
                    Production is not touched; replays run under Heimdall's own budgeted virtual key.
                  </p>
                  <div className="row gap-lg" style={{ alignItems: "flex-end" }}>
                    <div className="field grow" style={{ minWidth: 220 }}>
                      <label htmlFor="cand">Candidate</label>
                      <select id="cand" className="select mono" value={candidate} onChange={(e) => setCandidate(e.target.value)}>
                        {data.candidates.map((c) => <option key={c.id} value={c.id}>{c.id} · {c.reason}</option>)}
                      </select>
                    </div>
                    <div className="field" style={{ width: 110 }}>
                      <label htmlFor="n">Requests</label>
                      <input id="n" className="input tabular" type="number" min={5} max={200} value={samples}
                        onChange={(e) => setSamples(Math.max(5, Math.min(200, Number(e.target.value) || 25)))} />
                    </div>
                    <button className="btn primary" disabled={starting || !!running || !candidate} onClick={rehearse}>
                      {starting || running ? <Spinner /> : <FlaskConical size={15} />} {running ? "Rehearsing" : "Rehearse"}
                    </button>
                  </div>
                </>
              )}

              {data.rehearsals.length ? (
                <div style={{ marginTop: 22 }}>
                  <div className="label" style={{ marginBottom: 10 }}>Rehearsals</div>
                  {data.rehearsals.map((r) => (
                    <button key={r.id} className="list-row" onClick={() => onOpenRehearsal(r.id)}>
                      <div style={{ minWidth: 0 }}>
                        <div className="list-main mono">{r.candidate}</div>
                        <div className="list-sub">
                          {r.status === "running" ? `Running: ${r.settled} of ${r.sampled || r.requested} compared` :
                            r.status === "failed" ? r.error : `${r.settled} requests · ${ago(r.finished_at)}`}
                        </div>
                      </div>
                      <div className="row" style={{ flexWrap: "nowrap" }}>
                        {r.status === "done" ? <><span className="tabular soft">{Math.round(r.score)}%</span><VerdictTag verdict={r.verdict} /></> :
                          r.status === "running" ? <Spinner /> : <span className="tag critical">Failed</span>}
                        <ChevronRight size={16} className="muted" />
                      </div>
                    </button>
                  ))}
                </div>
              ) : null}
            </div>
          </div>
        </div>

        <div className="stack">
          <Cutover id={id} detail={data} status={status} replacement={candidate} onChanged={refresh} toast={toast} />

          <div className="panel">
            <div className="panel-head">
              <div className="panel-title">Traffic</div>
              <span className="muted" style={{ fontSize: 13 }}>{win ? `Last ${win >= 48 ? `${Math.round(win / 24)} days` : `${Math.round(win)} hours`}` : ""}</span>
            </div>
            <div className="panel-body">
              {t ? (
                <>
                  <dl className="kv">
                    <dt>Requests</dt><dd>{num(t.requests)}</dd>
                    <dt>Share of gateway requests</dt><dd>{pct(t.request_share)}</dd>
                    <dt>Success rate</dt><dd className={t.success_rate < 90 ? "bad-num" : ""}>{Math.round(t.success_rate)}%</dd>
                    <dt>Spend</dt><dd>{money(t.cost)}</dd>
                    <dt>Average latency</dt><dd>{Math.round(t.avg_latency_ms)} ms</dd>
                  </dl>
                  {t.consumers?.length ? (
                    <div style={{ marginTop: 20 }}>
                      <div className="label" style={{ marginBottom: 12 }}>Applications that depend on it</div>
                      {t.consumers.map((c) => (
                        <div className="consumer-row" key={c.id}>
                          <span>{c.name}</span>
                          <ShareBar share={c.requests / Math.max(1, t.requests)} />
                          <span className="muted tabular">{num(c.requests)}</span>
                        </div>
                      ))}
                    </div>
                  ) : null}
                </>
              ) : (
                <p className="soft">No requests reach this model any more{data.migration?.stage === "promoted" ? ": its traffic is being served by the replacement." : "."}</p>
              )}
            </div>
          </div>

          {f?.changes?.length ? (
            <div className="panel">
              <div className="panel-head"><div className="panel-title">What changed between scans</div></div>
              <div className="panel-body">
                {f.changes.slice().reverse().map((c, i) => (
                  <div key={i} style={{ fontSize: 14, marginTop: i ? 10 : 0 }}>
                    <span className="soft">{c.field.replace("_", " ")}</span>: {c.from || "none"} → <strong>{c.to || "none"}</strong>
                    <span className="muted"> · {ago(c.at)}</span>
                  </div>
                ))}
              </div>
            </div>
          ) : null}
        </div>
      </div>
    </>
  );
}
