import { useRef } from "react";
import { Check, X } from "lucide-react";
import { COLOR, gsap, ScrollTrigger, useScene } from "./motion";

// --------------------------------------------------------------- grounding

const RULES = [
  ["A quote must be in the source", "A paraphrase is discarded, and the discard is shown."],
  ["A date must be in the quote", "A date the model remembers is dropped."],
  ["Confidence comes from the source", "Never from how sure the model sounded."],
  ["The provider outranks everyone", "Disagreement is shown, not hidden."],
];

const ROW = "| aurora-large-1 | September 5, 2026 | December 4, 2026 | aurora-large-2 |";

export function Grounding() {
  const ref = useRef<HTMLElement>(null);

  useScene(ref, ({ tl, q }) => {
    const claims = q(".lp-claim");
    const stamps = q(".lp-stamp");
    const chips = q(".lp-chip");
    const strikes = q(".lp-strike");
    const rules = q(".lp-rule");
    const hl = q(".lp-hl")[0];
    const scans = q(".lp-scan");
    const finding = q(".lp-finding")[0];
    const dateSpan = q(".lp-row.r1 .d")[0];

    gsap.set(claims, { opacity: 0, y: 22 });
    gsap.set(stamps, { opacity: 0, scale: 1.5 });
    gsap.set(chips, { opacity: 0, y: 8 });
    gsap.set(strikes, { scaleX: 0, transformOrigin: "left center" });
    gsap.set(rules, { opacity: 0.3 });
    gsap.set(hl, { scaleX: 0, transformOrigin: "left center" });
    gsap.set(scans, { opacity: 0 });
    gsap.set(finding, { opacity: 0, y: 22 });

    const lit = (i: number, t: number) => tl.to(rules[i], { opacity: 1, duration: 0.4 }, t);
    const dim = (i: number, t: number) => tl.to(rules[i], { opacity: 0.3, duration: 0.3 }, t);

    // 1. a real quote
    tl.to(claims[0], { opacity: 1, y: 0, duration: 0.6, ease: "power2.out" }, 0.2);
    lit(0, 0.4);
    tl.to(hl, { scaleX: 1, duration: 0.7 }, 0.9);
    tl.to(chips[0], { opacity: 1, y: 0, duration: 0.4 }, 1.4);
    tl.to(stamps[0], { opacity: 1, scale: 1, duration: 0.4, ease: "back.out(2)" }, 1.8);
    tl.to(claims[0], { opacity: 0, y: -18, duration: 0.5 }, 3.1);
    tl.to(hl, { scaleX: 0, duration: 0.3 }, 3.1);

    // 2. a paraphrase
    tl.to(claims[1], { opacity: 1, y: 0, duration: 0.6, ease: "power2.out" }, 3.5);
    tl.to(chips[1], { opacity: 1, y: 0, duration: 0.4 }, 4.0);
    scans.forEach((sc, i) => {
      tl.to(sc, { opacity: 1, duration: 0.2 }, 4.2 + i * 0.4);
      tl.to(sc, { opacity: 0, duration: 0.2 }, 4.5 + i * 0.4);
    });
    tl.to(strikes[0], { scaleX: 1, duration: 0.4 }, 5.5);
    tl.to(stamps[1], { opacity: 1, scale: 1, duration: 0.4, ease: "back.out(2)" }, 5.7);
    tl.to(stamps[1], { x: 5, duration: 0.06, repeat: 5, yoyo: true, ease: "power1.inOut" }, 6.1);
    tl.to(claims[1], { opacity: 0, y: -18, duration: 0.5 }, 7.0);

    // 3. a date that is not in the quote
    tl.to(claims[2], { opacity: 1, y: 0, duration: 0.6, ease: "power2.out" }, 7.4);
    lit(1, 7.5);
    dim(0, 7.5);
    tl.to(hl, { scaleX: 1, duration: 0.5 }, 7.8);
    tl.to(chips[2], { opacity: 1, y: 0, duration: 0.4 }, 8.3);
    tl.to(dateSpan, { color: COLOR.amber, duration: 0.3 }, 8.5);
    tl.to(strikes[1], { scaleX: 1, duration: 0.4 }, 9.0);
    tl.to(stamps[2], { opacity: 1, scale: 1, duration: 0.4, ease: "back.out(2)" }, 9.2);
    tl.to(claims[2], { opacity: 0, y: -18, duration: 0.5 }, 10.5);
    tl.to(hl, { scaleX: 0, duration: 0.3 }, 10.5);
    tl.to(dateSpan, { color: COLOR.text, duration: 0.2 }, 10.5);

    // 4. what survives
    tl.to(finding, { opacity: 1, y: 0, duration: 0.7, ease: "power2.out" }, 11.1);
    lit(2, 11.3);
    lit(3, 11.7);
    dim(1, 11.3);
    tl.to({}, { duration: 1.2 }, 12.2);
  }, { end: "+=480%" });

  return (
    <div id="evidence">
      <section ref={ref} className="lp-scene lp-grounding">
        <div className="lp-wrap lp-grid lp-grid-d">
          <div className="lp-copy">
            <p className="lp-eyebrow"><i />Evidence</p>
            <h2 className="lp-h2">The model points at the text. Heimdall decides.</h2>
            <ol className="lp-rules">
              {RULES.map(([t, d], i) => (
                <li key={t} className="lp-rule">
                  <span>{i + 1}</span>
                  <div><b>{t}</b><p>{d}</p></div>
                </li>
              ))}
            </ol>
          </div>
          <div className="lp-evidence">
            <div className="lp-doc">
              <div className="lp-doc-bar"><span>docs.aurora-ai.example/models/deprecations</span><em>retrieved with Tavily</em></div>
              <div className="lp-doc-body">
                <div className="lp-row head"><span>Model</span><span>Announced</span><span>Shutdown date</span><span>Replacement</span></div>
                <div className="lp-row r1">
                  <i className="lp-hl" /><i className="lp-scan" />
                  <span>aurora-large-1</span><span>September 5, 2026</span><span className="d">December 4, 2026</span><span>aurora-large-2</span>
                </div>
                <div className="lp-row r2"><i className="lp-scan" /><span>aurora-mini-1</span><span>October 18, 2026</span><span className="d">February 15, 2027</span><span>aurora-mini-2</span></div>
                <div className="lp-row r3"><i className="lp-scan" /><span>aurora-instant-0</span><span>July 9, 2026</span><span className="d">November 6, 2026</span><span>aurora-mini-2</span></div>
              </div>
            </div>
            <div className="lp-claims">
              <div className="lp-claim">
                <div className="lp-claim-head">Notice from the model, 1 of 3</div>
                <code className="lp-quote">{ROW}</code>
                <span className="lp-chip">shutdown 2026-12-04</span>
                <div className="lp-stamp ok"><Check size={15} /><span><b>Grounded.</b> Quote found word for word. Date found in the quote.</span></div>
              </div>
              <div className="lp-claim">
                <div className="lp-claim-head">Notice from the model, 2 of 3</div>
                <code className="lp-quote">aurora-large-1 will stop working in early December.</code>
                <span className="lp-chip">shutdown 2026-12<i className="lp-strike" /></span>
                <div className="lp-stamp bad"><X size={15} /><span><b>Discarded.</b> That sentence is not in the retrieved text.</span></div>
              </div>
              <div className="lp-claim">
                <div className="lp-claim-head">Notice from the model, 3 of 3</div>
                <code className="lp-quote">{ROW}</code>
                <span className="lp-chip">shutdown 2026-11-20<i className="lp-strike" /></span>
                <div className="lp-stamp warn"><X size={15} /><span><b>Date dropped.</b> 2026-11-20 is not in the quote, so it cannot reach high confidence.</span></div>
              </div>
              <div className="lp-finding">
                <div className="lp-claim-head">What Heimdall keeps</div>
                <p><b>aurora-large-1</b> shuts down on <b>4 Dec 2026</b>. Replacement: <b>aurora-large-2</b>.</p>
                <div className="lp-finding-tags"><span className="lp-tag ok">High confidence</span><span className="lp-tag">Official source</span><span className="lp-tag">Exact model</span><span className="lp-tag">Full date in the quote</span></div>
              </div>
            </div>
          </div>
        </div>
      </section>
    </div>
  );
}

