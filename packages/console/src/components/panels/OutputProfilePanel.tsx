// packages/console/src/components/panels/OutputProfilePanel.tsx
//
// Output profile. Binds to the selected job's real FT-3 preset. Edits PATCH
// /v1/presets/{id} (shared-preset semantics: the edit applies to every job on
// that preset), and the control set is mapped onto the real model:
//   - container chips write the lowercase Container enum
//   - rate-control chips swap the quality slider (CRF) for the bitrate slider
//     (VBR/CBR), matching the prototype, and PATCH the consistent value fields
//     together (the server validates the merged result)
//   - GOP is in FRAMES (the real model), not gop-seconds
//   - encoder speed maps fast/medium/slow onto p6/p4/p2
// The derived rows (resolution, keyframe, formats-out, est. output) are computed
// from the preset and are absent (em dash) when an input is unknown; the
// prototype's magic-number estimator is not ported (R3, R10(b)). When EVE is on
// the pane locks (AM-11) and shows a lock badge instead of the controls.

import { useState } from "react";
import { Panel, Row, Eb, TChip, DragSlider } from "../Parts";
import { Icon } from "../Icons";
import { EM_DASH } from "../../lib/format";
import {
  CONTAINERS,
  RATE_CONTROLS,
  SPEED_CHIPS,
  codecLabel,
  estOutputLabel,
  keyframeLabel,
  resolutionLabel,
  speedChipFor,
} from "../../lib/preset";
import { patchPreset, type Container, type Preset, type PresetPatch, type RateControl } from "../../api/jobs";
import type { Job } from "../../api/jobs";
import type { EveState } from "./EvePanel";

export function OutputProfilePanel({
  preset,
  eve,
  job,
  onPatched,
}: {
  preset: Preset | null;
  eve: EveState;
  job: Job | null;
  onPatched: () => void;
}) {
  void job; // job duration would feed est. output; no FT-3 probe route yet.
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const patch = (body: PresetPatch) => {
    if (!preset) return;
    setBusy(true);
    setErr(null);
    (async () => {
      try {
        await patchPreset(preset.id, body);
        onPatched();
      } catch (e) {
        setErr(e instanceof Error ? e.message : "preset update failed");
      } finally {
        setBusy(false);
      }
    })();
  };

  const headerRight = (
    <span style={{ font: "var(--t-label)", color: "#fff", whiteSpace: "nowrap" }}>
      {eve.on ? "EVE · per title" : preset ? preset.name : EM_DASH}
    </span>
  );

  return (
    <Panel
      title="Output profile"
      style={{ flex: 1, minHeight: 0, opacity: eve.on ? 0.72 : 1 }}
      bodyStyle={{ overflowY: "auto", flex: 1 }}
      right={headerRight}
    >
      {eve.on ? (
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: 9,
            padding: "10px 11px",
            marginBottom: 14,
            borderRadius: "var(--r-xs)",
            background: "var(--blue-tint)",
            border: "1px solid var(--line)",
          }}
        >
          <Icon name="lock" size={14} color="var(--blue-400)" />
          <span style={{ font: "var(--t-body-sm)", color: "var(--fg2)" }}>Codec, ladder and rate control set by EVE</span>
        </div>
      ) : preset ? (
        <ManualControls preset={preset} busy={busy} patch={patch} />
      ) : (
        <div style={{ font: "var(--t-body-sm)", color: "var(--fg4)", marginBottom: 14 }}>
          No preset selected. Select a job to edit its output profile.
        </div>
      )}

      {err && <div style={{ font: "var(--t-micro)", color: "var(--err)", marginBottom: 8 }}>{err}</div>}

      <div style={{ height: 1, background: "var(--line)", margin: "0 0 8px" }} />
      <Row label="Codec" value={eve.on ? "EVE per title" : preset ? codecLabel(preset.videoCodec) : EM_DASH} />
      <Row label="Resolution" value={eve.on ? "Per-title ladder" : preset ? resolutionLabel(preset) : EM_DASH} />
      <Row label="Frame rate" value={eve.on ? "Source cadence" : EM_DASH} />
      <Row label="Keyframe" value={eve.on ? "Scene-aligned" : preset ? keyframeLabel(preset) : EM_DASH} />
      <Row label="Audio" value={eve.on ? "Loudness normalised" : EM_DASH} />
      <Row
        label="Formats out"
        value={eve.on ? eve.formats.join(" · ") || "none" : preset ? preset.container.toUpperCase() : EM_DASH}
      />
      {/* Est. output is derived from the preset bitrate and the source probe
          duration (R3). The FT-3 job record carries no duration and there is no
          probe route yet, so the duration input is unknown and the estimate is
          the em dash rather than a fabricated figure. */}
      <Row label="Est. output" value={eve.on ? EM_DASH : preset ? estOutputLabel(preset, null) : EM_DASH} />
    </Panel>
  );
}

