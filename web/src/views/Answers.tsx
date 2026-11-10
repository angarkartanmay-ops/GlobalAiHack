import { useState } from "react";
import { SearchCheck } from "lucide-react";
import { api, type FreshnessReport, type Status } from "../api";
import { ClaimTag, EvidenceItem, Notice, Spinner } from "../ui";

export function Answers({ status }: { status: Status | null }) {
  const [question, setQuestion] = useState("");
  const [answer, setAnswer] = useState("");
  const [rewrite, setRewrite] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [report, setReport] = useState<FreshnessReport | null>(null);
  const examples = status?.sim?.examples ?? [];
  const noWeb = status?.tavily && !status.tavily.configured;

  const run = async () => {
    setBusy(true); setError("");
    try { setReport(await api.freshness(question, answer, rewrite)); }
    catch (e) { setError(e instanceof Error ? e.message : String(e)); }
    setBusy(false);
  };

  const checked = report ? report.supported + report.changed + report.unverifiable : 0;

  return (
    <>
      <div className="page-head">
        <div>
          <h1 className="page-title">Answer check</h1>
          <p className="page-sub">
            Paste an answer a model gave. Heimdall finds the claims that can go out of date, checks each against the web as it is today,
            and shows which still hold, with the source text each verdict rests on.
          </p>
        </div>
      </div>

      {noWeb ? <div style={{ marginBottom: 18 }}><Notice kind="warn"><strong>Tavily is not configured.</strong> Answers cannot be checked against the web until it is. See Setup.</Notice></div> : null}

      <div className="check-grid">
        <div className="panel">
          <div className="panel-body" style={{ display: "flex", flexDirection: "column", gap: 16 }}>
            <div className="field">
              <label htmlFor="q">Question (optional)</label>
              <input id="q" className="input" value={question} onChange={(e) => setQuestion(e.target.value)} placeholder="What the model was asked" />
            </div>
            <div className="field">
              <label htmlFor="a">Answer to check</label>
              <textarea id="a" className="textarea" value={answer} onChange={(e) => setAnswer(e.target.value)} placeholder="Paste the model's answer" />
            </div>
            <label className="row" style={{ fontSize: 14, cursor: "pointer" }}>
              <input type="checkbox" checked={rewrite} onChange={(e) => setRewrite(e.target.checked)} style={{ accentColor: "var(--beacon)" }} />
              Also write a corrected answer with citations
            </label>
            <div className="row between">
              <button className="btn primary" onClick={run} disabled={busy || !answer.trim() || !!noWeb}>
                {busy ? <Spinner /> : <SearchCheck size={15} />} {busy ? "Checking" : "Check against the web"}
              </button>
            </div>
            {examples.length ? (
              <div>
                <div className="label" style={{ marginBottom: 8 }}>Examples</div>
                <div className="row">
                  {examples.map((ex) => (
                    <button key={ex.label} className="btn sm" onClick={() => { setQuestion(ex.question); setAnswer(ex.answer); setReport(null); }}>{ex.label}</button>
                  ))}
                </div>
                <p className="muted" style={{ fontSize: 13, marginTop: 10 }}>
                  In simulator mode only the bundled demo pages are searchable, so other text will come back as "could not verify".
                  With a Tavily key the same check runs against the live web.
                </p>
              </div>
            ) : null}
          </div>
        </div>

        <div className="stack">
          {error ? <Notice kind="bad">{error}</Notice> : null}
          {!report && !error ? (
            <div className="panel"><div className="empty">
              <h3>Nothing checked yet</h3>
              <p>Verdicts appear here claim by claim. A claim is only marked "still true" or "out of date" when the supporting quote is found word for word in a retrieved page.</p>
            </div></div>
          ) : null}
          {report ? (
            <>
              {report.error ? <Notice kind="warn"><strong>The check ran into a problem.</strong> {report.error}</Notice> : null}
              <div className="stats" style={{ marginTop: 0, gridTemplateColumns: "repeat(3, minmax(0, 1fr))" }}>
                <div className="stat"><small>Still true</small><b className={report.supported ? "good-num" : ""}>{report.supported}</b></div>
                <div className="stat"><small>Out of date</small><b className={report.changed ? "bad-num" : ""}>{report.changed}</b></div>
                <div className="stat"><small>Could not verify</small><b>{report.unverifiable}</b></div>
              </div>
              <div className="panel">
                <div className="panel-head">
                  <div className="panel-title">Claims</div>
                  <span className="muted" style={{ fontSize: 13 }}>{checked} checked with {report.tavily_searches} {report.tavily_searches === 1 ? "search" : "searches"}</span>
                </div>
                <div className="panel-body">
                  {report.claims.length === 0 ? <p className="soft">No factual claims were found in this answer.</p> : null}
                  {report.claims.map((c) => (
                    <div className="claim" key={c.id}>
                      <div className="row">
                        <ClaimTag verdict={c.checked ? c.verdict : undefined} />
                        {c.time_sensitive ? <span className="tag outline">{c.category}</span> : null}
                        {c.checked && c.confidence === "high" ? <span className="tag outline" title="A second, independent site states the same value">Two sources agree</span> : null}
                      </div>
                      <div className="claim-text">{c.text}</div>
                      {c.verdict === "changed" && c.current_value ? <div className="claim-now">Now: <b>{c.current_value}</b></div> : null}
                      {c.evidence.map((e) => <EvidenceItem key={e.id + (e.url ?? "")} ev={e} showApplies={false} />)}
                      {c.inference ? <p className="muted" style={{ fontSize: 13, marginTop: 10 }}>Analyst's reasoning: {c.inference}</p> : null}
                    </div>
                  ))}
                </div>
              </div>
              {report.rewrite ? (
                <div className="panel">
                  <div className="panel-head"><div className="panel-title">Corrected answer</div><span className="muted" style={{ fontSize: 13 }}>Only the facts that changed were edited</span></div>
                  <div className="panel-body"><div className="rewrite">{report.rewrite}</div></div>
                </div>
              ) : null}
              <p className="muted" style={{ fontSize: 13 }}>
                Evidence via Tavily{status?.tavily?.mode?.startsWith("relay") ? ", relayed through Bifrost" : ""}; read by{" "}
                <span className="mono">{report.analyst_model}</span>{status?.simulated ? " (simulated, rule-based)" : " through Bifrost"}. Took {(report.elapsed_ms / 1000).toFixed(1)} s.
              </p>
            </>
          ) : null}
        </div>
      </div>
    </>
  );
}
