// packages/console/src/components/Parts.tsx
//
// Shared console primitives ported from docs/design/ui_kits/aether-live/
// Parts.jsx: Panel, Dot, Read, Meter, TChip, DragSlider, Row, Graph, Eb,
// ConsoleCrumb. Presentational only; all data bindings live in the panels.
//
// Removed per R10(b): stamp() (timestamps now come from event "at" fields) and
// jit() (the simulation jitter helper). Neither survives into product code.

import type { CSSProperties, ReactNode } from "react";
import { useRef } from "react";
import { Icon, Mark } from "./Icons";

export type Tone = "ok" | "warn" | "err" | "idle" | "onair";

export function Eb({ children, style }: { children: ReactNode; style?: CSSProperties }) {
  return (
    <div
      style={{
        font: "var(--t-eyebrow)",
        letterSpacing: "var(--ls-eyebrow)",
        textTransform: "uppercase",
        color: "var(--blue-400)",
        ...style,
      }}
    >
      {children}
    </div>
  );
}

export function Panel({
  title,
  right,
  children,
  style,
  bodyStyle,
}: {
  title: string;
  right?: ReactNode;
  children: ReactNode;
  style?: CSSProperties;
  bodyStyle?: CSSProperties;
}) {
  return (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        background: "var(--bg-panel)",
        border: "1px solid var(--line)",
        borderRadius: "var(--r-md)",
        minHeight: 0,
        ...style,
      }}
    >
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 10,
          padding: "10px 13px",
          borderBottom: "1px solid var(--line)",
          flex: "none",
        }}
      >
        <span
          style={{
            font: "var(--t-eyebrow)",
            letterSpacing: "var(--ls-eyebrow)",
            textTransform: "uppercase",
            color: "var(--fg3)",
          }}
        >
          {title}
        </span>
        <div style={{ flex: 1 }} />
        {right}
      </div>
      <div style={{ padding: 13, minHeight: 0, ...bodyStyle }}>{children}</div>
    </div>
  );
}

export function Dot({ tone = "ok", pulse }: { tone?: Tone; pulse?: boolean }) {
  const c = {
    ok: "var(--ok)",
    warn: "var(--warn)",
    err: "var(--err)",
    idle: "var(--idle)",
    onair: "var(--onair)",
  }[tone];
  return (
    <span
      style={{
        width: 7,
        height: 7,
        borderRadius: "50%",
        background: c,
        flex: "none",
        boxShadow: tone === "onair" ? "0 0 0 3px rgba(229,51,78,.22)" : "none",
        animation: pulse ? "ae-pulse var(--dur-glow) var(--ease-std) infinite" : "none",
      }}
    />
  );
}

// Read: mono value + tracked unit caption. The house numeral readout.
export function Read({
  value,
  unit,
  label,
  tone,
  size = 19,
}: {
  value: string;
  unit?: string;
  label: string;
  tone?: string;
  size?: number;
}) {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 4, minWidth: 0 }}>
      <div style={{ display: "flex", alignItems: "baseline", gap: 4 }}>
        <span
          style={{
            font: `500 ${size}px var(--font-mono)`,
            color: tone || "#fff",
            letterSpacing: "-.01em",
            fontVariantNumeric: "tabular-nums",
          }}
        >
          {value}
        </span>
        {unit && <span style={{ font: "var(--t-micro)", color: "var(--fg3)" }}>{unit}</span>}
      </div>
      <span
        style={{
          font: "500 9px var(--font-sans)",
          letterSpacing: ".13em",
          textTransform: "uppercase",
          color: "var(--fg4)",
          whiteSpace: "nowrap",
        }}
      >
        {label}
      </span>
    </div>
  );
}

export function Meter({ pct, color = "var(--blue-500)", h = 3 }: { pct: number; color?: string; h?: number }) {
  return (
    <div style={{ height: h, background: "var(--bg-input)", borderRadius: 1, overflow: "hidden" }}>
      <div
        style={{
          height: "100%",
          width: `${Math.min(100, Math.max(0, pct))}%`,
          background: color,
          transition: "width .45s linear",
        }}
      />
    </div>
  );
}

export function TChip({
  children,
  active,
  onClick,
}: {
  children: ReactNode;
  active?: boolean;
  onClick?: () => void;
}) {
  return (
    <button
      className="ae-b"
      onClick={onClick}
      style={{
        font: "600 10px var(--font-sans)",
        letterSpacing: ".09em",
        textTransform: "uppercase",
        padding: "6px 10px",
        borderRadius: "var(--r-xs)",
        cursor: "pointer",
        whiteSpace: "nowrap",
        background: active ? "var(--blue-tint)" : "transparent",
        border: `1px solid ${active ? "var(--blue-500)" : "var(--line-strong)"}`,
        color: active ? "var(--blue-300)" : "var(--fg2)",
      }}
    >
      {children}
    </button>
  );
}