function ManualControls({ preset, busy, patch }: { preset: Preset; busy: boolean; patch: (b: PresetPatch) => void }) {
  const setContainer = (c: Container) => patch({ container: c });
  const setRateControl = (rc: RateControl) => {
    // A rate-control change must carry consistent value fields (the server
    // validates the merged preset). Provide sensible defaults per mode.
    if (rc === "crf") patch({ rateControl: "crf", crf: preset.crf && preset.crf > 0 ? preset.crf : 21 });
    else patch({ rateControl: rc, bitrateKbps: preset.bitrateKbps && preset.bitrateKbps > 0 ? preset.bitrateKbps : 8000 });
  };

  const crf = preset.crf ?? 21;
  const bitrateMbps = (preset.bitrateKbps ?? 8000) / 1000;

  return (
    <div style={{ pointerEvents: busy ? "none" : "auto", opacity: busy ? 0.6 : 1 }}>
      <Eb style={{ marginBottom: 11 }}>Container</Eb>
      <div style={{ display: "flex", gap: 6, marginBottom: 16, flexWrap: "wrap" }}>
        {CONTAINERS.map((c) => (
          <TChip key={c.id} active={preset.container === c.id} onClick={() => setContainer(c.id)}>
            {c.label}
          </TChip>
        ))}
      </div>
      <Eb style={{ marginBottom: 11 }}>Rate control</Eb>
      <div style={{ display: "flex", gap: 6, marginBottom: 16 }}>
        {RATE_CONTROLS.map((m) => (
          <TChip key={m.id} active={preset.rateControl === m.id} onClick={() => setRateControl(m.id)}>
            {m.label}
          </TChip>
        ))}
      </div>
      <div style={{ display: "flex", flexDirection: "column", gap: 14 }}>
        {preset.rateControl === "crf" ? (
          <DragSlider label="Quality · CRF" value={crf} min={0} max={51} step={1} unit="" onChange={(v) => patch({ crf: Math.round(v) })} />
        ) : (
          <DragSlider
            label="Target bitrate"
            value={bitrateMbps}
            min={0.5}
            max={40}
            step={0.5}
            unit="Mb/s"
            onChange={(v) => patch({ bitrateKbps: Math.round(v * 1000) })}
          />
        )}
        <DragSlider
          label="GOP length"
          value={preset.gopLength}
          min={1}
          max={600}
          step={1}
          unit="frames"
          onChange={(v) => patch({ gopLength: Math.round(v) })}
        />
      </div>
      <div style={{ height: 1, background: "var(--line)", margin: "16px 0 12px" }} />
      <Eb style={{ marginBottom: 11 }}>Encoder speed</Eb>
      <div style={{ display: "flex", gap: 5, marginBottom: 6 }}>
        {SPEED_CHIPS.map((s) => (
          <TChip key={s.id} active={speedChipFor(preset.speedPreset) === s.id} onClick={() => patch({ speedPreset: s.id })}>
            {s.label}
          </TChip>
        ))}
      </div>
      <div style={{ display: "flex", justifyContent: "space-between", font: "var(--t-micro)", color: "var(--fg4)", marginBottom: 16 }}>
        <span>Cheapest</span>
        <span>Smallest file</span>
      </div>
    </div>
  );
}
