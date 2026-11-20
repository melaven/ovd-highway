// Lead wire-format normalization for the WebSocket stream.
// Raw payload shape streamed by api/ws.go: Go domain.QualifiedLead

import { extractOriginName, formatRelativeTime, getSourceFromSignal } from '../utils/formatters';

export type LeadStatus = 'new' | 'contacted' | 'archived';

const LEAD_STATUSES: readonly string[] = ['new', 'contacted', 'archived'];

export function normalizeLeadStatus(value: unknown): LeadStatus {
  return LEAD_STATUSES.includes(String(value)) ? (value as LeadStatus) : 'new';
}

export interface QualifiedLead {
  id: string;
  source: 'mtproto' | 'habr' | 'web' | 'reddit' | 'hh' | string;
  origin_name: string;
  title: string;
  body: string;
  author_url: string;
  post_url: string;
  budget: string;
  captured_at: string;
  score: number;
  is_relevant: boolean;
  tech_stack: string[];
  pain_summary: string;
  draft_proposal: string;
  job_type?: 'freelance' | 'vacancy' | string;
  status: LeadStatus;
}

export interface RawQualifiedLead {
  signal: {
    id?: string;
    source?: string;
    url?: string;
    text?: string;
    budget?: string;
    received_at?: string;
    job_type?: string;
  };
  qualification: {
    is_relevant?: boolean;
    score?: number;
    estimated_budget?: string;
    pain_points?: string;
    draft_proposal?: string;
    tech_stack?: string[];
  };
  tech_stack?: string[];
  job_type?: string;
  qualified_at?: string;
  status?: string;
}

export function isRawLead(msg: unknown): msg is RawQualifiedLead {
  if (!msg || typeof msg !== 'object') return false;
  const m = msg as Record<string, unknown>;
  return (
    typeof m.signal === 'object' && m.signal !== null &&
    typeof m.qualification === 'object' && m.qualification !== null
  );
}

export function isFlatLead(msg: unknown): msg is QualifiedLead {
  if (!msg || typeof msg !== 'object') return false;
  const m = msg as Record<string, unknown>;
  return typeof m.title === 'string' && typeof m.score === 'number';
}

export function normalizeRawLead(raw: RawQualifiedLead): QualifiedLead {
  const sig = raw.signal ?? {};
  const qual = raw.qualification ?? {};
  const source = getSourceFromSignal(String(sig.source ?? ''));
  const url = String(sig.url ?? '');
  const text = String(sig.text ?? '');
  const title = text.length > 140 ? `${text.slice(0, 137).trimEnd()}…` : text;

  return {
    id: String(sig.id ?? `${url}-${raw.qualified_at ?? Date.now()}`),
    source: source.toLowerCase(),
    origin_name: extractOriginName(String(sig.source ?? ''), url),
    title,
    body: text,
    author_url: url,
    post_url: url,
    budget: String(qual.estimated_budget ?? sig.budget ?? ''),
    captured_at: formatRelativeTime(String(sig.received_at ?? raw.qualified_at ?? new Date().toISOString())),
    score: Number(qual.score ?? 0),
    is_relevant: Boolean(qual.is_relevant),
    tech_stack: Array.isArray(qual.tech_stack)
      ? qual.tech_stack.map(String)
      : Array.isArray(raw.tech_stack)
        ? raw.tech_stack.map(String)
        : [],
    pain_summary: String(qual.pain_points ?? ''),
    draft_proposal: String(qual.draft_proposal ?? ''),
    job_type: String(raw.job_type ?? sig.job_type ?? ''),
    status: normalizeLeadStatus(raw.status),
  };
}

export function normalizeIncoming(msg: unknown): QualifiedLead | null {
  if (isFlatLead(msg)) {
    return { ...msg, status: normalizeLeadStatus(msg.status) };
  }
  if (isRawLead(msg)) return normalizeRawLead(msg);
  return null;
}

export function scoreTextClass(score: number): string {
  if (score >= 8) return 'text-emerald-300';
  if (score >= 6) return 'text-amber-200';
  return 'text-[var(--text-muted)]';
}

export const SCORE_FILTERS = [
  { label: 'All', val: 0 },
  { label: '7+', val: 7 },
  { label: '8+', val: 8 },
];

export const SOURCE_FILTERS = [
  { label: 'All', val: 'all' },
  { label: 'Reddit', val: 'reddit' },
  { label: 'HH', val: 'hh' },
  { label: 'TG', val: 'mtproto' },
];

export const TRACK_TABS = [
  { label: 'Contracts', val: 'contracts' },
  { label: 'Full-Time Jobs', val: 'jobs' },
] as const;

export type TrackFilter = (typeof TRACK_TABS)[number]['val'];

export const STATUS_FILTERS = [
  { label: 'Active', val: 'new' },
  { label: 'Contacted', val: 'contacted' },
  { label: 'Archived', val: 'archived' },
  { label: 'All', val: 'all' },
] as const;

export type StatusFilter = (typeof STATUS_FILTERS)[number]['val'];
