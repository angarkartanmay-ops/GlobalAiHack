import { useRef } from "react";
import { COLOR, gsap, useScene } from "./motion";

// ---------------------------------------------------------------- problem

const STATEMENT =
  "Providers retire models on a schedule. The notice goes to whoever created the account. The first your application team hears of it is a 404 in production.";

const OK_LINES: [string, string][] = [
  ["09:41:02", "312 ms"], ["09:41:09", "287 ms"], ["09:41:14", "341 ms"], ["09:41:26", "296 ms"],
  ["09:41:31", "322 ms"], ["09:41:44", "279 ms"], ["09:41:58", "308 ms"], ["09:42:03", "335 ms"],
];
const FAIL_TIMES = ["00:00:01", "00:00:01", "00:00:02", "00:00:02", "00:00:03", "00:00:03", "00:00:04"];

export function Problem() {
  const ref = useRef<HTMLElement>(null);
  const words = STATEMENT.split(" ");
  const hotFrom = words.length - 3;

  useScene(ref, ({ tl, q }) => {
    const w = q(".lp-word");
    const hot = q(".lp-word.hot");
    const ok = q(".lp-logline.ok");
    const fail = q(".lp-logline.fail");
    const mark = q(".lp-logmark");
    tl.fromTo(w, { opacity: 0.16 }, { opacity: 1, stagger: 0.13, duration: 0.4 }, 0);
    tl.fromTo(ok, { opacity: 0, y: 14 }, { opacity: 1, y: 0, stagger: 0.28, duration: 0.45, ease: "power2.out" }, 0.5);
    tl.fromTo(mark, { opacity: 0, scaleX: 0.6 }, { opacity: 1, scaleX: 1, duration: 0.5, ease: "power2.out", transformOrigin: "left center" }, 3.1);
    tl.fromTo(fail, { opacity: 0, y: 14 }, { opacity: 1, y: 0, stagger: 0.26, duration: 0.45, ease: "power2.out" }, 3.5);
    tl.fromTo(hot, { color: COLOR.text }, { color: COLOR.ember, stagger: 0.2, duration: 0.5 }, 3.6);
    tl.to({}, { duration: 1.4 }, 5.6);
  }, { end: "+=230%" });

  return (
    <div id="problem">
      <section ref={ref} className="lp-scene lp-problem">
        <div className="lp-wrap lp-grid lp-grid-a">
          <div>
            <p className="lp-eyebrow"><i />The problem</p>
            <p className="lp-statement">
              {words.map((wd, i) => (
                <span key={i} className={`lp-word${i >= hotFrom ? " hot" : ""}`}>{wd} </span>
              ))}
            </p>
          </div>
          <div className="lp-log" aria-hidden="true">
            <div className="lp-log-head"><span>gateway log</span><span className="mono">aurora-large-1</span></div>
            {OK_LINES.map(([t, ms]) => (
              <div key={t} className="lp-logline ok">
                <time>{t}</time><code>POST /v1/chat/completions</code><b>200</b><span>{ms}</span>
              </div>
            ))}
            <div className="lp-logmark"><span>Dec 4. The provider retires aurora-large-1</span></div>
            {FAIL_TIMES.map((t, i) => (
              <div key={i} className="lp-logline fail">
                <time>{t}</time><code>POST /v1/chat/completions</code><b>404</b><span>model_not_found</span>
              </div>
            ))}
          </div>
        </div>
      </section>
    </div>
  );
}

// ---------------------------------------------------------------- horizon

interface Mark { name: string; d: number; r: number; share: number; reps: string; up: boolean }

// Shares and dates follow the built-in simulator scenario, rounded.
const MARKS: Mark[] = [
  { name: "aurora-large-1", d: 23, r: 19.6, share: 82, reps: "aurora-large-2", up: true },
  { name: "aurora-mini-1", d: 96, r: 11.2, share: 12, reps: "aurora-mini-2", up: false },
  { name: "lumen-chat-2-0815", d: 110, r: 8.1, share: 2, reps: "lumen-chat-3", up: true },
];
const X0 = 48;
const X1 = 712;
const DAYS = 120;
const xOf = (d: number) => X0 + (d / DAYS) * (X1 - X0);

