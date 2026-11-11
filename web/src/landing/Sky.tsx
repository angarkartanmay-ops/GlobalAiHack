import { useEffect, useRef, type MutableRefObject } from "react";

export interface SkyState {
  /** 0 at the top of the page, 1 once the hero has scrolled out. */
  p: number;
}

type RGB = [number, number, number];

/** A 1px-wide vertical gradient, drawn stretched to paint one aurora column. */
function strip(rgb: RGB) {
  const c = document.createElement("canvas");
  c.width = 1;
  c.height = 64;
  const g = c.getContext("2d")!;
  const gr = g.createLinearGradient(0, 0, 0, 64);
  gr.addColorStop(0, `rgba(${rgb[0]},${rgb[1]},${rgb[2]},0)`);
  gr.addColorStop(0.5, `rgba(${rgb[0]},${rgb[1]},${rgb[2]},1)`);
  gr.addColorStop(1, `rgba(${rgb[0]},${rgb[1]},${rgb[2]},0)`);
  g.fillStyle = gr;
  g.fillRect(0, 0, 1, 64);
  return c;
}

function rng(seed: number) {
  let s = seed;
  return () => (s = (s * 1664525 + 1013904223) % 4294967296) / 4294967296;
}

interface Layer {
  base: number;
  waves: [number, number, number][];
  color: string;
  rim?: string;
  par: number;
}

const LAYERS: Layer[] = [
  { base: 0.7, waves: [[0.045, 1.3, 0.4], [0.025, 3.1, 1.9], [0.012, 7.3, 0.2]], color: "#1b243a", rim: "rgba(125,184,232,0.2)", par: 0.18 },
  { base: 0.79, waves: [[0.05, 1.1, 2.2], [0.03, 2.7, 0.6], [0.014, 6.1, 3.0]], color: "#151c2e", par: 0.1 },
  { base: 0.91, waves: [[0.035, 0.8, 1.1], [0.02, 2.2, 4.1], [0.01, 5.7, 0.9]], color: "#0f1420", par: 0.04 },
];

const ridge = (u: number, L: Layer) => L.base + L.waves.reduce((s, [a, f, ph]) => s + a * Math.sin(u * f * 6.2832 + ph), 0);

interface Ribbon {
  img: HTMLCanvasElement;
  base: number;
  amp: number;
  f1: number;
  s1: number;
  f2: number;
  s2: number;
  f3: number;
  s3: number;
  h: number;
  alpha: number;
  ph: number;
}

interface Particle {
  t: number;
  sp: number;
  off: number;
  r: number;
  state: 0 | 1 | 2; // flowing, doomed, falling
  age: number;
}

/**
 * The hero scene: an aurora, stars, three ridgelines, a watchtower whose
 * beacon sweeps, and the bridge, which is the gateway. Requests cross it. Now
 * and then one is addressed to a retired model and falls.
 */
