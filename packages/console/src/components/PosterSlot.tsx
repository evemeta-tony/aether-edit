// packages/console/src/components/PosterSlot.tsx
//
// Poster-frame slot for the source inspector. The panel map maps the poster to
// FT-3 GET /v1/jobs/{id}/poster (a frame extracted at probe time), but the
// orchestrator does not expose that route yet, so there is no server poster to
// show. Rather than fabricate one, this slot preserves the prototype's
// user-fillable behavior (drop or browse a still) and persists the dropped
// frame locally, keyed by jobId, so it survives reloads. This is the honest
// port of the prototype's <image-slot> persistence: when the FT-3 poster route
// lands, the server frame becomes the default and a drop overrides it.

import { useEffect, useRef, useState } from "react";
import { Icon } from "./Icons";

const STORE_PREFIX = "aether.console.poster.";

function loadPoster(jobId: string): string | null {
  try {
    return localStorage.getItem(STORE_PREFIX + jobId);
  } catch {
    return null;
  }
}

function savePoster(jobId: string, dataUrl: string): void {
  try {
    localStorage.setItem(STORE_PREFIX + jobId, dataUrl);
  } catch {
    // Storage full or unavailable: the poster simply does not persist.
  }
}

export function PosterSlot({ jobId, placeholder }: { jobId: string | null; placeholder: string }) {
  const [dataUrl, setDataUrl] = useState<string | null>(null);
  const [over, setOver] = useState(false);
  const inputRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    setDataUrl(jobId ? loadPoster(jobId) : null);
  }, [jobId]);

  const accept = (file: File | undefined) => {
    if (!file || !jobId || !file.type.startsWith("image/")) return;
    const reader = new FileReader();
    reader.onload = () => {
      const url = String(reader.result);
      setDataUrl(url);
      savePoster(jobId, url);
    };
    reader.readAsDataURL(file);
  };

  return (
    <div
      onClick={() => inputRef.current?.click()}
      onDragOver={(e) => {
        e.preventDefault();
        setOver(true);
      }}
      onDragLeave={() => setOver(false)}
      onDrop={(e) => {
        e.preventDefault();
        setOver(false);
        accept(e.dataTransfer.files[0]);
      }}
      style={{
        position: "absolute",
        inset: 0,
        cursor: "pointer",
        display: "grid",
        placeItems: "center",
        background: over ? "var(--blue-tint)" : "transparent",
        border: over ? "1px dashed var(--blue-500)" : "none",
      }}
    >
      {dataUrl ? (
        <img src={dataUrl} alt="" style={{ position: "absolute", inset: 0, width: "100%", height: "100%", objectFit: "cover" }} />
      ) : (
        <div style={{ display: "flex", flexDirection: "column", alignItems: "center", gap: 6, color: "var(--fg4)", pointerEvents: "none" }}>
          <Icon name="upload" size={16} color="var(--fg4)" />
          <span style={{ font: "var(--t-micro)", color: "var(--fg4)", textAlign: "center", padding: "0 10px" }}>{placeholder}</span>
        </div>
      )}
      <input
        ref={inputRef}
        type="file"
        accept="image/*"
        style={{ display: "none" }}
        onChange={(e) => accept(e.target.files?.[0] ?? undefined)}
      />
    </div>
  );
}