export function Horizon() {
  const ref = useRef<HTMLElement>(null);

  useScene(ref, ({ tl, q }) => {
    const head = q(".hz-head")[0] as unknown as SVGLineElement;
    const headLabel = q(".hz-headlabel")[0] as unknown as SVGTextElement;
    const gaugeNum = q(".lp-gauge-num")[0] as HTMLElement;
    const gaugeBar = q(".lp-gauge-bar i")[0] as HTMLElement;
    const copyA = q(".lp-phase-a");
    const copyB = q(".lp-phase-b");
    const cores = q(".hz-core");
    const dead = q(".hz-dead");
    const safe = q(".hz-safe");

    const ph = { d: 0 };
    const gauge = { v: 100 };
    const paintHead = () => {
      const x = xOf(ph.d);
      gsap.set(head, { attr: { x1: x, x2: x } });
      gsap.set(headLabel, { attr: { x: x }, textContent: `day ${Math.round(ph.d)}` });
    };
    const paintGauge = () => {
      gaugeNum.textContent = `${Math.round(gauge.v)}%`;
      gsap.set(gaugeBar, { scaleX: gauge.v / 100 });
    };
    gsap.set(head, { attr: { x1: xOf(0), x2: xOf(0) } });

    const SWEEP = 4.6;
    const at = (d: number, base: number) => base + (d / 118) * SWEEP;

    // phase B copy, markers, and dead labels start hidden
    tl.set(copyB, { opacity: 0, y: 16 }, 0);
    tl.set(dead, { opacity: 0 }, 0);
    tl.set(safe, { opacity: 0 }, 0);
    tl.fromTo(q(".lp-hz-stage"), { opacity: 0, y: 24 }, { opacity: 1, y: 0, duration: 0.8, ease: "power2.out" }, 0);
    tl.fromTo(q(".hz-mark"), { opacity: 0, scale: 0.4, transformOrigin: "50% 50%" }, { opacity: 1, scale: 1, stagger: 0.15, duration: 0.5, ease: "back.out(1.6)" }, 0.3);

    // phase A: the calendar decides
    tl.to(ph, { d: 118, duration: SWEEP, onUpdate: paintHead }, 1);
    [[0, 23, 18], [1, 96, 6], [2, 110, 4]].forEach(([i, d, served]) => {
      const t = at(d, 1);
      tl.to(cores[i], { fill: COLOR.ember, duration: 0.25 }, t);
      tl.to(dead[i], { opacity: 1, duration: 0.25 }, t);
      tl.to(gauge, { v: served, duration: 0.5, onUpdate: paintGauge }, t);
    });
    tl.to(gaugeBar, { backgroundColor: COLOR.ember, duration: 0.5 }, at(23, 1));

    // switch story
    const sw = 1 + SWEEP + 0.7;
    tl.to(copyA, { opacity: 0, y: -16, duration: 0.5 }, sw);
    tl.to(copyB, { opacity: 1, y: 0, duration: 0.5 }, sw + 0.3);
    tl.to(ph, { d: 0, duration: 0.5, onUpdate: paintHead }, sw);
    tl.to(cores, { fill: COLOR.blue, duration: 0.4 }, sw);
    tl.to(dead, { opacity: 0, duration: 0.3 }, sw);
    tl.to(gauge, { v: 100, duration: 0.5, onUpdate: paintGauge }, sw);
    tl.to(gaugeBar, { backgroundColor: COLOR.green, duration: 0.5 }, sw);

    // phase B: the date is just a date
    const B = sw + 1;
    tl.to(ph, { d: 118, duration: SWEEP, onUpdate: paintHead }, B);
    [[0, 23], [1, 96], [2, 110]].forEach(([i, d]) => {
      const t = at(d, B);
      tl.to(cores[i], { fill: COLOR.green, duration: 0.25 }, t);
      tl.to(safe[i], { opacity: 1, duration: 0.3 }, t);
    });
    tl.to({}, { duration: 1.2 }, B + SWEEP);
  }, { end: "+=440%" });

  return (
    <div id="watch">
      <section ref={ref} className="lp-scene lp-horizon">
        <div className="lp-wrap lp-grid lp-grid-b">
          <div className="lp-copy">
            <p className="lp-eyebrow"><i />The watch</p>
            <div className="lp-phases">
              <div className="lp-phase-a">
                <h2 className="lp-h2">Without a watch, the calendar decides.</h2>
                <p className="lp-p">Each shutdown date arrives on schedule. Every request still addressed to that model fails the same day, whoever sent it.</p>
              </div>
              <div className="lp-phase-b">
                <h2 className="lp-h2">With Heimdall, the date is just a date.</h2>
                <p className="lp-p">The safety net is armed weeks ahead. When the provider pulls the model, the gateway serves the request from the replacement.</p>
              </div>
            </div>
            <div className="lp-gauge" aria-live="off">
              <div className="lp-gauge-top"><span>Requests served</span><b className="lp-gauge-num">100%</b></div>
              <div className="lp-gauge-bar"><i /></div>
            </div>
          </div>
          <div className="lp-hz-stage">
            <svg viewBox="0 0 760 330" role="img" aria-label="Timeline of three scheduled model shutdowns, sized by share of requests">
              <line x1={X0} y1="170" x2={X1} y2="170" stroke={COLOR.line} strokeWidth="2" />
              {[0, 30, 60, 90, 120].map((d) => (
                <g key={d}>
                  <line x1={xOf(d)} y1="164" x2={xOf(d)} y2="176" stroke={COLOR.line} strokeWidth="1.5" />
                  <text x={xOf(d)} y="304" textAnchor="middle" className="hz-tick">{d === 0 ? "today" : `${d} d`}</text>
                </g>
              ))}
              {MARKS.map((m) => {
                const x = xOf(m.d);
                const ly = m.up ? 108 : 244;
                return (
                  <g key={m.name} className="hz-mark">
                    <line x1={x} y1={m.up ? 170 - m.r - 4 : 170 + m.r + 4} x2={x} y2={m.up ? ly + 14 : ly - 18} stroke={COLOR.line} />
                    <circle cx={x} cy="170" r={m.r + 6} fill={COLOR.blue} opacity="0.12" />
                    <circle className="hz-core" cx={x} cy="170" r={m.r} fill={COLOR.blue} stroke={COLOR.ground} strokeWidth="2" />
                    <text x={x} y={ly - (m.up ? 12 : 0)} textAnchor="middle" className="hz-name">{m.name}</text>
                    <text x={x} y={ly + (m.up ? 2 : 14)} textAnchor="middle" className="hz-sub">{m.share}% of requests · day {m.d}</text>
                    <text x={x} y={ly + (m.up ? 16 : 28)} textAnchor="middle" className="hz-dead">404 from here on</text>
                    <text x={x} y={ly + (m.up ? 16 : 28)} textAnchor="middle" className="hz-safe">now served by {m.reps}</text>
                  </g>
                );
              })}
              <line className="hz-head" x1={X0} y1="60" x2={X0} y2="284" stroke={COLOR.text} strokeWidth="1.5" />
              <text className="hz-headlabel" x={X0} y="48" textAnchor="middle">day 0</text>
            </svg>
            <p className="lp-note">Scenario from the built-in simulator, shares rounded. Circle size is the share of requests the model carries.</p>
          </div>
        </div>
      </section>
    </div>
  );
}

