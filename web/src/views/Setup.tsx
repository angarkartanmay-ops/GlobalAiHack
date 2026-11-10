import { useEffect, useState } from "react";
import { CircleAlert, CircleCheck, CircleDashed, Send } from "lucide-react";
import { api, type EventItem, type Status } from "../api";
import { Notice, Spinner, ago, usePoll } from "../ui";

function Row({ state, title, children }: { state: "ok" | "bad" | "warn" | "off"; title: string; children: React.ReactNode }) {
  const Icon = state === "ok" ? CircleCheck : state === "off" ? CircleDashed : CircleAlert;
  return (
    <div className={`check-row ${state}`}>
      <Icon size={18} className={state === "off" ? "muted" : undefined} />
      <div><div className="check-title">{title}</div><div className="check-note">{children}</div></div>
    </div>
  );
}

function SampleTraffic({ toast }: { toast: (m: string, bad?: boolean) => void }) {
  const [models, setModels] = useState<string[]>([]);
  const [pick, setPick] = useState({ large: "", small: "", other: "", legacy: "" });
  const [busy, setBusy] = useState(false);
  useEffect(() => { api.gatewayModels().then((r) => setModels(r.models)).catch(() => setModels([])); }, []);

  const send = async () => {
    setBusy(true);
    try {
      const r = await api.traffic(pick);
      toast(`Sending ${r.requests} sample requests through the gateway. Run a scan when they finish.`);
    } catch (e) { toast(e instanceof Error ? e.message : String(e), true); }
    setBusy(false);
  };
  const select = (key: keyof typeof pick, label: string, hint: string, required = false) => (
    <div className="field grow" style={{ minWidth: 200 }}>
      <label htmlFor={`m-${key}`}>{label}</label>
      <select id={`m-${key}`} className="select mono" value={pick[key]} onChange={(e) => setPick({ ...pick, [key]: e.target.value })}>
        <option value="">{required ? "Choose a model" : "Skip"}</option>
        {models.map((m) => <option key={m} value={m}>{m}</option>)}
      </select>
      <span className="muted" style={{ fontSize: 12.5 }}>{hint}</span>
    </div>
  );
  return (
    <div className="panel">
      <div className="panel-head"><div className="panel-title">Send sample traffic</div></div>
      <div className="panel-body">
        <p className="soft" style={{ fontSize: 14, marginBottom: 16 }}>
          A fresh gateway has no traffic to inventory. This sends about a hundred realistic requests (support answers, JSON extraction,
          tool calls) under four sample applications' virtual keys, to the models you choose. They are real requests and are billed by the provider.
        </p>
        <div className="row gap-lg" style={{ alignItems: "flex-start" }}>
          {select("large", "Main model", "Support, extraction and tool calls", true)}
          {select("small", "Small model", "High-volume query rewriting")}
          {select("other", "Second provider", "A share of support questions")}
          {select("legacy", "Old model", "A forgotten batch job")}
        </div>
        <div style={{ marginTop: 16 }}>
          <button className="btn primary" disabled={busy || !pick.large} onClick={send}>{busy ? <Spinner /> : <Send size={15} />} Send sample traffic</button>
        </div>
      </div>
    </div>
  );
}

