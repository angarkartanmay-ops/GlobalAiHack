// Scroll motion for the landing page. GSAP and Lenis live here and nowhere
// else: the dashboard uses plain CSS and IntersectionObserver.
import { useLayoutEffect, type RefObject } from "react";
import gsap from "gsap";
import { ScrollTrigger } from "gsap/ScrollTrigger";
import Lenis from "lenis";

gsap.registerPlugin(ScrollTrigger);
export { gsap, ScrollTrigger };

// Palette mirrors styles.css, for tweens that need literal colours.
export const COLOR = {
  ground: "#131926",
  surface: "#1c2535",
  line: "#2a3549",
  text: "#f2f4f8",
  text2: "#a9b4c7",
  text3: "#8894a8",
  amber: "#f2b13c",
  green: "#5fd3a6",
  blue: "#7db8e8",
  ember: "#f0705a",
};

export type Mode = "full" | "compact" | "reduced";

const MQ = {
  full: "(min-width: 900px) and (prefers-reduced-motion: no-preference)",
  compact: "(max-width: 899px) and (prefers-reduced-motion: no-preference)",
  reduced: "(prefers-reduced-motion: reduce)",
};

let lenis: Lenis | null = null;

/** Starts inertial scrolling, wired into ScrollTrigger. Returns its cleanup. */
export function startSmoothScroll(): () => void {
  if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) return () => {};
  const l = new Lenis({ lerp: 0.09, wheelMultiplier: 0.95 });
  lenis = l;
  l.on("scroll", ScrollTrigger.update);
  const tick = (t: number) => l.raf(t * 1000);
  gsap.ticker.add(tick);
  gsap.ticker.lagSmoothing(0);
  return () => {
    gsap.ticker.remove(tick);
    l.destroy();
    if (lenis === l) lenis = null;
  };
}

export function scrollToId(id: string) {
  const el = document.getElementById(id);
  if (!el) return;
  if (lenis) lenis.scrollTo(el, { offset: 0, duration: 1.5 });
  else el.scrollIntoView({ behavior: "smooth" });
}

export interface SceneCtx {
  tl: gsap.core.Timeline;
  q: gsap.utils.SelectorFunc;
  mode: Mode;
  el: HTMLElement;
}

export interface SceneOptions {
  /** Scroll distance the pinned scene lasts, as a ScrollTrigger end value. */
  end?: string;
  /** Skip the timeline on small screens, leaving the final layout visible. */
  staticOnCompact?: boolean;
}

/**
 * Builds one scroll scene.
 *
 *  full     wide screens: the section pins and the timeline scrubs with scroll
 *  compact  phones: no pinning; the timeline plays once when the section enters
 *  reduced  prefers-reduced-motion: the timeline jumps to its final frame
 *
 * Tweens should be written as from/fromTo so the final frame is the natural
 * layout, which is what compact and reduced users see.
 */
export function useScene(ref: RefObject<HTMLElement | null>, build: (c: SceneCtx) => void | (() => void), opts: SceneOptions = {}) {
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const mm = gsap.matchMedia();
    mm.add(MQ, (ctx) => {
      const c = ctx.conditions as Record<Mode, boolean>;
      const mode: Mode = c.reduced ? "reduced" : c.full ? "full" : "compact";
      if (mode === "compact" && opts.staticOnCompact) return;
      const tl = gsap.timeline({ paused: true, defaults: { ease: "none" } });
      const cleanup = build({ tl, q: gsap.utils.selector(el), mode, el });
      if (mode === "reduced") {
        tl.progress(1);
        return cleanup;
      }
      if (mode === "full") {
        ScrollTrigger.create({
          trigger: el,
          start: "top top",
          end: opts.end ?? "+=260%",
          pin: true,
          scrub: 0.8,
          animation: tl,
          anticipatePin: 1,
          invalidateOnRefresh: true,
        });
      } else {
        ScrollTrigger.create({
          trigger: el,
          start: "top 72%",
          once: true,
          onEnter: () => {
            tl.timeScale(Math.max(1, tl.duration() / 4.5)).play();
          },
        });
      }
      return cleanup;
    });
    return () => mm.revert();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
}

/** Fades blocks in as they enter, in a stagger. For sections that do not pin. */
export function useReveal(ref: RefObject<HTMLElement | null>, selector: string, opts: { y?: number; stagger?: number } = {}) {
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const mm = gsap.matchMedia();
    mm.add("(prefers-reduced-motion: no-preference)", () => {
      const items = gsap.utils.toArray<HTMLElement>(selector, el);
      gsap.from(items, {
        opacity: 0,
        y: opts.y ?? 28,
        duration: 0.9,
        ease: "power3.out",
        stagger: opts.stagger ?? 0.08,
        scrollTrigger: { trigger: el, start: "top 78%", once: true },
      });
    });
    return () => mm.revert();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
}