// --------------------------------------------------------------- rehearsal

// 40 replayed requests. Three regressed, six were more current than production.
const REGRESS = new Set([7, 19, 33]);
const BETTER = new Set([4, 11, 15, 22, 27, 36]);
const OUTCOMES = Array.from({ length: 40 }, (_, i) => (REGRESS.has(i) ? "regression" : BETTER.has(i) ? "better" : "equivalent"));
const OUT_COLOR: Record<string, string> = { equivalent: COLOR.green, better: COLOR.blue, regression: COLOR.ember };

export function Rehearsal() {
  const ref = useRef<HTMLElement>(null);

  useScene(ref, ({ tl, q }) => {
    const cells = q(".lp-cell");
    const num = (sel: string) => q(sel)[0] as HTMLElement;
    const c = { replayed: 0, regress: 0, cost: 0, fresh: 0 };
    const paint = () => {
      num(".cnt-replayed").textContent = String(Math.round(c.replayed));
      num(".cnt-regress").textContent = String(Math.round(c.regress));
      num(".cnt-cost").textContent = `${Math.round(c.cost) > 0 ? "−" : ""}${Math.round(c.cost)}%`;
      num(".cnt-fresh").textContent = String(Math.round(c.fresh));
    };
    paint();

    tl.fromTo(cells, { opacity: 0, scale: 0.35 }, { opacity: 1, scale: 1, stagger: 0.04, duration: 0.4, ease: "back.out(1.6)", transformOrigin: "50% 50%" }, 0.2);
    tl.to(c, { replayed: 40, duration: 1.9, onUpdate: paint }, 0.2);
    tl.to(cells, { backgroundColor: (_i: number, el: Element) => OUT_COLOR[(el as HTMLElement).dataset.out ?? "equivalent"], stagger: 0.055, duration: 0.35 }, 2.3);
    tl.to(c, { regress: 3, duration: 1.2, onUpdate: paint }, 3.4);
    tl.to(c, { fresh: 6, duration: 1.2, onUpdate: paint }, 3.4);
    tl.to(c, { cost: 35, duration: 1.4, onUpdate: paint }, 3.4);
    tl.fromTo(q(".lp-broke > *"), { opacity: 0, y: 14 }, { opacity: 1, y: 0, stagger: 0.25, duration: 0.5, ease: "power2.out" }, 4.8);
    tl.fromTo(q(".lp-tiebreak"), { opacity: 0, y: 30 }, { opacity: 1, y: 0, duration: 0.8, ease: "power2.out" }, 6.0);
    tl.fromTo(q(".lp-tb-prod .lp-tag"), { opacity: 0, scale: 0.8 }, { opacity: 1, scale: 1, duration: 0.4, ease: "back.out(2)" }, 7.0);
    tl.fromTo(q(".lp-tb-cand .lp-tag"), { opacity: 0, scale: 0.8 }, { opacity: 1, scale: 1, duration: 0.4, ease: "back.out(2)" }, 7.4);
    tl.to({}, { duration: 1.4 }, 8);
  }, { end: "+=420%" });

  return (
    <div id="rehearsal">
      <section ref={ref} className="lp-scene lp-rehearsal">
        <div className="lp-wrap lp-grid lp-grid-e">
          <div className="lp-rehearsal-vis">
            <div className="lp-cells" aria-hidden="true">
              {OUTCOMES.map((o, i) => <i key={i} className="lp-cell" data-out={o} />)}
            </div>
            <div className="lp-legend"><span><i style={{ background: COLOR.green }} />equivalent</span><span><i style={{ background: COLOR.blue }} />more current</span><span><i style={{ background: COLOR.ember }} />regression</span></div>
            <div className="lp-tiebreak">
              <div className="lp-claim-head">Settled by live evidence</div>
              <div className="lp-tb-grid">
                <div className="lp-tb-prod"><span>Production says</span><p>New integrations should use Northpost API v4.</p><span className="lp-tag bad">Out of date</span></div>
                <div className="lp-tb-cand"><span>Candidate says</span><p>Use Northpost API v5, the current version.</p><span className="lp-tag ok">Still true</span></div>
              </div>
              <p className="lp-tb-src">“Northpost API v5 is the current version of the Northpost API.” developers.northpost.example</p>
            </div>
          </div>
          <div className="lp-copy">
            <p className="lp-eyebrow"><i />Rehearsal</p>
            <h2 className="lp-h2">Prove the replacement on your own traffic.</h2>
            <p className="lp-p">Real requests from the gateway's logs, replayed against the candidate under a budgeted virtual key. Production is never touched.</p>
            <dl className="lp-counts">
              <div><dt>Requests replayed</dt><dd className="cnt-replayed">0</dd></div>
              <div><dt>Regressions</dt><dd className="cnt-regress bad">0</dd></div>
              <div><dt>Cost on this traffic</dt><dd className="cnt-cost good">0%</dd></div>
              <div><dt>Answers more current</dt><dd className="cnt-fresh">0</dd></div>
            </dl>
            <div className="lp-broke">
              <span className="lp-tag bad">JSON wrapped in a markdown fence</span>
              <span className="lp-tag bad">Order id sent as a number, not a string</span>
              <span className="lp-tag bad">Tool call skipped for a prose answer</span>
            </div>
            <p className="lp-note">Found by parsing the output, not by asking a model. Figures are from one rehearsal in the built-in simulator.</p>
          </div>
        </div>
      </section>
    </div>
  );
}