// DragSlider: a real pointer-driven slider (ported verbatim from Parts.jsx).
export function DragSlider({
  label,
  value,
  min,
  max,
  step = 0.1,
  unit,
  onChange,
}: {
  label: string;
  value: number;
  min: number;
  max: number;
  step?: number;
  unit?: string;
  onChange: (v: number) => void;
}) {
  const ref = useRef<HTMLDivElement | null>(null);
  const pct = ((value - min) / (max - min)) * 100;
  const grab = (e: React.PointerEvent) => {
    const el = ref.current;
    if (!el) return;
    const set = (clientX: number) => {
      const r = el.getBoundingClientRect();
      const t = Math.max(0, Math.min(1, (clientX - r.left) / r.width));
      onChange(Math.round((min + t * (max - min)) / step) * step);
    };
    set(e.clientX);
    const move = (ev: PointerEvent) => set(ev.clientX);
    const up = () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
  };
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "baseline" }}>
        <span
          style={{
            font: "500 10px var(--font-sans)",
            letterSpacing: ".13em",
            textTransform: "uppercase",
            color: "var(--fg3)",
          }}
        >
          {label}
        </span>
        <span style={{ font: "400 12px var(--font-mono)", color: "#fff", fontVariantNumeric: "tabular-nums" }}>
          {value.toFixed(step < 1 ? 1 : 0)}
          <span style={{ color: "var(--fg3)" }}> {unit}</span>
        </span>
      </div>
      <div
        ref={ref}
        onPointerDown={grab}
        style={{ height: 22, display: "flex", alignItems: "center", cursor: "ew-resize", touchAction: "none" }}
      >
        <div style={{ position: "relative", width: "100%", height: 2, background: "var(--bg-hover)" }}>
          <div style={{ position: "absolute", left: 0, top: 0, bottom: 0, width: `${pct}%`, background: "var(--blue-500)" }} />
          <div
            style={{
              position: "absolute",
              left: `calc(${pct}% - 5px)`,
              top: -4,
              width: 10,
              height: 10,
              background: "#fff",
              borderRadius: 1,
            }}
          />
        </div>
      </div>
    </div>
  );
}

export function Row({ label, value, tone }: { label: string; value: ReactNode; tone?: string }) {
  return (
    <div style={{ display: "flex", justifyContent: "space-between", alignItems: "baseline", gap: 10, minHeight: 26 }}>
      <span
        style={{
          font: "500 10px var(--font-sans)",
          letterSpacing: ".13em",
          textTransform: "uppercase",
          color: "var(--fg3)",
        }}
      >
        {label}
      </span>
      <span
        style={{
          font: "400 12px var(--font-mono)",
          color: tone || "#fff",
          fontVariantNumeric: "tabular-nums",
          textAlign: "right",
        }}
      >
        {value}
      </span>
    </div>
  );
}

// Graph: a fixed-length sparkline. data is a ring buffer held by the caller and
// fed from real stream cadence (no random walk).
export function Graph({ data, max, live }: { data: number[]; max: number; live: boolean }) {
  const W = 100;
  const H = 100;
  const safeMax = max > 0 ? max : 1;
  const pts =
    data.length > 1
      ? data.map((v, i) => `${(i / (data.length - 1)) * W},${H - (v / safeMax) * H}`).join(" ")
      : "";
  return (
    <div
      style={{
        position: "relative",
        height: 76,
        background: "var(--bg-void)",
        border: "1px solid var(--line)",
        borderRadius: "var(--r-xs)",
        overflow: "hidden",
      }}
    >
      {[25, 50, 75].map((p) => (
        <div key={p} style={{ position: "absolute", left: 0, right: 0, top: `${p}%`, height: 1, background: "rgba(255,255,255,.05)" }} />
      ))}
      {pts && (
        <svg viewBox="0 0 100 100" preserveAspectRatio="none" style={{ position: "absolute", inset: 0, width: "100%", height: "100%" }}>
          <polygon points={`0,100 ${pts} 100,100`} fill="rgba(47,107,246,.16)" />
          <polyline points={pts} fill="none" stroke={live ? "var(--blue-400)" : "var(--idle)"} strokeWidth="1" vectorEffect="non-scaling-stroke" />
        </svg>
      )}
      <span style={{ position: "absolute", right: 7, top: 5, font: "400 9px var(--font-mono)", color: "var(--fg4)" }}>
        {max.toFixed(0)} fps
      </span>
      <span style={{ position: "absolute", left: 7, bottom: 4, font: "400 9px var(--font-mono)", color: "var(--fg4)" }}>
        {"\u221260 s"}
      </span>
    </div>
  );
}

export interface CrumbItem {
  label: string;
  mono?: boolean;
}

// ConsoleCrumb: wordmark plus breadcrumb head (ported from Parts.jsx). The
// workspace crumb is now data-driven from the tenancy context.
export function ConsoleCrumb({ trail }: { trail: CrumbItem[] }) {
  return (
    <>
      <div style={{ display: "flex", alignItems: "center", gap: 9 }}>
        <Mark size={19} />
        <span style={{ font: "600 12px var(--font-sans)", letterSpacing: "var(--ls-wordmark)", color: "#fff" }}>AETHER</span>
        <span style={{ font: "500 11px var(--font-sans)", letterSpacing: ".14em", textTransform: "uppercase", color: "var(--fg3)" }}>
          Cloud
        </span>
      </div>
      <div style={{ width: 1, height: 22, background: "var(--line)" }} />
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 7,
          font: "var(--t-body-sm)",
          color: "var(--fg3)",
          whiteSpace: "nowrap",
          flex: "none",
        }}
      >
        {trail.map((t, i) => (
          <span key={i} style={{ display: "flex", alignItems: "center", gap: 7 }}>
            {i > 0 && <Icon name="chevron-right" size={12} />}
            <span
              style={{
                whiteSpace: "nowrap",
                ...(i === trail.length - 1
                  ? { color: "#fff", font: "var(--t-label)" }
                  : t.mono
                    ? { fontFamily: "var(--font-mono)", color: "var(--fg2)" }
                    : {}),
              }}
            >
              {t.label}
            </span>
          </span>
        ))}
      </div>
    </>
  );
}
