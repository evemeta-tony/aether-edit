// packages/console/src/hooks/useSession.ts
//
// Boots the FT-6a session: exchanges the refresh cookie for an access token,
// loads identity (/v1/me), the workspace list, usage rollup, and API keys.
// A hard 401 (no valid refresh cookie) routes the operator to the OIDC login
// endpoint. All four reads surface loading/error honestly (R10).

import { useCallback, useEffect, useState } from "react";
import { refreshAccessToken } from "../api/http";
import { registerUnauthorizedHandler } from "../api/session";
import {
  getMe,
  getUsage,
  listApiKeys,
  listWorkspaces,
  loginUrl,
  switchWorkspace,
  type ApiKeyView,
  type MeResponse,
  type UsageResponse,
  type WorkspaceView,
} from "../api/tenancy";
import { setAccessToken } from "../api/session";

export type Phase = "booting" | "ready" | "unauthenticated" | "error";

export interface SessionState {
  phase: Phase;
  me: MeResponse | null;
  workspaces: WorkspaceView[];
  usage: UsageResponse | null;
  apiKeys: ApiKeyView[] | null;
  errorMessage: string | null;
  refresh: () => void;
  switchTo: (workspaceId: string) => void;
  goToLogin: () => void;
}

export function useSession(): SessionState {
  const [phase, setPhase] = useState<Phase>("booting");
  const [me, setMe] = useState<MeResponse | null>(null);
  const [workspaces, setWorkspaces] = useState<WorkspaceView[]>([]);
  const [usage, setUsage] = useState<UsageResponse | null>(null);
  const [apiKeys, setApiKeys] = useState<ApiKeyView[] | null>(null);
  const [errorMessage, setError] = useState<string | null>(null);
  const [reloadTick, setReload] = useState(0);

  const goToLogin = useCallback(() => {
    window.location.href = loginUrl();
  }, []);

  useEffect(() => {
    registerUnauthorizedHandler(() => setPhase("unauthenticated"));
  }, []);

  useEffect(() => {
    let cancelled = false;
    const ctrl = new AbortController();
    (async () => {
      setPhase("booting");
      setError(null);
      const ok = await refreshAccessToken();
      if (cancelled) return;
      if (!ok) {
        setPhase("unauthenticated");
        return;
      }
      try {
        const meResp = await getMe(ctrl.signal);
        if (cancelled) return;
        setMe(meResp);
        // The remaining reads are non-fatal: a workspace with no usage rollup
        // yet, or a member without key-admin rights, still yields a usable
        // console. Each failure leaves its slice null (rendered as loading or
        // em-dash), never a fabricated value.
        const [wsRes, usageRes, keysRes] = await Promise.allSettled([
          listWorkspaces(ctrl.signal),
          getUsage(ctrl.signal),
          listApiKeys(ctrl.signal),
        ]);
        if (cancelled) return;
        if (wsRes.status === "fulfilled") setWorkspaces(wsRes.value.workspaces);
        if (usageRes.status === "fulfilled") setUsage(usageRes.value);
        if (keysRes.status === "fulfilled") setApiKeys(keysRes.value.apiKeys);
        setPhase("ready");
      } catch (err) {
        if (cancelled) return;
        setError(err instanceof Error ? err.message : "session load failed");
        setPhase("error");
      }
    })();
    return () => {
      cancelled = true;
      ctrl.abort();
    };
  }, [reloadTick]);

  const refresh = useCallback(() => setReload((t) => t + 1), []);

  const switchTo = useCallback(
    (workspaceId: string) => {
      (async () => {
        try {
          const res = await switchWorkspace(workspaceId);
          if (res.accessToken && typeof res.expiresIn === "number") {
            setAccessToken(res.accessToken, res.expiresIn);
          }
          setReload((t) => t + 1);
        } catch (err) {
          setError(err instanceof Error ? err.message : "workspace switch failed");
        }
      })();
    },
    [],
  );

  return { phase, me, workspaces, usage, apiKeys, errorMessage, refresh, switchTo, goToLogin };
}