// ----------------------------------------------------------------- cutover

const RULE_TEXT = [
  { k: "No rule yet", code: "provider == aurora && model == aurora-large-1\n\n(no routing rule)\nrequests go to the model they name" },
  { k: "Safety net", code: "targets:    aurora-large-1  100%\nfallbacks:  aurora-large-2\n\nif the provider fails, the replacement answers" },
  { k: "Canary", code: "targets:    aurora-large-1   70%\n            aurora-large-2   30%\nfallbacks:  each side backs up the other" },
  { k: "The provider retires it", code: "aurora-large-1  ->  404 model_not_found\n                    fallback: aurora-large-2\n\nthe application sees a 200" },
  { k: "Cut over", code: "targets:    aurora-large-2  100%\nfallbacks:  aurora-large-1\n\nthe application still sends the old name" },
];

interface Route {
  total: number;
  at: (u: number) => DOMPoint;
}

function route(parts: [SVGPathElement, number, number][]): Route {
  const segs = parts.map(([el, a, b]) => ({ el, a, b, len: el.getTotalLength() * (b - a), L: el.getTotalLength() }));
  const total = segs.reduce((s, p) => s + p.len, 0);
  return {
    total,
    at(u: number) {
      let d = Math.max(0, Math.min(1, u)) * total;
      for (const p of segs) {
        if (d <= p.len || p === segs[segs.length - 1]) {
          const f = p.len ? Math.min(1, d / p.len) : 1;
          return p.el.getPointAtLength(p.L * (p.a + (p.b - p.a) * f));
        }
        d -= p.len;
      }
      return new DOMPoint(0, 0);
    },
  };
}