export function Sky({ state }: { state: MutableRefObject<SkyState> }) {
  const aurRef = useRef<HTMLCanvasElement>(null);
  const fxRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const aur = aurRef.current!;
    const fx = fxRef.current!;
    const host = fx.parentElement!;
    const actx = aur.getContext("2d")!;
    const ctx = fx.getContext("2d")!;
    const reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

    let W = 0;
    let H = 0;
    let aw = 0;
    let ah = 0;
    let raf = 0;
    let visible = true;
    let mx = 0;
    let my = 0;
    let tx = 0;
    let ty = 0;
    let last = performance.now();
    let clock = reduce ? 4 : 0;
    let nextDoom = 3;

    const r = rng(11);
    const stars = Array.from({ length: 240 }, () => ({
      x: r(),
      y: r() * 0.74,
      s: r() < 0.1 ? 1.7 : r() < 0.45 ? 1.15 : 0.7,
      ph: r() * 6.28,
      sp: 0.5 + r() * 1.5,
    }));
    const ribbons: Ribbon[] = [
      { img: strip([95, 211, 166]), base: 0.34, amp: 0.07, f1: 1.6, s1: 0.16, f2: 4.1, s2: 0.23, f3: 2.2, s3: 0.3, h: 0.12, alpha: 0.5, ph: 0.4 },
      { img: strip([125, 184, 232]), base: 0.27, amp: 0.06, f1: 2.1, s1: -0.12, f2: 5.3, s2: -0.2, f3: 3.1, s3: 0.25, h: 0.1, alpha: 0.42, ph: 2.1 },
      { img: strip([110, 205, 200]), base: 0.4, amp: 0.05, f1: 1.2, s1: 0.1, f2: 3.4, s2: 0.18, f3: 1.8, s3: -0.2, h: 0.08, alpha: 0.3, ph: 4.3 },
    ];
    const particles: Particle[] = Array.from({ length: 44 }, () => ({
      t: r(),
      sp: 0.03 + r() * 0.05,
      off: (r() - 0.5) * 9,
      r: 1 + r() * 1.1,
      state: 0,
      age: 0,
    }));

    const size = () => {
      W = host.clientWidth;
      H = host.clientHeight;
      const dpr = Math.min(window.devicePixelRatio || 1, 1.5);
      fx.width = Math.round(W * dpr);
      fx.height = Math.round(H * dpr);
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      aw = Math.max(40, Math.ceil(W / 6));
      ah = Math.max(30, Math.ceil(H / 6));
      aur.width = aw;
      aur.height = ah;
    };

    const bridgeOf = () => {
      const narrow = W < 760;
      const tx = narrow ? 0.76 : 0.6;
      const tower = { x: W * tx, y: ridge(tx, LAYERS[1]) * H };
      const S = { x: tower.x, y: tower.y - 40 };
      const C = narrow ? { x: W * 0.92, y: H * 0.4 } : { x: W * 0.88, y: -H * 0.02 };
      const E = narrow ? { x: W * 1.08, y: H * 0.66 } : { x: W * 1.07, y: H * 0.56 };
      return { tower, S, C, E };
    };
    const quad = (b: ReturnType<typeof bridgeOf>, t: number) => {
      const u = 1 - t;
      const x = u * u * b.S.x + 2 * u * t * b.C.x + t * t * b.E.x;
      const y = u * u * b.S.y + 2 * u * t * b.C.y + t * t * b.E.y;
      const dx = 2 * u * (b.C.x - b.S.x) + 2 * t * (b.E.x - b.C.x);
      const dy = 2 * u * (b.C.y - b.S.y) + 2 * t * (b.E.y - b.C.y);
      const n = Math.hypot(dx, dy) || 1;
      return { x, y, nx: -dy / n, ny: dx / n };
    };

    const drawAurora = (t: number, dim: number) => {
      actx.clearRect(0, 0, aw, ah);
      actx.globalCompositeOperation = "lighter";
      for (const b of ribbons) {
        for (let x = 0; x < aw; x++) {
          const u = x / aw;
          const y = (b.base + b.amp * Math.sin(u * b.f1 * 6.283 + t * b.s1 + b.ph) + b.amp * 0.55 * Math.sin(u * b.f2 * 6.283 - t * b.s2)) * ah;
          const h = ah * b.h * (0.7 + 0.3 * Math.sin(u * b.f3 * 6.283 + t * b.s3 + b.ph));
          actx.globalAlpha = b.alpha * dim * (0.55 + 0.45 * Math.sin(u * 9 + t * 0.6 + b.ph));
          actx.drawImage(b.img, 0, 0, 1, 64, x, y - h, 1, h * 2);
        }
      }
      actx.globalAlpha = 1;
    };

    const drawRidge = (L: Layer, offY: number, offX: number) => {
      ctx.beginPath();
      ctx.moveTo(-10, H + 10);
      for (let x = -10; x <= W + 10; x += 8) ctx.lineTo(x + offX, ridge(x / W, L) * H + offY);
      ctx.lineTo(W + 10, H + 10);
      ctx.closePath();
      ctx.fillStyle = L.color;
      ctx.fill();
      if (L.rim) {
        ctx.beginPath();
        for (let x = -10; x <= W + 10; x += 8) {
          const y = ridge(x / W, L) * H + offY;
          if (x === -10) ctx.moveTo(x + offX, y);
          else ctx.lineTo(x + offX, y);
        }
        ctx.strokeStyle = L.rim;
        ctx.lineWidth = 1;
        ctx.stroke();
      }
    };

    const frame = (now: number) => {
      const dt = Math.min(0.05, (now - last) / 1000);
      last = now;
      const p = state.current.p;
      if (!reduce) clock += dt * (1 + p * 1.2);
      const t = clock;
      mx += (tx - mx) * 0.05;
      my += (ty - my) * 0.05;

      drawAurora(t, 1 - 0.78 * p);

      ctx.clearRect(0, 0, W, H);
      // stars
      for (const s of stars) {
        const a = (0.35 + 0.65 * (0.5 + 0.5 * Math.sin(t * s.sp + s.ph))) * (1 - 0.6 * p);
        ctx.fillStyle = `rgba(222,232,255,${a.toFixed(3)})`;
        ctx.fillRect(s.x * W + mx * 8, s.y * H + p * H * 0.25 + my * 5, s.s, s.s);
      }

      drawRidge(LAYERS[0], p * H * LAYERS[0].par, mx * 10);

      // the bridge
      const b = bridgeOf();
      const grad = ctx.createLinearGradient(b.S.x, 0, b.E.x, 0);
      grad.addColorStop(0, "rgba(242,177,60,0.85)");
      grad.addColorStop(0.5, "rgba(95,211,166,0.55)");
      grad.addColorStop(1, "rgba(125,184,232,0.45)");
      const curve = () => {
        ctx.beginPath();
        ctx.moveTo(b.S.x, b.S.y);
        ctx.quadraticCurveTo(b.C.x, b.C.y, b.E.x, b.E.y);
      };
      ctx.lineCap = "round";
      curve();
      ctx.strokeStyle = grad;
      ctx.globalAlpha = 0.1;
      ctx.lineWidth = 12;
      ctx.stroke();
      ctx.globalAlpha = 0.85;
      ctx.lineWidth = 1.6;
      ctx.stroke();
      ctx.globalAlpha = 1;
      // hangers
      ctx.strokeStyle = "rgba(169,180,199,0.12)";
      ctx.lineWidth = 1;
      for (let i = 1; i < 46; i++) {
        const q = quad(b, i / 46);
        ctx.beginPath();
        ctx.moveTo(q.x, q.y);
        ctx.lineTo(q.x, q.y + 7 + 10 * Math.sin(i * 0.9 + 1));
        ctx.stroke();
      }
      // requests crossing it
      const speed = 1 + p * 2.5;
      nextDoom -= dt;
      if (nextDoom <= 0 && !reduce) {
        const cand = particles.find((q) => q.state === 0 && q.t < 0.3);
        if (cand) cand.state = 1;
        nextDoom = 6 + r() * 4;
      }
      for (const q of particles) {
        if (!reduce) {
          if (q.state === 2) q.age += dt;
          else q.t += dt * q.sp * speed;
        }
        if (q.state === 1 && q.t > 0.5) {
          q.state = 2;
          q.age = 0;
        }
        if (q.t > 1 || (q.state === 2 && q.age > 0.9)) {
          q.t = 0;
          q.state = 0;
          q.age = 0;
        }
        const c = quad(b, Math.min(q.t, 1));
        const x = c.x + c.nx * q.off;
        let y = c.y + c.ny * q.off - 2;
        let a = 0.9;
        let col = "255,236,200";
        if (q.state === 1) col = "240,112,90";
        if (q.state === 2) {
          y += q.age * q.age * 220;
          a = 0.9 * (1 - q.age / 0.9);
          col = "240,112,90";
        }
        if (q.t < 0.04) a *= q.t / 0.04;
        const tail = quad(b, Math.max(0, q.t - 0.012));
        ctx.strokeStyle = `rgba(${col},${(a * 0.4).toFixed(3)})`;
        ctx.lineWidth = q.r;
        ctx.beginPath();
        ctx.moveTo(tail.x + tail.nx * q.off, tail.y + tail.ny * q.off - 2);
        ctx.lineTo(x, y);
        ctx.stroke();
        ctx.fillStyle = `rgba(${col},${a.toFixed(3)})`;
        ctx.beginPath();
        ctx.arc(x, y, q.r, 0, 6.283);
        ctx.fill();
      }

      drawRidge(LAYERS[1], p * H * LAYERS[1].par, mx * 18);

      // watchtower and beacon
      const tw = b.tower;
      const bx = tw.x + mx * 18;
      ctx.fillStyle = "#0f1420";
      ctx.fillRect(bx - 5, tw.y - 36, 10, 40);
      ctx.beginPath();
      ctx.moveTo(bx - 9, tw.y - 36);
      ctx.lineTo(bx, tw.y - 46);
      ctx.lineTo(bx + 9, tw.y - 36);
      ctx.closePath();
      ctx.fill();
      ctx.fillRect(bx - 7, tw.y - 44, 14, 8);
      const pulse = 0.75 + 0.25 * Math.sin(t * 2);
      const by = tw.y - 40;
      ctx.save();
      ctx.translate(bx, by);
      const bl = W * (W < 760 ? 0.6 : 0.42);
      ctx.rotate((W < 760 ? -0.1 : -0.38) + Math.sin(t * 0.35) * 0.07);
      const beam = ctx.createLinearGradient(0, 0, bl, 0);
      beam.addColorStop(0, "rgba(242,177,60,0.16)");
      beam.addColorStop(1, "rgba(242,177,60,0)");
      ctx.fillStyle = beam;
      ctx.beginPath();
      ctx.moveTo(0, 0);
      ctx.lineTo(bl, -bl * 0.08);
      ctx.lineTo(bl, bl * 0.08);
      ctx.closePath();
      ctx.fill();
      ctx.restore();
      const halo = ctx.createRadialGradient(bx, by, 0, bx, by, 38);
      halo.addColorStop(0, `rgba(242,177,60,${(0.55 * pulse).toFixed(3)})`);
      halo.addColorStop(1, "rgba(242,177,60,0)");
      ctx.fillStyle = halo;
      ctx.fillRect(bx - 40, by - 40, 80, 80);
      ctx.fillStyle = "#f6c25b";
      ctx.beginPath();
      ctx.arc(bx, by, 2.6, 0, 6.283);
      ctx.fill();

      drawRidge(LAYERS[2], p * H * LAYERS[2].par, mx * 28);

      if (!reduce && visible) raf = requestAnimationFrame(frame);
    };

    size();
    const ro = new ResizeObserver(() => {
      size();
      if (reduce) frame(performance.now());
    });
    ro.observe(host);
    const io = new IntersectionObserver(([e]) => {
      const was = visible;
      visible = e.isIntersecting;
      if (visible && !was && !reduce) {
        last = performance.now();
        raf = requestAnimationFrame(frame);
      }
    });
    io.observe(host);
    const onMove = (e: PointerEvent) => {
      const rc = host.getBoundingClientRect();
      tx = (e.clientX - rc.left) / rc.width - 0.5;
      ty = (e.clientY - rc.top) / rc.height - 0.5;
    };
    host.addEventListener("pointermove", onMove);
    raf = requestAnimationFrame(frame);
    return () => {
      cancelAnimationFrame(raf);
      ro.disconnect();
      io.disconnect();
      host.removeEventListener("pointermove", onMove);
    };
  }, [state]);

  return (
    <div className="lp-sky" aria-hidden="true">
      <canvas ref={aurRef} className="lp-aurora" />
      <canvas ref={fxRef} className="lp-fx" />
    </div>
  );
}
