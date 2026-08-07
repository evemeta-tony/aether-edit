// packages/console/src/components/panels/EvePanel.tsx
//
// EVE panel (AM-11). The toggle is live: turning it on locks the output-profile
// pane (handled by OutputProfilePanel) and shows the derived EVE rows, while
// the delivery formats stay a multi-select. Honest absence (C1): no EVE adapter
// exists yet (U3), so an EVE-mode job submission is NOT silently mapped to the
// manual profile. Instead the panel surfaces an explicit "eve-pending" banner,
// making clear that EVE selections are captured but cannot be executed until the
// analysis service lands. The steps list carries no fabricated per-title status
// (there is no analysis engine to report it); each step shows a neutral pending
// dot until FT-3 exposes real pipeline stage status.

import { Panel, Eb, Dot, TChip } from "../Parts";

export interface EveState {
  on: boolean;
  formats: string[];
  // pending is set when EVE is on but no adapter can execute it (always true
  // today, until U3 lands). Surfaced explicitly to the operator.
  pending: boolean;
}

const EVE_FORMATS = ["HLS", "DASH", "MP4", "CMAF"];
const EVE_STEPS: [string, string][] = [
  ["Source analysis", "grain, motion, cadence"],
  ["Per-title ladder", "rungs fitted to content"],
  ["Quality target", "VMAF target per rung"],
  ["Packaging", "segmented + encrypted"],
];

export function EvePanel({ state, onChange }: { state: EveState; onChange: (s: EveState) => void }) {
  const toggle = () => onChange({ ...state, on: !state.on, pending: !state.on });
  const toggleFormat = (f: string) =>
    onChange({
      ...state,
      formats: state.formats.includes(f) ? state.formats.filter((x) => x !== f) : [...state.formats, f],
    });

  return (
    <Panel
      title="EVE"
      style={{ flex: "none" }}
      right={
        <button
          className="ae-b"
          onClick={toggle}
          aria-pressed={state.on}
          style={{
            width: 34,
            height: 19,
            borderRadius: 999,
            cursor: "pointer",
            position: "relative",
            padding: 0,
            background: state.on ? "var(--blue-500)" : "var(--bg-hover)",
            border: "none",
            flex: "none",
          }}
        >
          <span
            style={{
              position: "absolute",
              top: 2.5,
              left: state.on ? 17.5 : 2.5,
              width: 14,
              height: 14,
              borderRadius: "50%",
              background: "#fff",
              transition: "left var(--dur-fast) var(--ease-std)",
            }}
          />
        </button>
      }
    >
      <div style={{ display: "flex", alignItems: "baseline", gap: 8, marginBottom: 10 }}>
        <span style={{ font: "var(--t-label)", color: "#fff" }}>Encoding · Verified · Efficient</span>
        <span
          style={{
            font: "var(--t-micro)",
            color: state.on ? "var(--blue-400)" : "var(--fg4)",
            marginLeft: "auto",
            whiteSpace: "nowrap",
          }}
        >
          {state.on ? "Automated" : "Manual profile"}
        </span>
      </div>
      {state.on ? (
        <>
          {state.pending && (
            <div
              style={{
                display: "flex",
                alignItems: "center",
                gap: 8,
                padding: "8px 10px",
                marginBottom: 12,
                borderRadius: "var(--r-xs)",
                background: "rgba(245,165,36,.10)",
                border: "1px solid var(--warn)",
              }}
            >
              <Dot tone="warn" />
              <span style={{ font: "var(--t-micro)", color: "var(--warn)" }}>
                EVE pending: no analysis adapter yet. Jobs submitted in EVE mode enter an explicit eve-pending state and are never
                silently run on the manual profile.
              </span>
            </div>
          )}
          <div style={{ display: "flex", flexDirection: "column", gap: 9, marginBottom: 14 }}>
            {EVE_STEPS.map(([label, note]) => (
              <div key={label} style={{ display: "flex", alignItems: "center", gap: 9 }}>
                {/* Neutral pending dot: no analysis engine reports real stage
                    status, so no ok/complete state is fabricated (R10). */}
                <Dot tone="idle" />
                <span style={{ font: "var(--t-body-sm)", color: "#fff", whiteSpace: "nowrap" }}>{label}</span>
                <span
                  style={{
                    font: "var(--t-micro)",
                    color: "var(--fg4)",
                    flex: 1,
                    textAlign: "right",
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                    whiteSpace: "nowrap",
                  }}
                >
                  {note}
                </span>
              </div>
            ))}
          </div>
          <Eb style={{ marginBottom: 10 }}>Delivery formats</Eb>
          <div style={{ display: "flex", gap: 6, flexWrap: "wrap" }}>
            {EVE_FORMATS.map((f) => (
              <TChip key={f} active={state.formats.includes(f)} onClick={() => toggleFormat(f)}>
                {f}
              </TChip>
            ))}
          </div>
          <div style={{ font: "var(--t-micro)", color: "var(--fg4)", marginTop: 9 }}>
            Everything else is decided per title. You pick what comes out.
          </div>
        </>
      ) : (
        <div style={{ font: "var(--t-body-sm)", color: "var(--fg3)", lineHeight: 1.5 }}>
          Off. Jobs use the output profile below, exactly as configured.
        </div>
      )}
    </Panel>
  );
}
