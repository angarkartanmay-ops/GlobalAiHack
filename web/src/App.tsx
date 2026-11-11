import { lazy, Suspense, useCallback, useEffect, useState } from "react";
import { KeyRound } from "lucide-react";
import { api, getToken, setToken, type Overview, type Status } from "./api";
import { Notice, Spinner, usePoll } from "./ui";
import { Watch } from "./views/Watch";
import { Model } from "./views/Model";
import { Rehearsal } from "./views/Rehearsal";
import { Answers } from "./views/Answers";
import { Activity, Setup } from "./views/Setup";

const Landing = lazy(() => import("./landing/Landing"));

type Route =
  | { view: "home" }
  | { view: "watch" }
  | { view: "model"; id: string }
  | { view: "rehearsal"; id: string }
  | { view: "answers" }
  | { view: "activity" }
  | { view: "setup" };

function parse(hash: string): Route {
  const parts = hash.replace(/^#\/?/, "").split("/");
  const rest = decodeURIComponent(parts.slice(1).join("/"));
  switch (parts[0]) {
    case "model": return rest ? { view: "model", id: rest } : { view: "watch" };
    case "rehearsal": return rest ? { view: "rehearsal", id: rest } : { view: "watch" };
    case "watch": return { view: "watch" };
    case "answers": return { view: "answers" };
    case "activity": return { view: "activity" };
    case "setup": return { view: "setup" };
    default: return { view: "home" };
  }
}

function href(r: Route): string {
  switch (r.view) {
    case "model": return `#/model/${r.id.split("/").map(encodeURIComponent).join("/")}`;
    case "rehearsal": return `#/rehearsal/${encodeURIComponent(r.id)}`;
    case "home": return "#/";
    default: return `#/${r.view}`;
  }
}

function Mark() {
  return (
    <svg width="28" height="28" viewBox="0 0 32 32" aria-hidden="true">
      <rect width="32" height="32" rx="7" fill="var(--surface-2)" />
      <path d="M5 22.5c4-9.5 18-9.5 22 0" fill="none" stroke="var(--beacon)" strokeWidth="2.4" strokeLinecap="round" />
      <circle cx="16" cy="14" r="3.2" fill="var(--beacon)" />
    </svg>
  );
}

function Gate({ locked, onToken }: { locked: boolean; onToken: (t: string) => Promise<boolean> }) {
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState(false);
  const [bad, setBad] = useState(false);
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true); setBad(false);
    if (!(await onToken(value.trim()))) setBad(true);
    setBusy(false);
  };
  return (
    <div className="gate">
      <div className="gate-box">
        <Mark />
        <h1>Heimdall</h1>
        {locked ? (
          <>
            <p>This instance is connected to a live gateway and has no dashboard token, so it is locked.</p>
            <Notice kind="warn">Set <code>HEIMDALL_ADMIN_TOKEN</code> on the deployment and restart. The dashboard shows real request content and can change gateway routing, so it never runs open in live mode.</Notice>
          </>
        ) : (
          <form onSubmit={submit}>
            <p>Enter the dashboard token to continue.</p>
            <div className="field" style={{ marginBottom: 14 }}>
              <label htmlFor="tok">Dashboard token</label>
              <input id="tok" className="input mono" type="password" autoFocus autoComplete="current-password" value={value} onChange={(e) => setValue(e.target.value)} />
            </div>
            {bad ? <div style={{ marginBottom: 14 }}><Notice kind="bad">That token was not accepted.</Notice></div> : null}
            <button className="btn primary" disabled={busy || !value.trim()}>{busy ? <Spinner /> : <KeyRound size={15} />} Unlock</button>
          </form>
        )}
      </div>
    </div>
  );
}