export function Setup({ status, toast }: { status: Status | null; toast: (m: string, bad?: boolean) => void }) {
  if (!status) return <div className="skeleton" style={{ height: 240 }} />;
  const gw = status.gateway;
  const tav = status.tavily;
  const an = status.analyst;
  const last = status.scan?.last;
  const set = status.settings;
  const origin = typeof window !== "undefined" ? window.location.origin : "";

  return (
    <>
      <div className="page-head">
        <div>
          <h1 className="page-title">Setup</h1>
          <p className="page-sub">
            {status.simulated
              ? "Heimdall is running against a real Bifrost gateway with simulated providers and a simulated web. Nothing here needs a key."
              : "Heimdall is connected to your gateway. Everything it sends goes through Bifrost under one virtual key."}
          </p>
        </div>
      </div>

      {(status.config_problems ?? []).map((p, i) => <div key={i} style={{ marginBottom: 12 }}><Notice kind="bad">{p}</Notice></div>)}

      <div className="detail-grid">
        <div className="stack">
          <div className="panel">
            <div className="panel-head"><div className="panel-title">Connections</div><span className="muted mono" style={{ fontSize: 12 }}>v{status.version}</span></div>
            <div className="panel-body" style={{ paddingTop: 6, paddingBottom: 6 }}>
              <div className="checklist">
                <Row state={gw?.healthy ? "ok" : "bad"} title="Bifrost gateway">
                  {gw?.healthy ? <>Reachable at <span className="mono">{gw.url}</span></> : gw?.error ?? "Not reachable"}
                </Row>
                <Row state={gw?.admin ? "ok" : "bad"} title="Logs and routing rules">
                  {gw?.admin ? "The gateway's admin API accepts Heimdall's credentials. It reads log aggregates and writes only routing rules named heimdall-…" :
                    gw?.healthy ? gw.error ?? "The admin API refused Heimdall's credentials." : "Waiting for the gateway."}
                </Row>
                <Row state={gw?.virtual_key ? "ok" : "bad"} title="Virtual key">
                  {gw?.virtual_key ? `Heimdall's key can reach ${gw.models ?? 0} models. Its spend is capped by a budget on the gateway.` :
                    gw?.healthy ? gw.error ?? "The virtual key was refused." : "Waiting for the gateway."}
                </Row>
                <Row state={!tav?.configured ? "bad" : tav.last_error ? "warn" : "ok"} title="Tavily">
                  {!tav?.configured ? "Not configured. Without it the radar can only use dates that provider APIs report." :
                    <>
                      {tav.mode?.startsWith("relay") ? <>Through the Bifrost relay <span className="mono">{tav.mode.slice(6)}</span>: the gateway holds the key and logs every call.</> : "Called directly with TAVILY_API_KEY."}
                      {status.simulated ? " In this mode the relay points at the simulated web." : ""}
                      {" "}{tav.searches ?? 0} searches and {tav.extracts ?? 0} page reads since start.
                      {tav.last_error ? <> Last error: {tav.last_error}</> : null}
                    </>}
                </Row>
                <Row state={an?.model ? "ok" : "warn"} title="Analyst model">
                  {an?.model ? <>
                    <span className="mono">{an.model}</span>
                    {status.simulated ? " is a rule-based stand-in, not a language model. In live mode a real model reads the evidence." :
                      an.auto ? " was chosen from the gateway's model list, so it cannot be a retired name. Set HEIMDALL_ANALYST_MODEL to choose another." : " (set by HEIMDALL_ANALYST_MODEL)."}
                    {" "}{an.calls} calls since start.
                  </> : "Will be chosen from the gateway's model list on the first scan."}
                </Row>
                <Row state={status.simulated ? "off" : status.auth_required ? "ok" : "warn"} title="Dashboard access">
                  {status.simulated ? "Open: in simulator mode there is nothing real behind the dashboard." :
                    status.auth_required ? "Protected by HEIMDALL_ADMIN_TOKEN." : "Open to anyone who can reach it (HEIMDALL_ALLOW_ANONYMOUS). Set HEIMDALL_ADMIN_TOKEN before exposing it."}
                </Row>
                <Row state={set?.alerts ? "ok" : "off"} title="Alerts">
                  {set?.alerts ? "New notices, moved dates and canary failures are posted to the webhook." : "Optional. Set HEIMDALL_ALERT_WEBHOOK_URL to a Slack-compatible webhook."}
                </Row>
              </div>
            </div>
          </div>

          {!status.simulated && set?.traffic_keys ? <SampleTraffic toast={toast} /> : null}

          {status.simulated ? (
            <div className="panel">
              <div className="panel-head"><div className="panel-title">Go live</div></div>
              <div className="panel-body">
                <p className="soft" style={{ fontSize: 14, marginBottom: 14 }}>
                  Set these on the deployment and restart. Heimdall regenerates the gateway's configuration for real providers and the real Tavily API;
                  the simulator is switched off.
                </p>
                <table className="env">
                  <tbody>
                    <tr><td><code>OPENAI_API_KEY</code></td><td className="soft">Or <code>ANTHROPIC_API_KEY</code>, <code>GEMINI_API_KEY</code>, <code>GROQ_API_KEY</code>, <code>MISTRAL_API_KEY</code>, <code>OPENROUTER_API_KEY</code>. At least one; several is better, so rehearsals can cross providers.</td></tr>
                    <tr><td><code>TAVILY_API_KEY</code></td><td className="soft">Registered on the gateway as a relay. Heimdall reaches Tavily only through Bifrost.</td></tr>
                    <tr><td><code>HEIMDALL_ADMIN_TOKEN</code></td><td className="soft">Password for this dashboard. Required in live mode.</td></tr>
                    <tr><td><code>HEIMDALL_ALERT_WEBHOOK_URL</code></td><td className="soft">Optional. Slack-compatible webhook for alerts.</td></tr>
                  </tbody>
                </table>
              </div>
            </div>
          ) : null}
        </div>

        <div className="stack">
          <div className="panel">
            <div className="panel-head"><div className="panel-title">Scanning</div></div>
            <div className="panel-body">
              <dl className="kv">
                <dt>Traffic window</dt><dd>{set ? (set.lookback_hours >= 48 ? `${Math.round(set.lookback_hours / 24)} days` : `${Math.round(set.lookback_hours)} hours`) : "—"}</dd>
                <dt>Scan every</dt><dd>{status.scan?.interval_hours ? `${status.scan.interval_hours} hours` : "Manual only"}</dd>
                <dt>Search beyond official docs</dt><dd>{set?.open_web ? "Yes, as corroboration" : "No"}</dd>
                <dt>Rehearsal size</dt><dd>{set?.rehearsal_samples} requests</dd>
                <dt>Rehearsal budget</dt><dd>${set?.rehearsal_budget_usd.toFixed(2)}</dd>
                <dt>Roll back a failing canary</dt><dd>{set?.auto_rollback ? "Automatically" : "Alert only"}</dd>
              </dl>
              {last ? (
                <p className="muted" style={{ fontSize: 13, marginTop: 16 }}>
                  Last scan {ago(last.finished_at) || "is running"}: {last.models} models, {last.tavily_searches} searches, {last.tavily_extracts} page reads, {last.llm_calls} analyst calls.
                </p>
              ) : null}
            </div>
          </div>

          <div className="panel">
            <div className="panel-head"><div className="panel-title">Use Heimdall from an agent</div></div>
            <div className="panel-body">
              <p className="soft" style={{ fontSize: 14, marginBottom: 12 }}>
                Heimdall is also an MCP server. Register it in Bifrost and every agent behind the gateway can ask which models are being retired,
                or check an answer for stale facts.
              </p>
              <pre className="code">{`"mcp": { "client_configs": [{
  "name": "heimdall",
  "connection_type": "http",
  "connection_string": "${origin}/mcp",
  "tools_to_execute": ["*"]
}]}`}</pre>
              <p className="muted" style={{ fontSize: 13, marginTop: 10 }}>
                Tools: <span className="mono">heimdall_list_sunsets</span>, <span className="mono">heimdall_check_model</span>, <span className="mono">heimdall_check_answer</span>.
              </p>
            </div>
          </div>

          {status.sim ? (
            <div className="panel">
              <div className="panel-head"><div className="panel-title">Simulator</div></div>
              <div className="panel-body">
                <dl className="kv">
                  <dt>Application requests sent</dt><dd>{status.sim.traffic_sent.toLocaleString("en-US")}</dd>
                  <dt>Failed at the provider</dt><dd>{status.sim.traffic_failed.toLocaleString("en-US")}</dd>
                  <dt>Models shut down</dt><dd className="mono" style={{ fontSize: 12.5 }}>{(status.sim.retired ?? []).join(", ") || "none"}</dd>
                </dl>
                <p className="muted" style={{ fontSize: 13, marginTop: 14 }}>
                  Providers "aurora" and "lumen", the company "Northpost" and every page under <span className="mono">.example</span> are invented.
                  The gateway in the middle is real.
                </p>
              </div>
            </div>
          ) : null}
        </div>
      </div>
    </>
  );
}

