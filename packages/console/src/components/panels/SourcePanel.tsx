// packages/console/src/components/panels/SourcePanel.tsx
//
// Source file inspector. Follows the job-queue selection. The probe mediainfo
// (container, codec, resolution, chroma, bitrate, duration, stream inventory)
// is source truth from FT-3, but the orchestrator does not currently expose the
// source probe over an HTTP route (GetSource is internal only), and the Job
// record carries no mediainfo. So every probe-derived row honestly renders the
// em dash until an FT-3 source/probe route lands, rather than fabricating the
// prototype's SRC constants (R10(b)). File size and duration likewise come only
// from real fields when present. The poster is a user-fillable slot (see
// PosterSlot) because no FT-3 poster route exists yet.

import { Panel, Row, Eb } from "../Parts";
import { Icon } from "../Icons";
import { PosterSlot } from "../PosterSlot";
import { EM_DASH } from "../../lib/format";
import type { Job } from "../../api/jobs";

export function SourcePanel({ job }: { job: Job | null }) {
  // The source object key is content-addressed; the display name is not on the
  // job record, so we show the object key's tail as the closest honest label.
  const fileLabel = job ? job.sourceObjectKey.split("/").pop() ?? job.sourceObjectKey : EM_DASH;

  return (
    <Panel
      title="Source file"
      style={{ flex: 1, minHeight: 0 }}
      bodyStyle={{ overflowY: "auto", flex: 1 }}
      right={<span style={{ font: "400 10px var(--font-mono)", color: "var(--fg3)" }}>{EM_DASH}</span>}
    >
      <div
        style={{
          position: "relative",
          aspectRatio: "16 / 9",
          borderRadius: "var(--r-xs)",
          overflow: "hidden",
          border: "1px solid var(--line)",
          background: "var(--bg-void)",
          marginBottom: 12,
        }}
      >
        <PosterSlot jobId={job?.id ?? null} placeholder="Drop a frame from this title" />
        <span
          style={{
            position: "absolute",
            left: 8,
            top: 8,
            pointerEvents: "none",
            font: "600 9px var(--font-sans)",
            letterSpacing: ".14em",
            textTransform: "uppercase",
            color: "#fff",
            background: "rgba(3,5,6,.7)",
            padding: "4px 7px",
            borderRadius: "var(--r-xs)",
          }}
        >
          Poster
        </span>
        <span
          style={{
            position: "absolute",
            right: 8,
            bottom: 7,
            pointerEvents: "none",
            font: "400 10px var(--font-mono)",
            color: "#fff",
            background: "rgba(3,5,6,.7)",
            padding: "3px 6px",
            borderRadius: "var(--r-xs)",
          }}
        >
          {EM_DASH}
        </span>
      </div>
      <div style={{ font: "400 12px var(--font-mono)", color: "#fff", marginBottom: 12, wordBreak: "break-all" }}>{fileLabel}</div>
      <Row label="Container" value={EM_DASH} />
      <Row label="Codec in" value={EM_DASH} />
      <Row label="Resolution" value={EM_DASH} />
      <Row label="Chroma" value={EM_DASH} />
      <Row label="Source rate" value={EM_DASH} />
      <Row label="Duration" value={EM_DASH} />
      <div style={{ height: 1, background: "var(--line)", margin: "12px 0" }} />
      <Eb style={{ marginBottom: 9 }}>Streams</Eb>
      <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
        {[
          ["video", "V · source"],
          ["audio-lines", "A · source"],
          ["captions", "S · timed text"],
        ].map(([ic, label]) => (
          <div key={label} style={{ display: "flex", alignItems: "center", gap: 8 }}>
            <Icon name={ic} size={13} color="var(--fg3)" />
            <span style={{ font: "var(--t-body-sm)", color: "var(--fg2)", flex: 1 }}>{label}</span>
            <span style={{ font: "400 11px var(--font-mono)", color: "var(--fg3)" }}>{EM_DASH}</span>
          </div>
        ))}
      </div>
    </Panel>
  );
}
