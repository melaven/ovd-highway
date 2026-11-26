// Centralized Control Plane API client.
// - Persists ADMIN_API_TOKEN in localStorage under `ovd_admin_token`.
// - Attaches `Authorization: Bearer <token>` to every /api/v1/* request.
// - Appends `?token=<encoded>` to the WebSocket URL when a token exists.
// - Surfaces 401s through onUnauthorized() so the UI can open the Token Lock
//   modal exactly once per failure (callers must not retry-loop).

import type {
  CreateSourcePayload,
  EngineSettings,
  Source,
  UpdateSourcePayload,
} from '../types/controlPlane';
import type { LeadStatus } from './leads';

const env = (import.meta as { env?: Record<string, string | undefined> }).env;

const TOKEN_KEY = 'ovd_admin_token';
const API_BASE = (env?.VITE_API_BASE?.trim() || '/api/v1').replace(/\/+$/, '');

function defaultWsBaseUrl(): string {
  const configured = env?.VITE_WS_URL?.trim();
  if (configured) return configured;
  if (typeof window !== 'undefined' && window.location?.host) {
    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    return `${proto}//${window.location.host}/ws`;
  }
  return 'ws://localhost:8080/ws';
}

export const WS_BASE_URL = defaultWsBaseUrl();

export function getAdminToken(): string | null {
  try {
    const t = localStorage.getItem(TOKEN_KEY);
    return t && t.length > 0 ? t : null;
  } catch {
    return null;
  }
}

export function setAdminToken(token: string): void {
  try {
    const trimmed = token.trim();
    if (trimmed) localStorage.setItem(TOKEN_KEY, trimmed);
  } catch {
    /* storage unavailable */
  }
}

export function clearAdminToken(): void {
  try {
    localStorage.removeItem(TOKEN_KEY);
  } catch {
    /* storage unavailable */
  }
}

export function hasAdminToken(): boolean {
  return getAdminToken() !== null;
}

/** Build the WebSocket URL, attaching the stored admin token when present. */
export function wsUrl(base: string = WS_BASE_URL): string {
  const token = getAdminToken();
  if (!token) return base;
  const sep = base.includes('?') ? '&' : '?';
  return `${base}${sep}token=${encodeURIComponent(token)}`;
}

export type ApiResult<T> =
  | { ok: true; data: T }
  | { ok: false; status: number; message: string };

type UnauthorizedHandler = (message: string) => void;
const unauthorizedHandlers = new Set<UnauthorizedHandler>();

/** Subscribe to 401 events. Returns an unsubscribe function. */
export function onUnauthorized(handler: UnauthorizedHandler): () => void {
  unauthorizedHandlers.add(handler);
  return () => unauthorizedHandlers.delete(handler);
}

function emitUnauthorized(message: string): void {
  unauthorizedHandlers.forEach((h) => h(message));
}

export async function apiFetch<T>(
  path: string,
  init: RequestInit = {},
): Promise<ApiResult<T>> {
  const headers = new Headers(init.headers);
  const token = getAdminToken();
  if (token) headers.set('Authorization', `Bearer ${token}`);
  if (init.body && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json');
  }

  let res: Response;
  try {
    res = await fetch(`${API_BASE}${path}`, { ...init, headers });
  } catch {
    return { ok: false, status: 0, message: 'network error' };
  }

  if (res.status === 401) {
    const message = 'Control Plane API rejected the request (401 Unauthorized)';
    emitUnauthorized(message);
    return { ok: false, status: 401, message };
  }

  if (!res.ok) {
    let message = `HTTP ${res.status}`;
    try {
      const body = (await res.json()) as { error?: string; message?: string };
      if (body?.error) message = String(body.error);
      else if (body?.message) message = String(body.message);
    } catch {
      /* non-JSON error body */
    }
    return { ok: false, status: res.status, message };
  }

  if (res.status === 204) {
    return { ok: true, data: undefined as T };
  }
  try {
    return { ok: true, data: (await res.json()) as T };
  } catch {
    return { ok: true, data: undefined as T };
  }
}

// ---- Sources ----

export function listSources(): Promise<ApiResult<Source[]>> {
  return apiFetch<Source[]>('/sources');
}

export function createSource(
  payload: CreateSourcePayload,
): Promise<ApiResult<Source>> {
  return apiFetch<Source>('/sources', {
    method: 'POST',
    body: JSON.stringify(payload),
  });
}

export function updateSource(
  id: string,
  patch: UpdateSourcePayload,
): Promise<ApiResult<Source>> {
  return apiFetch<Source>(`/sources/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    body: JSON.stringify(patch),
  });
}

export function deleteSource(id: string): Promise<ApiResult<void>> {
  return apiFetch<void>(`/sources/${encodeURIComponent(id)}`, {
    method: 'DELETE',
  });
}

// ---- Leads ----

export interface PatchLeadPayload {
  status?: LeadStatus;
  draft_proposal?: string;
}

export interface LeadPatchResult {
  id: string;
  status: LeadStatus;
  draft_proposal: string;
}

export function patchLead(
  id: string,
  payload: PatchLeadPayload,
): Promise<ApiResult<LeadPatchResult>> {
  return apiFetch<LeadPatchResult>(`/leads/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    body: JSON.stringify(payload),
  });
}

// ---- Settings ----

export function getSettings(): Promise<ApiResult<EngineSettings>> {
  return apiFetch<EngineSettings>('/settings');
}

export function putSettings(
  settings: EngineSettings,
): Promise<ApiResult<EngineSettings>> {
  return apiFetch<EngineSettings>('/settings', {
    method: 'PUT',
    body: JSON.stringify(settings),
  });
}

// ---- Dead Letter Queue ----

export interface DLQItem {
  id: string;
  source: string;
  error: string;
  attempts: number;
  created_at: string;
  payload_preview: string;
}

export interface DLQList {
  count: number;
  items: DLQItem[];
}

export function listDLQ(): Promise<ApiResult<DLQList>> {
  return apiFetch<DLQList>('/dlq');
}

export function retryDLQ(): Promise<ApiResult<{ queued: number }>> {
  return apiFetch<{ queued: number }>('/dlq/retry', { method: 'POST' });
}

/** True when the failure means "endpoint not implemented / unreachable / 5xx". */
export function isOfflineResult(result: { ok: boolean; status?: number }): boolean {
  if (result.ok) return false;
  const s = result.status ?? 0;
  return s === 0 || s === 404 || s >= 500;
}