export function Activity({ onOpenModel }: { onOpenModel: (id: string) => void }) {
  const { data, error, loading } = usePoll<{ events: EventItem[] }>(() => api.events(), 6000);
  const events = data?.events ?? [];
  return (
    <>
      <div className="page-head">
        <div>
          <h1 className="page-title">Activity</h1>
          <p className="page-sub">What Heimdall found and what was changed on the gateway. Warnings and above also go to the alert webhook, when one is set.</p>
        </div>
      </div>
      {error ? <div style={{ marginBottom: 18 }}><Notice kind="bad">{error}</Notice></div> : null}
      {loading && !data ? <div className="skeleton" style={{ height: 200 }} /> : events.length === 0 ? (
        <div className="panel"><div className="empty"><h3>Nothing yet</h3><p>Retirement notices, moved dates, rehearsal results and routing changes are recorded here as they happen.</p></div></div>
      ) : (
        <div className="panel">
          {events.map((e) => (
            <div key={e.id} className={`event ${e.level}`}>
              <span className="event-dot" aria-hidden="true" />
              <div style={{ minWidth: 0 }}>
                <div className="event-title">{e.title}</div>
                {e.detail ? <div className="event-detail">{e.detail}</div> : null}
                {e.model ? <button className="btn ghost sm mono" style={{ padding: "0 6px", marginTop: 6, marginLeft: -6, height: 24 }} onClick={() => onOpenModel(e.model!)}>{e.model}</button> : null}
              </div>
              <span className="event-time">{ago(e.at)}{e.alerted ? " · alerted" : ""}</span>
            </div>
          ))}
        </div>
      )}
    </>
  );
}
