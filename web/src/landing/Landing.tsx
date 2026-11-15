import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { ArrowRight, ArrowUpRight, Check, Copy, Circle } from "lucide-react";
import "./landing.css";
import { gsap, ScrollTrigger, scrollToId, startSmoothScroll, useReveal } from "./motion";
import { Mark } from "../App";
import { Sunset, type Sun } from "../Sunset";
import { Horizon, Problem, Steps } from "./scenesA";
import { Cutover, Grounding, Rehearsal } from "./scenesB";

const REPO = "https://github.com/angarkartanmay-ops/GlobalAiHack";
const COMMAND = "docker build -t heimdall . && docker run --rm -p 7860:7860 heimdall";

const INDEX: [string, string][] = [
  ["problem", "The problem"],
  ["watch", "The watch"],
  ["how", "How it works"],
  ["evidence", "Evidence"],
  ["rehearsal", "Rehearsal"],
  ["cutover", "Cutover"],
  ["boundary", "One boundary"],
  ["start", "Try it"],
];

// The simulator's own scenario: four retiring models on one gateway.
const DEMO: Sun[] = [
  { id: "aurora/aurora-instant-0", label: "aurora-instant-0", days: -5, share: 0.03 },
  { id: "aurora/aurora-large-1", label: "aurora-large-1", days: 23, share: 0.77 },
  { id: "aurora/aurora-mini-1", label: "aurora-mini-1", days: 96, share: 0.13 },
  { id: "lumen/lumen-chat-2-0815", label: "lumen-chat-2-0815", days: 110, share: 0.02 },
];

function Hero({ onOpen }: { onOpen: () => void }) {
  const root = useRef<HTMLElement>(null);
  const [elapsed, setElapsed] = useState(0);

  // Scrolling the hero moves time forward, so the suns sink toward the horizon.
  useLayoutEffect(() => {
    const el = root.current!;
    const mm = gsap.matchMedia();
    mm.add("(prefers-reduced-motion: no-preference)", () => {
      ScrollTrigger.create({
        trigger: el,
        start: "top top",
        end: "bottom 35%",
        onUpdate: (s) => setElapsed(Math.round(s.progress * 36 * 4) / 4),
      });
    });
    return () => mm.revert();
  }, []);

  const day = Math.round(elapsed);
  return (
    <section ref={root} className="lp-hero" id="top">
      <div className="lp-wrap lp-hero-copy">
        <p className="lp-kicker">A retirement watch for the Bifrost AI gateway</p>
        <h1 className="lp-h1">Every model you call has a shutdown date.</h1>
        <div className="lp-hero-row">
          <p className="lp-lede">
            Heimdall reads which models your gateway depends on, finds the retirement notices their providers have published, tests the replacement on your logged traffic, and moves the traffic over with one routing rule you can undo.
          </p>
          <div className="lp-cta">
            <button className="lp-btn primary" onClick={onOpen}>Open the live demo <ArrowRight size={17} /></button>
            <button className="lp-btn" onClick={() => scrollToId("problem")}>How it works</button>
          </div>
        </div>
      </div>
      <figure className="lp-hero-chart">
        <figcaption className="lp-wrap lp-figcap">
          <span>Four models on one gateway, by days until shutdown. Scroll, and the days pass.</span>
          <b>{day === 0 ? "Today" : `Day ${day}`}</b>
        </figcaption>
        <Sunset suns={DEMO} elapsed={elapsed} tall width={1440} />
      </figure>
    </section>
  );
}

const BIFROST: [string, string][] = [
  ["Logs and aggregates", "Inventory, sampling real requests, and canary health."],
  ["Routing rules", "The whole of cutover: safety net, canary, cut over, rollback."],
  ["Virtual keys and budgets", "Heimdall's own spend is capped by the gateway, like any consumer."],
  ["Relays", "Tavily sits behind the gateway. The gateway holds the key and logs every search."],
  ["Logging headers", "Its own requests are tagged, so they are never counted as demand."],
  ["Model API", "A provider's own shutdown_date is read as first-party evidence."],
  ["MCP gateway", "Three tools for the agents behind it: list sunsets, check a model, check an answer."],
];
const TAVILY: [string, string][] = [
  ["Extract", "Each provider's lifecycle page, read in full."],
  ["Search, official domains", "Per model, restricted to the provider's own documentation."],
  ["Search, open web", "Corroboration and early warning. Never outranks the provider."],
  ["Search, per claim", "The stale-answer detector, and the tiebreak in a rehearsal."],
];

function Boundary() {
  const ref = useRef<HTMLElement>(null);
  useReveal(ref, ".lp-brow", { stagger: 0.07, y: 22 });
  return (
    <div id="boundary">
      <section ref={ref} className="lp-block lp-boundary">
        <div className="lp-wrap lp-grid lp-grid-g">
          <div className="lp-copy lp-sticky">
            <h2 className="lp-h2">Everything goes through Bifrost.</h2>
            <p className="lp-p">No fork, no patched binary, no plugin to compile. Heimdall uses public endpoints on a stock install, and holds one virtual key and the gateway's admin login. No provider keys, no Tavily key.</p>
          </div>
          <div>
            <h3 className="lp-listhead">Bifrost</h3>
            <ul className="lp-list">{BIFROST.map(([a, b]) => <li key={a} className="lp-brow"><b>{a}</b><span>{b}</span></li>)}</ul>
            <h3 className="lp-listhead">Tavily</h3>
            <ul className="lp-list">{TAVILY.map(([a, b]) => <li key={a} className="lp-brow"><b>{a}</b><span>{b}</span></li>)}</ul>
          </div>
        </div>
      </section>
    </div>
  );
}

