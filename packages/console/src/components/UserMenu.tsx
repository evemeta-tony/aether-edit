// packages/console/src/components/UserMenu.tsx
//
// Account menu, ported from Parts.jsx but data-driven from the FT-6a tenancy
// session (panel map section 6). The hardcoded identity, workspace list, plan
// row, and quota meter are replaced by /v1/me, /v1/workspaces, and /v1/usage.
// The encode-hours meter is derived from the usage rollup (encodeHoursUsed)
// against the tier limit (encodeHoursLimit); when the limit is unknown it shows
// used hours only, never a fabricated cap. The workspace switcher calls
// POST /v1/workspaces/switch and reloads with the new active workspace token.
// Account settings / Billing targets are U2 (no service yet), so they are
// rendered explicitly disabled rather than as live links.

import { useEffect, useRef, useState } from "react";
import { Icon } from "./Icons";
import { Dot, Eb, Meter, Row } from "./Parts";
import { EM_DASH, fmtInt, fmtNum } from "../lib/format";
import type { SessionState } from "../hooks/useSession";
import { logout } from "../api/tenancy";
import { clearAccessToken } from "../api/session";

function initials(name: string): string {
  return name
    .split(" ")
    .map((s) => s[0])
    .filter(Boolean)
    .slice(0, 2)
    .join("");
}

export function UserMenu({ session }: { session: SessionState }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    const onDoc = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", onDoc);
    return () => document.removeEventListener("mousedown", onDoc);
  }, []);

  const user = session.me?.user;
  const activeWs = session.me?.workspace;
  const usage = session.usage;
  const name = user?.name || user?.email || "Account";
  const email = user?.email ?? EM_DASH;
  const org = activeWs?.name ?? EM_DASH;
  const role = activeWs?.role ?? EM_DASH;

  const hoursUsed = usage ? usage.encodeHoursUsed : null;
  const hoursLimit = usage && usage.encodeHoursLimit > 0 ? usage.encodeHoursLimit : null;
  const quotaPct = hoursUsed !== null && hoursLimit ? Math.min(100, (hoursUsed / hoursLimit) * 100) : 0;

  const doSignOut = () => {
    setOpen(false);
    (async () => {
      try {
        await logout();
      } finally {
        clearAccessToken();
        session.goToLogin();
      }
    })();
  };

  return (
    <div style={{ position: "relative" }} ref={ref}>
      <button
        className="ae-b"
        onClick={() => setOpen((m) => !m)}
        style={{
          display: "flex",
          alignItems: "center",
          gap: 9,
          padding: "5px 9px 5px 5px",
          borderRadius: "var(--r-sm)",
          cursor: "pointer",
          background: open ? "var(--bg-hover)" : "transparent",
          border: "1px solid var(--line)",
        }}
      >
        <span
          style={{
            width: 26,
            height: 26,
            borderRadius: "50%",
            flex: "none",
            display: "grid",
            placeItems: "center",
            background: "var(--blue-500)",
            font: "600 10px var(--font-sans)",
            letterSpacing: ".04em",
            color: "#fff",
          }}
        >
          {initials(name)}
        </span>
        <span style={{ display: "flex", flexDirection: "column", alignItems: "flex-start", gap: 1 }}>
          <span style={{ font: "var(--t-label)", color: "#fff", whiteSpace: "nowrap" }}>{name}</span>
          <span style={{ font: "var(--t-micro)", color: "var(--fg4)", whiteSpace: "nowrap" }}>
            {org} {"·"} {role}
          </span>
        </span>
        <Icon name="chevron-down" size={13} color="var(--fg3)" />
      </button>
      {open && (
        <div
          style={{
            position: "absolute",
            right: 0,
            top: 46,
            width: 262,
            zIndex: 30,
            background: "var(--bg-panel)",
            border: "1px solid var(--line-strong)",
            borderRadius: "var(--r-md)",
            overflow: "hidden",
          }}
        >
          <div style={{ padding: "13px 14px", borderBottom: "1px solid var(--line)" }}>
            <div style={{ font: "var(--t-label)", color: "#fff", marginBottom: 3 }}>{name}</div>
            <div style={{ font: "400 11px var(--font-mono)", color: "var(--fg3)" }}>{email}</div>
          </div>
          <div style={{ padding: "11px 14px", borderBottom: "1px solid var(--line)", display: "flex", flexDirection: "column", gap: 8 }}>
            <Eb>Workspace</Eb>
            {session.workspaces.length === 0 && (
              <span style={{ font: "var(--t-micro)", color: "var(--fg4)" }}>No workspaces</span>
            )}
            {session.workspaces.map((w) => (
              <button
                key={w.id}
                className="ae-b"
                onClick={() => {
                  if (!w.active) session.switchTo(w.id);
                  setOpen(false);
                }}
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: 8,
                  background: "transparent",
                  border: "none",
                  cursor: w.active ? "default" : "pointer",
                  padding: 0,
                  textAlign: "left",
                }}
              >
                <Dot tone={w.active ? "ok" : "idle"} />
                <span style={{ font: "var(--t-body-sm)", color: w.active ? "#fff" : "var(--fg2)", flex: 1 }}>{w.name}</span>
                {w.active && <Icon name="check" size={13} color="var(--blue-400)" />}
              </button>
            ))}
          </div>
          <div style={{ padding: "11px 14px", borderBottom: "1px solid var(--line)" }}>
            <Row label="Plan" value={activeWs?.planTier ?? EM_DASH} />
            <Row
              label="Encode hours"
              value={`${fmtNum(hoursUsed, 0)} / ${hoursLimit ? fmtInt(hoursLimit) : EM_DASH}`}
            />
            <div style={{ marginTop: 6 }}>
              <Meter pct={quotaPct} />
            </div>
          </div>
          <div style={{ padding: 6, display: "flex", flexDirection: "column" }}>
            {/* Account settings and Billing are U2 (no service yet): shown
                disabled, never as live links. API keys reads FT-6a. */}
            <MenuItem icon="user" label="Account settings" disabled />
            <MenuItem icon="key-round" label={`API keys${session.apiKeys ? ` (${session.apiKeys.filter((k) => !k.revokedAt).length})` : ""}`} disabled />
            <MenuItem icon="receipt" label="Billing & usage" disabled />
            <MenuItem icon="log-out" label="Sign out" onClick={doSignOut} />
          </div>
        </div>
      )}
    </div>
  );
}

function MenuItem({ icon, label, onClick, disabled }: { icon: string; label: string; onClick?: () => void; disabled?: boolean }) {
  return (
    <button
      className="ae-b"
      onClick={disabled ? undefined : onClick}
      disabled={disabled}
      title={disabled ? "Not available yet" : undefined}
      style={{
        display: "flex",
        alignItems: "center",
        gap: 10,
        padding: "9px 8px",
        borderRadius: "var(--r-xs)",
        background: "transparent",
        border: "none",
        cursor: disabled ? "not-allowed" : "pointer",
        font: "var(--t-body-sm)",
        color: disabled ? "var(--fg4)" : "var(--fg1)",
        textAlign: "left",
      }}
    >
      <Icon name={icon} size={14} color={disabled ? "var(--fg4)" : "var(--fg3)"} />
      {label}
    </button>
  );
}
