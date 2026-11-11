import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { ArrowRight, ArrowUpRight, Check, Copy, Circle } from "lucide-react";
import "./landing.css";
import { gsap, ScrollTrigger, scrollToId, startSmoothScroll, useReveal } from "./motion";
import { Sky, type SkyState } from "./Sky";
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

function Mark() {
  return (
    <svg width="26" height="26" viewBox="0 0 32 32" aria-hidden="true">
      <rect width="32" height="32" rx="7" fill="var(--surface-2)" />
      <path d="M5 22.5c4-9.5 18-9.5 22 0" fill="none" stroke="var(--beacon)" strokeWidth="2.4" strokeLinecap="round" />
      <circle cx="16" cy="14" r="3.2" fill="var(--beacon)" />
    </svg>
  );
}

function Hero({ onOpen }: { onOpen: () => void }) {
  const root = useRef<HTMLElement>(null);
  const sky = useRef<SkyState>({ p: 0 });

  useLayoutEffect(() => {
    const el = root.current!;
    const mm = gsap.matchMedia();
    mm.add("(prefers-reduced-motion: no-preference)", () => {
      const q = gsap.utils.selector(el);
      gsap.from(q(".lp-line > span"), { yPercent: 118, duration: 1.2, ease: "power4.out", stagger: 0.1, delay: 0.2 });
      gsap.from(q(".lp-fade"), { opacity: 0, y: 20, duration: 1, ease: "power3.out", stagger: 0.09, delay: 0.7 });
      gsap.to(q(".lp-hero-copy"), {
        yPercent: -14,
        opacity: 0,
        ease: "none",
        scrollTrigger: { trigger: el, start: "top top", end: "bottom 35%", scrub: true },
      });
      ScrollTrigger.create({
        trigger: el,
        start: "top top",
        end: "bottom top",
        scrub: true,
        onUpdate: (s) => {
          sky.current.p = s.progress;
        },
      });
    });
    return () => mm.revert();
  }, []);

  return (
    <section ref={root} className="lp-hero" id="top">
      <Sky state={sky} />
      <div className="lp-hero-fade" aria-hidden="true" />
      <div className="lp-wrap lp-hero-copy">
        <p className="lp-eyebrow lp-fade"><i />Bifrost × Tavily · Geek Room Global AI Hackathon 2026</p>
        <h1 className="lp-h1"><span className="lp-line"><span>Heimdall</span></span></h1>
        <p className="lp-sub">
          <span className="lp-line"><span>The watchman for</span></span>
          <span className="lp-line"><span className="lp-amber">your AI gateway.</span></span>
        </p>
        <p className="lp-lede lp-fade">
          Heimdall reads which models your Bifrost gateway depends on, finds the retirement notices providers have published, proves the replacement on your real traffic, and moves traffic with one reversible routing rule.
        </p>
        <div className="lp-cta lp-fade">
          <button className="lp-btn primary" onClick={onOpen}>Open the live demo <ArrowRight size={17} /></button>
          <button className="lp-btn" onClick={() => scrollToId("problem")}>See how it works</button>
        </div>
        <p className="lp-facts lp-fade"><span>Runs on a stock Bifrost image</span><span>Evidence quoted from the source</span><span>MIT licensed</span></p>
      </div>
      <button className="lp-cue" onClick={() => scrollToId("problem")} aria-label="Scroll to the story"><span>Scroll</span><i /></button>
    </section>
  );
}

function Marquee() {
  const ref = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const el = ref.current!;
    const mm = gsap.matchMedia();
    mm.add("(prefers-reduced-motion: no-preference)", () => {
      gsap.fromTo(el.querySelector(".lp-marquee-track"), { xPercent: 0 }, {
        xPercent: -50,
        ease: "none",
        scrollTrigger: { trigger: el, start: "top bottom", end: "bottom top", scrub: 0.6 },
      });
    });
    return () => mm.revert();
  }, []);
  const words = ["model_not_found", "shutdown_date", "deprecated", "410 gone", "fallback", "canary", "retired", "end of life"];
  const run = (k: string) => words.map((w) => <span key={k + w}>{w}</span>);
  return (
    <div ref={ref} className="lp-marquee" aria-hidden="true">
      <div className="lp-marquee-track">{run("a")}{run("b")}{run("c")}{run("d")}</div>
    </div>
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
            <p className="lp-eyebrow"><i />One boundary</p>
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
        <p className="lp-eyebrow"><i />Stated plainly</p>
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
          <p className="lp-eyebrow lp-rv"><i />Try it</p>
          <h2 className="lp-h2 lp-h2-wide lp-rv">One command. No keys.</h2>
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
          <span>Built with AI assistance (Claude Code). MIT licensed.</span>
        </footer>
      </section>
    </div>
  );
}

export default function Landing({ onOpen }: { onOpen: () => void }) {
  const [active, setActive] = useState("");
  const [scrolled, setScrolled] = useState(false);

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
    const nav = ScrollTrigger.create({ start: 60, end: "max", refreshPriority: -1, onToggle: (s) => setScrolled(s.isActive) });
    return () => {
      triggers.forEach((t) => t.kill());
      nav.kill();
    };
  }, []);

  return (
    <div className="lp">
      <header className={`lp-nav${scrolled ? " solid" : ""}`}>
        <button className="lp-brand" onClick={() => scrollToId("top")} aria-label="Heimdall, back to the top"><Mark /><span>Heimdall</span></button>
        <nav aria-label="Sections">
          {INDEX.filter(([id]) => ["problem", "how", "evidence", "cutover", "start"].includes(id)).map(([id, label]) => (
            <button key={id} onClick={() => scrollToId(id)} className={active === id ? "on" : ""}>{label}</button>
          ))}
        </nav>
        <button className="lp-btn sm" onClick={onOpen}>Open dashboard</button>
      </header>

      <nav className="lp-dots" aria-label="Page sections">
        {INDEX.map(([id, label]) => (
          <button key={id} onClick={() => scrollToId(id)} className={active === id ? "on" : ""} aria-label={label} aria-current={active === id ? "true" : undefined}>
            <span>{label}</span><i />
          </button>
        ))}
      </nav>

      <main>
        <Hero onOpen={onOpen} />
        <Marquee />
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