// ------------------------------------------------------------------ steps

const STEPS = [
  { n: "01", t: "Inventory", d: "Which models carry traffic, and for which virtual keys. Read from the gateway's own log aggregates, so Heimdall keeps no copy of your traffic.", tag: "Bifrost logs and aggregates", img: "watch" },
  { n: "02", t: "Radar", d: "Retirement notices from the provider's own documentation, retrieved with Tavily. Every finding shows the text it rests on.", tag: "Tavily, through a Bifrost relay", img: "evidence" },
  { n: "03", t: "Rehearsal", d: "Real logged requests replayed against the replacement. A parser catches broken tool calls and JSON; live evidence settles stale facts.", tag: "Logs, a virtual key, a budget", img: "rehearsal" },
  { n: "04", t: "Cutover", d: "Safety net, canary, cut over. One routing rule matched on the retiring model, removable in a click.", tag: "Bifrost routing rules", img: "cutover" },
];

export function Steps() {
  const ref = useRef<HTMLElement>(null);

  useScene(ref, ({ tl, q, mode }) => {
    if (mode !== "full") return;
    const steps = q(".lp-step");
    const imgs = q(".lp-shot");
    const stage = q(".lp-window");
    tl.fromTo(stage, { y: 40, opacity: 0 }, { y: 0, opacity: 1, duration: 0.7, ease: "power2.out" }, 0);
    gsap.set(imgs.slice(1), { opacity: 0 });
    gsap.set(steps.slice(1), { opacity: 0.28 });
    const HOLD = 3;
    for (let i = 1; i < STEPS.length; i++) {
      const t = i * HOLD;
      tl.to(imgs[i - 1], { opacity: 0, scale: 0.97, duration: 0.5 }, t);
      tl.to(steps[i - 1], { opacity: 0.28, duration: 0.4 }, t);
      tl.fromTo(imgs[i], { opacity: 0, scale: 1.04 }, { opacity: 1, scale: 1, duration: 0.7, ease: "power2.out" }, t + 0.05);
      tl.to(steps[i], { opacity: 1, duration: 0.4 }, t + 0.1);
    }
    tl.to({}, { duration: 0.8 }, STEPS.length * HOLD - 0.4);
  }, { end: "+=320%", staticOnCompact: true });

  return (
    <div id="how">
      <section ref={ref} className="lp-scene lp-steps">
        <div className="lp-wrap lp-grid lp-grid-c">
          <div className="lp-copy">
            <p className="lp-eyebrow"><i />How it works</p>
            <h2 className="lp-h2">Four steps, each one a Bifrost feature.</h2>
            <ol className="lp-steplist">
              {STEPS.map((s) => (
                <li key={s.n} className="lp-step">
                  <span className="lp-stepn">{s.n}</span>
                  <div>
                    <h3>{s.t}</h3>
                    <p>{s.d}</p>
                    <span className="lp-tag">{s.tag}</span>
                    <img className="lp-step-img" src={`/media/${s.img}.webp`} alt="" loading="lazy" width="1600" height="1000" />
                  </div>
                </li>
              ))}
            </ol>
          </div>
          <div className="lp-window" aria-hidden="true">
            <div className="lp-window-bar"><span>heimdall</span><span>live demo</span></div>
            <div className="lp-window-body">
              {STEPS.map((s) => (
                <img key={s.img} className="lp-shot" src={`/media/${s.img}.webp`} alt="" loading="lazy" width="1600" height="1000" />
              ))}
            </div>
          </div>
        </div>
      </section>
    </div>
  );
}