export function App() {
  const [route, setRoute] = useState<Route>(() => parse(window.location.hash));
  const [toastMsg, setToastMsg] = useState<{ text: string; bad: boolean } | null>(null);
  const [authTick, setAuthTick] = useState(0);

  useEffect(() => {
    const onHash = () => { setRoute(parse(window.location.hash)); window.scrollTo(0, 0); };
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, []);

  const go = useCallback((r: Route) => { window.location.hash = href(r); }, []);

  const toast = useCallback((text: string, bad = false) => {
    setToastMsg({ text, bad });
    window.setTimeout(() => setToastMsg((cur) => (cur?.text === text ? null : cur)), bad ? 7000 : 3500);
  }, []);

  const status = usePoll<Status>(() => api.status(), 8000, [authTick]);
  const authed = !!status.data?.authenticated;
  const overview = usePoll<Overview>(() => api.overview(), 5000, [authTick], authed);

  const tryToken = async (t: string) => {
    setToken(t);
    try {
      const s = await api.status();
      if (s.authenticated) { setAuthTick((n) => n + 1); return true; }
    } catch { /* fall through */ }
    setToken("");
    return false;
  };

  if (route.view === "home") {
    return (
      <Suspense fallback={<div className="gate" />}>
        <Landing onOpen={() => go({ view: "watch" })} />
      </Suspense>
    );
  }

  const st = status.data;
  if (!st) {
    return (
      <div className="gate"><div className="gate-box">
        <Mark /><h1>Heimdall</h1>
        {status.error ? <Notice kind="bad">{status.error}</Notice> : <div className="row"><Spinner /> <span className="soft">Connecting</span></div>}
      </div></div>
    );
  }
  if (!st.authenticated) return <Gate locked={st.locked} onToken={tryToken} />;

  const down = st.gateway && !st.gateway.healthy;
  const nav: { view: Route["view"]; label: string }[] = [
    { view: "watch", label: "Watch" }, { view: "answers", label: "Answer check" }, { view: "activity", label: "Activity" }, { view: "setup", label: "Setup" },
  ];
  const active = route.view === "model" || route.view === "rehearsal" ? "watch" : route.view;

  return (
    <div className="shell">
      <header className="topbar">
        <button className="brand" onClick={() => go({ view: "home" })} aria-label="Heimdall, back to the overview">
          <Mark /><span className="brand-name">Heimdall</span>
        </button>
        <nav className="nav" aria-label="Sections">
          {nav.map((n) => (
            <button key={n.view} aria-current={active === n.view ? "page" : undefined} onClick={() => go({ view: n.view } as Route)}>{n.label}</button>
          ))}
        </nav>
        <div className="topbar-right">
          {down ? <span className="mode down"><span className="dot" /> Gateway unreachable</span> :
            st.simulated ? <span className="mode sim" title="A real Bifrost gateway, with simulated providers and a simulated web"><span className="dot" /> Simulated data</span> :
              <span className="mode live"><span className="dot" /> Live</span>}
          {st.auth_required && getToken() ? (
            <button className="btn ghost sm" onClick={() => { setToken(""); setAuthTick((n) => n + 1); }}>Lock</button>
          ) : null}
        </div>
      </header>

      <main className="page">
        {route.view === "watch" ? (
          <Watch overview={overview.data} status={st} loading={overview.loading} error={overview.error}
            onOpen={(id) => go({ view: "model", id })} onNavigate={(v) => go({ view: v })} refresh={() => { void overview.refresh(); void status.refresh(); }} />
        ) : null}
        {route.view === "model" ? (
          <Model id={route.id} status={st} onBack={() => go({ view: "watch" })} onOpenRehearsal={(id) => go({ view: "rehearsal", id })} toast={toast} />
        ) : null}
        {route.view === "rehearsal" ? (
          <Rehearsal id={route.id} onBack={() => window.history.length > 1 ? window.history.back() : go({ view: "watch" })} onOpenModel={(id) => go({ view: "model", id })} />
        ) : null}
        {route.view === "answers" ? <Answers status={st} /> : null}
        {route.view === "activity" ? <Activity onOpenModel={(id) => go({ view: "model", id })} /> : null}
        {route.view === "setup" ? <Setup status={st} toast={toast} /> : null}
      </main>

      {toastMsg ? <div className={`toast ${toastMsg.bad ? "bad" : ""}`} role="status">{toastMsg.text}</div> : null}
    </div>
  );
}
