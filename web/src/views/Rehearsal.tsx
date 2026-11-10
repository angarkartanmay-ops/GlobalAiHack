import { useState } from "react";
import { ArrowLeft, Check, ChevronDown, ChevronRight, Scale, X } from "lucide-react";
import { api, type Output, type Rehearsal as Reh, type Sample } from "../api";
import { EvidenceItem, Notice, OutcomeTag, Ring, Spinner, money, signed, usePoll, verdictTitle } from "../ui";

const order = ["equivalent", "better", "acceptable", "regression", "error"];
const outcomeLabel: Record<string, string> = { equivalent: "Equivalent", better: "Better", acceptable: "Acceptable difference", regression: "Regression", error: "Failed to answer" };
const checkLabel: Record<string, string> = {
  completed: "Answered", tool_calls: "Tool calls match", json_output: "JSON output intact", not_truncated: "Not truncated", no_refusal: "No new refusal", length: "Similar length",
};
const checkFailLabel: Record<string, string> = {
  completed: "did not answer", tool_calls: "changed a tool call", json_output: "broke JSON output", not_truncated: "were cut off", no_refusal: "refused", length: "differed greatly in length",
};

function render(o: Output): string {
  if (o.error) return o.error;
  const calls = (o.tool_calls ?? []).map((c) => `→ ${c.name}(${c.arguments})`).join("\n");
  return [o.content, calls].filter(Boolean).join("\n") || "(empty)";
}

function SampleRow({ s }: { s: Sample }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="sample">
      <button className="sample-head" onClick={() => setOpen(!open)} aria-expanded={open}>
        <OutcomeTag outcome={s.outcome} />
        <div style={{ minWidth: 0 }}>
          <div className="sample-prompt">{s.prompt || "(no user message)"}</div>
          <div className="sample-reason">{s.judge_reason}</div>
        </div>
        <span className="muted" style={{ fontSize: 12.5 }}>{s.consumer}</span>
        {open ? <ChevronDown size={16} className="muted" /> : <ChevronRight size={16} className="muted" />}
      </button>
      {open ? (
        <div className="sample-body">
          {s.judge_reason ? <p className="soft" style={{ fontSize: 14 }}>{s.judge_reason}</p> : null}
          <div className="compare">
            <div className="out">
              <div className="out-head"><span>In production today</span><span className="tabular">{Math.round(s.baseline.latency_ms)} ms · {money(s.baseline.cost)}</span></div>
              <pre>{render(s.baseline)}</pre>
            </div>
            <div className="out">
              <div className="out-head"><span>Candidate</span><span className="tabular">{s.candidate.error ? "failed" : `${Math.round(s.candidate.latency_ms)} ms · ${money(s.candidate.cost)}`}</span></div>
              <pre>{render(s.candidate)}</pre>
            </div>
          </div>
          <div className="checks">
            {s.checks.map((c) => (
              <span key={c.name} className={`tag ${c.pass ? "outline" : "critical"}`} title={c.detail}>
                {c.pass ? <Check size={12} /> : <X size={12} />} {checkLabel[c.name] ?? c.name}
              </span>
            ))}
          </div>
          {(s.tiebreaks ?? []).map((tb, i) => (
            <div className="tiebreak" key={i}>
              <div className="tiebreak-title">
                <Scale size={14} /> Settled by live evidence:{" "}
                {tb.winner === "candidate" ? "the candidate is current" : tb.winner === "baseline" ? "the candidate is out of date" : "neither answer could be confirmed"}
              </div>
              <div className="tiebreak-claims">
                <div><span className="label">Production says</span><div>{tb.baseline_claim}</div>
                  <span className={`tag ${tb.baseline_verdict === "supported" ? "ok" : tb.baseline_verdict === "changed" ? "critical" : ""}`} style={{ marginTop: 6 }}>{tb.baseline_verdict === "supported" ? "Still true" : tb.baseline_verdict === "changed" ? "Out of date" : "Could not verify"}</span></div>
                <div><span className="label">Candidate says</span><div>{tb.candidate_claim}</div>
                  <span className={`tag ${tb.candidate_verdict === "supported" ? "ok" : tb.candidate_verdict === "changed" ? "critical" : ""}`} style={{ marginTop: 6 }}>{tb.candidate_verdict === "supported" ? "Still true" : tb.candidate_verdict === "changed" ? "Out of date" : "Could not verify"}</span></div>
              </div>
              {tb.evidence.map((e) => <EvidenceItem key={e.id + e.url} ev={e} showApplies={false} />)}
            </div>
          ))}
        </div>
      ) : null}
    </div>
  );
}

