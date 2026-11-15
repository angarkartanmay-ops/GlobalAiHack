import { useEffect, useState } from "react";

/** One retiring model on the chart. `days` is days until shutdown; negative means it has already been shut down. */
export interface Sun {
  id: string;
  label: string;
  days: number;
  share: number;
}

function useNarrow(): boolean {
  const query = "(max-width: 720px)";
  const [narrow, setNarrow] = useState(() => typeof window !== "undefined" && window.matchMedia(query).matches);
  useEffect(() => {
    const mq = window.matchMedia(query);
    const on = () => setNarrow(mq.matches);
    mq.addEventListener("change", on);
    return () => mq.removeEventListener("change", on);
  }, []);
  return narrow;
}

const pctText = (share: number) => {
  const v = share * 100;
  return v > 0 && v < 1 ? "<1%" : `${Math.round(v)}%`;
};

interface Box { x: number; y: number; w: number; h: number }
const hit = (a: Box, b: Box) => a.x < b.x + b.w && a.x + a.w > b.x && a.y < b.y + b.h && a.y + a.h > b.y;

/**
 * The sunset chart. Each model is a sun on its way down to the horizon: the
 * further left and lower it sits, the fewer days it has. Size is the share of
 * requests it carries. Models that have already been shut down sit below the
 * horizon. `elapsed` moves time forward, for the landing page.
 */