const VERIFIED = [
  "An end-to-end suite against a real Bifrost binary: inventory, radar, rehearsal, all four cutover stages, the answer check, and MCP registration.",
  "The container, built on the official maximhq/bifrost image.",
  "The grounding rules, against a model scripted to misquote and to invent dates.",
  "Live-mode configuration, up to the provider boundary.",
];
const NOT_YET = [
  "Real model providers and the real Tavily API. The build environment had no route to them.",
  "The Tavily client is tested against the request and response shapes of Tavily's own SDK.",
  "In the simulator the analyst is a rule-based stand-in, not a language model.",
  "The first run with real keys is the first test of those two boundaries.",
];

function Honest() {
  const ref = useRef<HTMLElement>(null);
  useReveal(ref, ".lp-hrow", { stagger: 0.06, y: 18 });
  return (
    <section ref={ref} className="lp-block lp-honest">
      <div className="lp-wrap">
        <h2 className="lp-h2 lp-h2-wide">What is verified, and what is not yet.</h2>
        <div className="lp-honest-grid">
          <div>
            <h3 className="lp-listhead good">Verified</h3>
            <ul className="lp-hlist">{VERIFIED.map((t) => <li key={t} className="lp-hrow"><Check size={16} />{t}</li>)}</ul>
          </div>
          <div>
            <h3 className="lp-listhead warn">Not yet</h3>
            <ul className="lp-hlist">{NOT_YET.map((t) => <li key={t} className="lp-hrow"><Circle size={16} />{t}</li>)}</ul>
          </div>
        </div>
      </div>
    </section>
  );
}

function Closing({ onOpen }: { onOpen: () => void }) {
  const ref = useRef<HTMLElement>(null);
  const [copied, setCopied] = useState(false);
  useReveal(ref, ".lp-rv", { stagger: 0.1, y: 24 });
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(COMMAND);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1800);
    } catch {
      /* clipboard unavailable */
    }
  };
  return (
    <div id="start">
      <section ref={ref} className="lp-block lp-closing">
        <div className="lp-wrap">
          <h2 className="lp-h2 lp-h2-wide lp-rv">Try it with one command. No keys needed.</h2>
          <p className="lp-p lp-rv">With none set, Heimdall runs a real Bifrost gateway with simulated providers, a simulated web and four simulated applications. Add a provider key, a Tavily key and a dashboard token, restart, and the same container runs live.</p>
          <div className="lp-code lp-rv">
            <code>{COMMAND}</code>
            <button onClick={copy} aria-label="Copy the command">{copied ? <Check size={16} /> : <Copy size={16} />}<span>{copied ? "Copied" : "Copy"}</span></button>
          </div>
          <div className="lp-cta lp-rv">
            <button className="lp-btn primary" onClick={onOpen}>Open the live demo <ArrowRight size={17} /></button>
            <a className="lp-btn" href={REPO} target="_blank" rel="noreferrer">View the source <ArrowUpRight size={16} /></a>
          </div>
        </div>
        <footer className="lp-wrap lp-foot">
          <span>Heimdall. Built for the Geek Room Global AI Hackathon 2026.</span>
          <span>MIT licensed.</span>
        </footer>
      </section>
    </div>
  );
}

export default function Landing({ onOpen }: { onOpen: () => void }) {
  const [active, setActive] = useState("");

  useEffect(() => {
    document.documentElement.classList.add("lp-on");
    const stop = startSmoothScroll();
    const refresh = () => ScrollTrigger.refresh();
    void document.fonts?.ready.then(refresh);
    window.addEventListener("load", refresh);
    return () => {
      document.documentElement.classList.remove("lp-on");
      window.removeEventListener("load", refresh);
      stop();
    };
  }, []);

  useLayoutEffect(() => {
    const triggers = INDEX.map(([id]) =>
      ScrollTrigger.create({
        trigger: `#${id}`,
        start: "top 55%",
        end: "bottom 45%",
        refreshPriority: -1,
        onToggle: (s) => {
          if (s.isActive) setActive(id);
        },
      }),
    );
    return () => {
      triggers.forEach((t) => t.kill());
    };
  }, []);

  return (
    <div className="lp">
      <header className="lp-nav">
        <button className="lp-brand" onClick={() => scrollToId("top")} aria-label="Heimdall, back to the top"><Mark size={26} /><span>Heimdall</span></button>
        <nav aria-label="Sections">
          {INDEX.filter(([id]) => ["problem", "how", "evidence", "cutover", "start"].includes(id)).map(([id, label]) => (
            <button key={id} onClick={() => scrollToId(id)} className={active === id ? "on" : ""}>{label}</button>
          ))}
        </nav>
        <button className="lp-btn sm" onClick={onOpen}>Open dashboard</button>
      </header>

      <main>
        <Hero onOpen={onOpen} />
        <Problem />
        <Horizon />
        <Steps />
        <Grounding />
        <Rehearsal />
        <Cutover />
        <Boundary />
        <Honest />
        <Closing onOpen={onOpen} />
      </main>
    </div>
  );
}
