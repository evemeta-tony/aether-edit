// packages/console/src/api/tenancy.ts
//
// FT-6a tenancy client. Backs the SaaS chrome: identity (UserMenu), workspace
// switcher, plan/usage meter, and API keys. Shapes mirror services/tenancy
// exactly (userView, workspaceView, usageResponse, apiKeyView).

import { servicePaths } from "./config";
import { request } from "./http";

export interface UserView {
  id: string;
  email: string;
  name: string;
}

export interface WorkspaceView {
  id: string;
  name: string;
  planTier: string;
  createdAt: string;
  role?: string;
  active: boolean;
}

export interface MeResponse {
  user: UserView;
  workspace?: WorkspaceView;
}

export interface UsageResponse {
  workspaceId: string;
  planTier: string;
  month: string;
  encodeSecondsUsed: number;
  encodeHoursUsed: number;
  encodeHoursLimit: number;
  storageBytesUsed: number;
  storageBytesLimit: number;
  uploadSessions: number;
  uploadsCompleted: number;
  jobsQueued: number;
  jobsStarted: number;
  jobsCompleted: number;
  jobsFailed: number;
}

export interface ApiKeyView {
  id: string;
  workspaceId: string;
  name: string;
  createdBy: string;
  createdAt: string;
  lastUsedAt?: string;
  revokedAt?: string;
}

export interface SwitchWorkspaceResponse {
  accessToken: string;
  tokenType: string;
  expiresIn?: number;
}

const t = servicePaths.tenancy;

export function getMe(signal?: AbortSignal): Promise<MeResponse> {
  return request<MeResponse>(`${t}/v1/me`, { signal });
}

export function listWorkspaces(signal?: AbortSignal): Promise<{ workspaces: WorkspaceView[] }> {
  return request<{ workspaces: WorkspaceView[] }>(`${t}/v1/workspaces`, { signal });
}

export function getUsage(signal?: AbortSignal): Promise<UsageResponse> {
  return request<UsageResponse>(`${t}/v1/usage`, { signal });
}

export function listApiKeys(signal?: AbortSignal): Promise<{ apiKeys: ApiKeyView[] }> {
  return request<{ apiKeys: ApiKeyView[] }>(`${t}/v1/apikeys`, { signal });
}

export function switchWorkspace(workspaceId: string): Promise<SwitchWorkspaceResponse> {
  return request<SwitchWorkspaceResponse>(`${t}/v1/workspaces/switch`, {
    method: "POST",
    json: { workspaceId },
  });
}

// loginUrl is the OIDC start endpoint the console navigates to on a hard 401.
export function loginUrl(): string {
  return `${t}/v1/auth/login`;
}

export async function logout(): Promise<void> {
  await request<null>(`${t}/v1/auth/logout`, { method: "POST", noAuthRedirect: true });
}
