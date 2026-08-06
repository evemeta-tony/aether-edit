// packages/console/src/components/panels/DeliveryPanel.tsx
//
// Delivery targets. AM-10 scopes v1 delivery to three target types: media
// library, archive, and CDN package. The webhook target is OUT of v1 and is
// rendered explicitly disabled (never as a live target). The panel map maps
// targets and their health to FT-3 GET /v1/delivery-targets, but the
// orchestrator exposes no such route yet, so this panel does not fabricate host
// strings or health signals: the three allowed targets are listed as the
// supported delivery types with an unknown (idle) health dot until a
// delivery-targets route lands (R10). The add (+) affordance is disabled for the
// same reason.

import { Panel, Dot } from "../Parts";
import { Icon } from "../Icons";
import { EM_DASH } from "../../lib/format";

interface TargetType {
  name: string;
  proto: string;
  note: string;
  disabled?: boolean;
}

// The three AM-10 target types, plus the webhook shown explicitly disabled.
const TARGETS: TargetType[] = [
  { name: "Media library", proto: "API", note: "On complete" },
  { name: "CDN package", proto: "HLS", note: "Full ladder" },
  { name: "Archive", proto: "S3", note: "Mezzanine" },
  { name: "Notify (webhook)", proto: "HOOK", note: "Out of v1 (AM-10)", disabled: true },
];

// live is accepted for parity with the panel's queue-run-state binding, but no
// health signal is fabricated from it (no delivery-targets route exists yet).
export function DeliveryPanel({ live: _live }: { live: boolean }) {
  return (
    <Panel
      title="Delivery"
      style={{ flex: "none" }}
      right={
        <button
          className="ae-b"
          disabled
          title="Adding delivery targets is not available yet"
          style={{ background: "transparent", border: "none", cursor: "not-allowed", padding: 0 }}
        >
          <Icon name="plus" size={14} color="var(--fg4)" />
        </button>
      }
    >
      <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
        {TARGETS.map((d) => (
          <div
            key={d.name}
            style={{
              display: "flex",
              flexDirection: "column",
              gap: 5,
              paddingBottom: 10,
              borderBottom: "1px solid var(--line)",
              opacity: d.disabled ? 0.5 : 1,
            }}
          >
            <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
              {/* Health is unknown (no delivery-targets route): idle dot, never
                  a fabricated ok/warn signal. */}
              <Dot tone="idle" />
              <span style={{ font: "var(--t-label)", color: d.disabled ? "var(--fg4)" : "#fff", flex: 1 }}>{d.name}</span>
              <span style={{ font: "500 9px var(--font-sans)", letterSpacing: ".12em", color: d.disabled ? "var(--fg4)" : "var(--blue-400)" }}>
                {d.proto}
              </span>
            </div>
            <div style={{ display: "flex", gap: 8, font: "400 10px var(--font-mono)", color: "var(--fg4)" }}>
              <span style={{ flex: 1, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{EM_DASH}</span>
              <span style={{ color: "var(--fg3)" }}>{d.note}</span>
            </div>
          </div>
        ))}
      </div>
    </Panel>
  );
}