export function Rehearsal({ id, onBack, onOpenModel }: { id: string; onBack: () => void; onOpenModel: (id: string) => void }) {
  const [filter, setFilter] = useState("all");
  const [done, setDone] = useState(false);
  const { data, error, loading } = usePoll<Reh>(async () => {
    const r = await api.rehearsal(id);
    if (r.status !== "running") setDone(true);
    return r;
  }, done ? 0 : 1200, [id, done]);

  if (loading && !data) return <div className="skeleton" style={{ height: 220 }} />;
  if (!data) return <Notice kind="bad">{error || "This rehearsal could not be loaded."}</Notice>;
  const r = data;
  const total = r.samples.length;
  const fails = (r.outcomes.regression ?? 0) + (r.outcomes.error ?? 0);
  const tone = r.verdict === "ready" ? "ok" : r.verdict === "ready_with_caveats" ? "warn" : "bad";
  const shown = r.samples.filter((s) =>
    filter === "all" ? true : filter === "problems" ? s.outcome === "regression" || s.outcome === "error" : filter === "evidence" ? (s.tiebreaks?.length ?? 0) > 0 : s.outcome === filter);
  const failures = Object.entries(r.check_failures).filter(([k]) => k !== "length").sort((a, b) => b[1] - a[1]);
  const evidenceCount = r.samples.filter((s) => s.tiebreaks?.length).length;

  return (
    <>
      <button className="back" onClick={onBack}><ArrowLeft size={15} /> Back</button>
      <div className="page-head">
        <div style={{ minWidth: 0 }}>
          <h1 className="page-title">Rehearsal</h1>
          <p className="page-sub">
            Real requests to <button className="btn ghost sm mono" style={{ padding: "0 4px", height: 24 }} onClick={() => onOpenModel(r.baseline)}>{r.baseline}</button>,
            replayed against <span className="mono">{r.candidate}</span>.
          </p>
        </div>
      </div>

      {r.status === "failed" ? (
        <Notice kind="bad"><strong>The rehearsal could not run.</strong> {r.error}</Notice>
      ) : r.status === "running" && total === 0 ? (
        <div className="panel"><div className="panel-body">
          <div className="row"><Spinner /> <span>{r.sampled ? `Replaying ${r.sampled} requests against the candidate (${r.completed} done)` : "Collecting recent requests from the gateway's logs"}</span></div>
          <div className="progress"><i style={{ width: `${r.sampled ? (100 * r.completed) / r.sampled : 4}%` }} /></div>
        </div></div>
      ) : (
        <>
          <div className="verdict">
            <Ring score={r.score} tone={tone} />
            <div>
              <h2>{r.status === "running" ? `Comparing answers: ${total} of ${r.sampled}` : verdictTitle(r.verdict)}</h2>
              <p>{r.summary}</p>
              {r.stopped_early ? <p style={{ color: "var(--beacon)" }}>{r.stopped_early}</p> : null}
              {r.status === "running" ? <div className="progress" style={{ maxWidth: 420 }}><i style={{ width: `${(100 * total) / Math.max(1, r.sampled)}%` }} /></div> : null}
            </div>
          </div>

          <div className="stats">
            <div className="stat"><small>Requests replayed</small><b>{total}</b><small>{fails} regressed</small></div>
            <div className="stat"><small>Cost on this traffic</small>
              <b className={r.cost_delta_pct < 0 ? "good-num" : r.cost_delta_pct > 5 ? "bad-num" : ""}>{r.baseline_cost > 0 ? signed(r.cost_delta_pct) : "—"}</b>
              <small>{money(r.baseline_cost)} → {money(r.candidate_cost)}</small></div>
            <div className="stat"><small>Median latency</small>
              <b className={r.latency_delta_pct < 0 ? "good-num" : r.latency_delta_pct > 15 ? "bad-num" : ""}>{r.baseline_latency_p50_ms > 0 ? signed(r.latency_delta_pct) : "—"}</b>
              <small>{Math.round(r.baseline_latency_p50_ms)} ms → {Math.round(r.candidate_latency_p50_ms)} ms</small></div>
            <div className="stat"><small>Facts checked against the web</small>
              <b>{r.candidate_fresher + r.candidate_staler}</b>
              <small>{r.candidate_fresher} more current · {r.candidate_staler} out of date</small></div>
          </div>

          <div className="section">
            <div className="dist" role="img" aria-label="Outcome distribution">
              {order.map((k) => (r.outcomes[k] ? <i key={k} className={`c-${k}`} style={{ width: `${(100 * r.outcomes[k]) / Math.max(1, total)}%` }} /> : null))}
            </div>
            <div className="legend">
              {order.map((k) => (r.outcomes[k] ? <span key={k}><i className={`c-${k}`} /> {r.outcomes[k]} {outcomeLabel[k].toLowerCase()}</span> : null))}
            </div>
            {failures.length ? (
              <p className="soft" style={{ marginTop: 14, fontSize: 14 }}>
                What broke: {failures.map(([k, n]) => `${n} ${checkFailLabel[k] ?? k}`).join(", ")}.
                These are caught by parsing the output, not by asking a model.
              </p>
            ) : null}
          </div>

          <div className="section">
            <div className="section-head">
              <div><h2 className="section-title">Requests</h2><p className="section-note">Sampled across applications from the gateway's logs. Open one to compare the two answers.</p></div>
              <div className="filters" role="group" aria-label="Filter requests">
                {[["all", `All ${total}`], ["problems", `Regressions ${fails}`], ["better", `Better ${r.outcomes.better ?? 0}`], ["evidence", `Settled by evidence ${evidenceCount}`]].map(([k, label]) => (
                  <button key={k} className="filter" aria-pressed={filter === k} onClick={() => setFilter(k)}>{label}</button>
                ))}
              </div>
            </div>
            <div className="panel">
              {shown.length ? shown.map((s) => <SampleRow key={s.log_id} s={s} />) : <div className="empty" style={{ padding: 36 }}><p>No requests match this filter.</p></div>}
            </div>
            <p className="muted" style={{ fontSize: 13, marginTop: 12 }}>
              This rehearsal spent {money(r.spend_usd)} of its {money(r.budget_usd)} budget{r.judge_model ? <>, judged by <span className="mono">{r.judge_model}</span></> : null}.
            </p>
          </div>
        </>
      )}
    </>
  );
}