export function Cutover() {
  const ref = useRef<HTMLElement>(null);

  useScene(ref, ({ tl, q, el }) => {
    const svgPath = (cls: string) => q(cls)[0] as unknown as SVGPathElement;
    const trunk = svgPath(".cv-trunk");
    const upper = svgPath(".cv-upper");
    const lower = svgPath(".cv-lower");
    const retry = svgPath(".cv-retry");
    const routes = {
      old: route([[trunk, 0, 1], [upper, 0, 1]]),
      neu: route([[trunk, 0, 1], [lower, 0, 1]]),
      fb: route([[trunk, 0, 1], [upper, 0, 0.62], [retry, 0, 1], [lower, 0.62, 1]]),
    };
    const dots = q(".cv-dot") as unknown as SVGCircleElement[];
    const servedEl = q(".cv-served")[0] as HTMLElement;
    const state = { toNew: 0, oldAlive: 1 };
    const sim = dots.map(() => ({ u: 2, kind: "old" as keyof typeof routes, sp: 1 }));
    let served = 0;
    let spawn = 0;
    let running = false;

    const tick = () => {
      if (!running) return;
      const dt = Math.min(0.05, gsap.ticker.deltaRatio() / 60);
      spawn -= dt;
      if (spawn <= 0) {
        const free = sim.findIndex((s) => s.u > 1);
        if (free >= 0) {
          const s = sim[free];
          s.u = 0;
          s.sp = 0.85 + Math.random() * 0.35;
          s.kind = Math.random() < state.toNew ? "neu" : state.oldAlive > 0.5 ? "old" : "fb";
        }
        spawn = 0.22;
      }
      sim.forEach((s, i) => {
        const dot = dots[i];
        if (s.u > 1) {
          dot.setAttribute("opacity", "0");
          return;
        }
        s.u += (dt / 3) * s.sp;
        if (s.u > 1) {
          served++;
          servedEl.textContent = String(served);
          return;
        }
        const p = routes[s.kind].at(s.u);
        dot.setAttribute("cx", String(p.x));
        dot.setAttribute("cy", String(p.y));
        dot.setAttribute("opacity", "0.95");
        // a request that hits the retired model turns ember, then is served by the replacement
        const ember = s.kind === "fb" && s.u > 0.4 && s.u < 0.62;
        dot.setAttribute("fill", ember ? COLOR.ember : s.kind === "fb" && s.u >= 0.62 ? COLOR.green : "#ffecc8");
      });
    };
    const st = ScrollTrigger.create({
      trigger: el.closest("#cutover") ?? el,
      refreshPriority: -1,
      start: "top bottom",
      end: "bottom top",
      onToggle: (self) => {
        running = self.isActive;
        if (running) gsap.ticker.add(tick);
        else gsap.ticker.remove(tick);
      },
    });

    const rules = q(".lp-rulebox");
    const stages = q(".lp-stage");
    const oldNode = q(".cv-old")[0];
    const newNode = q(".cv-new")[0];
    const ruleNode = q(".cv-rule")[0];
    gsap.set(rules, { opacity: 0, y: 10 });
    gsap.set(rules[0], { opacity: 1, y: 0 });
    gsap.set(stages, { opacity: 0.3 });
    gsap.set(stages[0], { opacity: 1 });
    gsap.set(q(".cv-retrylane"), { opacity: 0 });
    gsap.set(q(".cv-oldlabel"), { opacity: 0 });

    const stage = (i: number, t: number) => {
      tl.to(rules[i - 1], { opacity: 0, y: -10, duration: 0.4 }, t);
      tl.to(rules[i], { opacity: 1, y: 0, duration: 0.5 }, t + 0.15);
      tl.to(stages[i - 1], { opacity: 0.3, duration: 0.3 }, t);
      tl.to(stages[i], { opacity: 1, duration: 0.4 }, t + 0.1);
    };
    tl.fromTo(q(".lp-cv-stage"), { opacity: 0, y: 24 }, { opacity: 1, y: 0, duration: 0.8, ease: "power2.out" }, 0);

    stage(1, 2.4);
    tl.to(ruleNode, { stroke: COLOR.amber, duration: 0.5 }, 2.4);
    tl.to(q(".cv-retrylane"), { opacity: 1, duration: 0.5 }, 2.6);

    stage(2, 5);
    tl.to(state, { toNew: 0.3, duration: 1 }, 5);

    stage(3, 8);
    tl.to(oldNode, { fill: "#3a2224", stroke: COLOR.ember, duration: 0.5 }, 8);
    tl.to(q(".cv-oldlabel"), { opacity: 1, duration: 0.4 }, 8.1);
    tl.to(state, { oldAlive: 0, duration: 0.2 }, 8);

    stage(4, 11);
    tl.to(state, { toNew: 1, duration: 1.2 }, 11);
    tl.to(oldNode, { opacity: 0.45, duration: 0.5 }, 11.4);
    tl.to(newNode, { stroke: COLOR.green, duration: 0.5 }, 11);
    tl.to({}, { duration: 1.6 }, 12);

    return () => {
      st.kill();
      gsap.ticker.remove(tick);
    };
  }, { end: "+=520%" });

  return (
    <div id="cutover">
      <section ref={ref} className="lp-scene lp-cutover">
        <div className="lp-wrap lp-grid lp-grid-f">
          <div className="lp-copy">
            <p className="lp-eyebrow"><i />Cutover</p>
            <h2 className="lp-h2">Cutover is one routing rule.</h2>
            <p className="lp-p">Heimdall never proxies a request. A migration is a single Bifrost rule, matched on the retiring model. Remove it and the gateway behaves exactly as before.</p>
            <ol className="lp-stages">
              {RULE_TEXT.map((r, i) => <li key={r.k} className="lp-stage"><span>{i + 1}</span>{r.k}</li>)}
            </ol>
          </div>
          <div className="lp-cv-stage">
            <svg viewBox="0 0 840 420" role="img" aria-label="Requests flow from applications through one routing rule to the old model and its replacement">
              <path className="cv-trunk" d="M176,210 L280,210 L440,210 L470,210" fill="none" stroke={COLOR.line} strokeWidth="2" />
              <path className="cv-upper" d="M470,210 C515,210 520,105 565,105 L640,105" fill="none" stroke={COLOR.line} strokeWidth="2" />
              <path className="cv-lower" d="M470,210 C515,210 520,315 565,315 L640,315" fill="none" stroke={COLOR.line} strokeWidth="2" />
              <g className="cv-retrylane">
                <path className="cv-retry" d="M525,135 C505,190 505,230 525,285" fill="none" stroke={COLOR.ember} strokeWidth="1.5" strokeDasharray="4 5" opacity="0.7" />
                <text x="498" y="214" textAnchor="end" className="cv-cap">fallback</text>
              </g>
              <g>
                <rect x="24" y="150" width="152" height="120" rx="10" className="cv-box" />
                <text x="100" y="188" textAnchor="middle" className="cv-cap">applications</text>
                <text x="100" y="214" textAnchor="middle" className="cv-name">checkout-app</text>
                <text x="100" y="238" textAnchor="middle" className="cv-name">support-bot</text>
              </g>
              <g>
                <rect className="cv-rule" x="246" y="170" width="228" height="80" rx="10" fill={COLOR.surface} stroke={COLOR.line} strokeWidth="2" />
                <text x="360" y="204" textAnchor="middle" className="cv-cap">Bifrost routing rule</text>
                <text x="360" y="228" textAnchor="middle" className="cv-name">heimdall-aurora-large-1</text>
              </g>
              <g>
                <rect className="cv-old" x="640" y="66" width="180" height="78" rx="10" fill={COLOR.surface} stroke={COLOR.line} strokeWidth="2" />
                <text x="730" y="100" textAnchor="middle" className="cv-cap">retiring model</text>
                <text x="730" y="124" textAnchor="middle" className="cv-name">aurora-large-1</text>
                <text x="730" y="164" textAnchor="middle" className="cv-oldlabel">404 model_not_found</text>
              </g>
              <g>
                <rect className="cv-new" x="640" y="276" width="180" height="78" rx="10" fill={COLOR.surface} stroke={COLOR.line} strokeWidth="2" />
                <text x="730" y="310" textAnchor="middle" className="cv-cap">replacement</text>
                <text x="730" y="334" textAnchor="middle" className="cv-name">aurora-large-2</text>
              </g>
              {Array.from({ length: 28 }, (_, i) => <circle key={i} className="cv-dot" r="3.4" cx="0" cy="0" fill="#ffecc8" opacity="0" />)}
            </svg>
            <div className="lp-cv-foot">
              <div className="lp-rulestack">
                {RULE_TEXT.map((r) => <pre key={r.k} className="lp-rulebox">{r.code}</pre>)}
              </div>
              <div className="lp-served"><span>Served to applications</span><b className="cv-served">0</b><em>Failed: 0</em></div>
            </div>
          </div>
        </div>
      </section>
    </div>
  );
}