export function Sunset({ suns, onOpen, elapsed = 0, tall = false, width = 1000 }: { suns: Sun[]; onOpen?: (id: string) => void; elapsed?: number; tall?: boolean; width?: number }) {
  const narrow = useNarrow();
  const W = narrow ? 560 : width;
  const band = suns.filter((s) => s.days - elapsed < 0).length > 1 || tall ? 112 : 84;
  const H = (tall ? (narrow ? 380 : 330) : narrow ? 340 : 300) + (band - 84);
  const horizonY = H - band;
  const x0 = narrow ? 184 : Math.round(W * 0.236);
  const x1 = W - (narrow ? 26 : 44);
  const top = 40;

  const live = suns.map((s) => ({ ...s, d: s.days - elapsed }));
  const furthest = Math.max(0, ...suns.map((s) => s.days));
  const maxDays = Math.min(365, Math.max(120, Math.ceil(furthest / 30) * 30 + 15));
  const xOf = (d: number) => x0 + (Math.min(d, maxDays) / maxDays) * (x1 - x0);
  const yOf = (d: number) => horizonY - (horizonY - top) * Math.pow(Math.min(d, maxDays) / maxDays, 0.62);
  const rOf = (share: number) => (narrow ? 5 : 7) + (narrow ? 13 : 17) * Math.sqrt(Math.max(0, share));

  const ticks: number[] = [];
  for (let d = 30; d <= maxDays; d += maxDays > 200 || narrow ? 60 : 30) ticks.push(d);

  const arc = Array.from({ length: 49 }, (_, i) => {
    const d = (i / 48) * maxDays;
    return `${i === 0 ? "M" : "L"}${xOf(d).toFixed(1)},${yOf(d).toFixed(1)}`;
  }).join(" ");

  // Suns that have set take a slot below the horizon, most recent first.
  const set = live.filter((s) => s.d < 0).sort((a, b) => b.d - a.d);
  const up = live.filter((s) => s.d >= 0).sort((a, b) => a.d - b.d);
  const slot = (i: number) => ({ x: x0 - (narrow ? 30 : 44) - i * 6, y: horizonY + 30 + i * 38 });

  const placedSuns = [
    ...set.map((s, i) => {
      const to = slot(i);
      const k = Math.min(1, -s.d / 5); // sink from the horizon into the slot over five days
      return { s, x: x0 + (to.x - x0) * k, y: horizonY + (to.y - horizonY) * k, r: Math.min(rOf(s.share), 15), isSet: true };
    }),
    ...up.map((s) => ({ s, x: xOf(s.d), y: yOf(s.d), r: rOf(s.share), isSet: false })),
  ];

  // Labels: try right, then left, then above; take the first spot that is clear of every sun and every earlier label.
  const sunBoxes: Box[] = placedSuns.map((p) => ({ x: p.x - p.r - 3, y: p.y - p.r - 3, w: p.r * 2 + 6, h: p.r * 2 + 6 }));
  const taken: Box[] = [];
  const labels = placedSuns.map((p, i) => {
    const d = Math.round(p.s.d);
    const sub = p.isSet ? `shut down ${-d === 0 ? "today" : `${-d} d ago`}` : `${d === 0 ? "today" : d === 1 ? "1 day" : `${d} days`} · ${pctText(p.s.share)} of requests`;
    const w = Math.max(p.s.label.length * 7.9, sub.length * 6.2) + 4;
    const h = 34;
    const options: { x: number; y: number; anchor: "start" | "end" | "middle"; box: Box }[] = p.isSet
      ? [{ x: p.x - p.r - 9, y: p.y - 3, anchor: "end", box: { x: p.x - p.r - 9 - w, y: p.y - 17, w, h } }]
      : [
          { x: p.x + p.r + 9, y: p.y - 3, anchor: "start", box: { x: p.x + p.r + 9, y: p.y - 17, w, h } },
          { x: p.x - p.r - 9, y: p.y - 3, anchor: "end", box: { x: p.x - p.r - 9 - w, y: p.y - 17, w, h } },
          { x: p.x, y: p.y - p.r - 26, anchor: "middle", box: { x: p.x - w / 2, y: p.y - p.r - 40, w, h } },
        ];
    const clear = (b: Box) =>
      b.x >= 2 && b.x + b.w <= W - 2 && b.y >= 2 && (p.isSet || b.y + b.h <= horizonY - 2) &&
      !taken.some((t) => hit(t, b)) && !sunBoxes.some((sb, j) => j !== i && hit(sb, b));
    const pick = options.find((o) => clear(o.box)) ?? options[0];
    taken.push(pick.box);
    return { ...pick, sub };
  });

  const fillOf = (d: number) => (d < 0 ? "var(--ember)" : d <= 14 ? "var(--ember)" : d <= 45 ? "var(--sun)" : "var(--sun-far)");
  const glow = Math.min(1, 0.55 + elapsed / 60);

  return (
    <svg className="sunset" viewBox={`0 0 ${W} ${H}`} role="group" aria-label="Models by days until shutdown">
      <defs>
        <radialGradient id="sunset-glow" gradientUnits="userSpaceOnUse" cx={x0} cy={horizonY} r={narrow ? 300 : Math.round(W * 0.47)}>
          <stop offset="0" stopColor="#e2622c" stopOpacity={0.5 * glow} />
          <stop offset="0.42" stopColor="#eaa24a" stopOpacity={0.24 * glow} />
          <stop offset="1" stopColor="#eaa24a" stopOpacity="0" />
        </radialGradient>
      </defs>
      <rect x="0" y="0" width={W} height={horizonY} fill="var(--sheet)" />
      <rect x="0" y="0" width={W} height={horizonY} fill="url(#sunset-glow)" />
      <path d={arc} fill="none" stroke="var(--ink-3)" strokeWidth="1" strokeDasharray="1 6" strokeLinecap="round" />

      <rect x="0" y={horizonY} width={W} height={band} fill="var(--night)" />
      <line x1="0" y1={horizonY} x2={W} y2={horizonY} stroke="var(--ink)" strokeWidth="2" />
      <line x1={x0} y1={horizonY - 9} x2={x0} y2={horizonY + 9} stroke="var(--paper)" strokeWidth="1.5" />
      <text className="sunset-tick now" x={x0} y={H - 12} textAnchor="middle">Today</text>
      {ticks.map((d) => (
        <g key={d}>
          <line x1={xOf(d)} y1={horizonY} x2={xOf(d)} y2={horizonY + 7} stroke="var(--paper)" strokeWidth="1" opacity="0.55" />
          <text className="sunset-tick" x={xOf(d)} y={H - 12} textAnchor="middle">{d} d</text>
        </g>
      ))}

      {placedSuns.map((p, i) => {
        const l = labels[i];
        const d = Math.round(p.s.d);
        const when = p.isSet ? `shut down ${-d} days ago` : `${d} days left`;
        const interactive = !!onOpen;
        return (
          <g key={p.s.id} className={`sunset-sun${p.isSet ? " set" : ""}${interactive ? " link" : ""}`} style={{ animationDelay: `${i * 90}ms` }}
            role={interactive ? "button" : undefined} tabIndex={interactive ? 0 : undefined}
            aria-label={`${p.s.id}, ${when}, ${pctText(p.s.share)} of requests`}
            onClick={interactive ? () => onOpen(p.s.id) : undefined}
            onKeyDown={interactive ? (ev) => { if (ev.key === "Enter" || ev.key === " ") { ev.preventDefault(); onOpen(p.s.id); } } : undefined}>
            <circle className="core" cx={p.x} cy={p.y} r={p.r} fill={fillOf(p.s.d)} stroke={p.isSet ? "var(--paper)" : "var(--ink)"} strokeWidth="1.5" />
            <text className="sunset-name" x={l.x} y={l.y} textAnchor={l.anchor}>{p.s.label}</text>
            <text className="sunset-sub" x={l.x} y={l.y + 16} textAnchor={l.anchor}>{l.sub}</text>
          </g>
        );
      })}
    </svg>
  );
}
