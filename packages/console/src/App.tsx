// packages/console/src/App.tsx
//
// Top-level shell. Gates the console on the FT-6a session: while the refresh
// exchange and identity load are in flight it shows a booting state; a hard 401
// shows a sign-in prompt that navigates to the OIDC login endpoint; a load
// error shows the error (never a fabricated console). Once ready, it renders
// the file transcoder wired to the live session.

import { useSession } from "./hooks/useSession";
import { FileTranscoder } from "./components/FileTranscoder";
import { Mark } from "./components/Icons";

export function App() {
  const session = useSession();

  if (session.phase === "ready" && session.me) {
    return <FileTranscoder session={session} />;
  }

  return (
    <div
      style={{
        position: "absolute",
        inset: 0,
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        justifyContent: "center",
        gap: 16,
        background: "var(--bg-base)",
        color: "var(--fg1)",
      }}
    >
      <Mark size={40} />
      <span style={{ font: "600 13px var(--font-sans)", letterSpacing: "var(--ls-wordmark)", color: "#fff" }}>
        AETHER CLOUD
      </span>
      {session.phase === "booting" && (
        <span style={{ font: "var(--t-body-sm)", color: "var(--fg3)" }}>Loading session...</span>
      )}
      {session.phase === "unauthenticated" && (
        <>
          <span style={{ font: "var(--t-body-sm)", color: "var(--fg3)" }}>Your session has expired.</span>
          <button
            className="ae-b"
            onClick={session.goToLogin}
            style={{
              font: "var(--t-btn)",
              letterSpacing: "var(--ls-btn)",
              textTransform: "uppercase",
              color: "#fff",
              background: "var(--blue-500)",
              border: "1px solid transparent",
              borderRadius: "var(--r-sm)",
              padding: "10px 18px",
              cursor: "pointer",
            }}
          >
            Sign in
          </button>
        </>
      )}
      {session.phase === "error" && (
        <span style={{ font: "var(--t-body-sm)", color: "var(--err)" }}>
          {session.errorMessage ?? "Session failed to load."}
        </span>
      )}
    </div>
  );
}
