import { useEffect, useMemo, useRef, useState } from 'react';
import { motion } from 'framer-motion';
import { Archive, ArchiveRestore, Copy, ExternalLink, Loader2, RotateCcw, Search, Send, X } from 'lucide-react';
import { patchLead, type PatchLeadPayload } from '../lib/api';
import {
  SCORE_FILTERS,
  SOURCE_FILTERS,
  STATUS_FILTERS,
  TRACK_TABS,
  scoreTextClass,
  type LeadStatus,
  type QualifiedLead,
  type StatusFilter,
  type TrackFilter,
} from '../lib/leads';
import type { ConnState } from '../App';

const PILL_SPRING = { stiffness: 420, damping: 34, mass: 0.6 };
const INSPECTOR_MOTION = { duration: 0.13, ease: 'easeOut' as const };

interface StreamViewProps {
  leads: QualifiedLead[];
  conn: ConnState;
  onLeadUpdated: (id: string, patch: PatchLeadPayload) => void;
}

export default function StreamView({ leads, conn, onLeadUpdated }: StreamViewProps) {
  const [selectedId, setSelectedId] = useState<string>('');
  const [minScore, setMinScore] = useState<number>(0);
  const [sourceFilter, setSourceFilter] = useState<string>('all');
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all');
  const [filter, setFilter] = useState<TrackFilter>('contracts');
  const [query, setQuery] = useState<string>('');
  const [mobileSheet, setMobileSheet] = useState(false);
  const searchRef = useRef<HTMLInputElement>(null);

  const filtered = useMemo(() => {
    return leads.filter((item) => {
      const isJob = item.source === 'hh' || item.job_type === 'vacancy';
      if (filter === 'jobs' ? !isJob : isJob) return false;
      if (statusFilter !== 'all' && item.status !== statusFilter) return false;
      if (item.score < minScore) return false;
      if (sourceFilter !== 'all' && item.source !== sourceFilter) return false;
      if (query.trim()) {
        const q = query.toLowerCase();
        const hay = `${item.title} ${item.body} ${item.origin_name} ${item.tech_stack.join(' ')}`.toLowerCase();
        if (!hay.includes(q)) return false;
      }
      return true;
    });
  }, [leads, minScore, sourceFilter, statusFilter, query, filter]);

  const activeLead = useMemo(
    () => filtered.find((l) => l.id === selectedId) || filtered[0] || null,
    [filtered, selectedId],
  );

  const resetFilters = () => {
    setMinScore(0);
    setSourceFilter('all');
    setStatusFilter('all');
    setQuery('');
    setFilter('contracts');
  };

  const emptyMessage =
    conn === 'connecting'
      ? 'Connecting to signal stream…'
      : conn === 'auth_required'
        ? 'WS auth required — open Token Lock from the rail'
        : conn === 'live'
          ? 'No qualified signals yet — connectors are ingesting'
          : 'Stream offline — reconnecting…';

  return (
    <div className="h-full flex flex-col min-h-0">
      {/* View header: track tabs + filters + search */}
      <div className="shrink-0 px-4 pt-3 pb-2 flex flex-col gap-2">
        <div className="flex items-center justify-between gap-3 flex-wrap">
          <div className="flex items-center gap-2">
            <span className="font-sans text-[10px] font-semibold uppercase tracking-widest text-[var(--text-muted)]">
              Stream
            </span>
            <div className="flex items-center gap-1">
              {TRACK_TABS.map(({ label, val }) => {
                const active = filter === val;
                return (
                  <button
                    key={val}
                    onClick={() => setFilter(val)}
                    className={`relative px-2.5 py-1 rounded-md font-sans text-[11px] font-medium transition-colors duration-[120ms] ease-out ${
                      active
                        ? 'text-[var(--text-primary)]'
                        : 'text-[var(--text-muted)] hover:text-[var(--text-secondary)]'
                    }`}
                  >
                    {active && (
                      <motion.span
                        layoutId="track-tab-pill"
                        transition={PILL_SPRING}
                        className="absolute inset-0 rounded-md bg-[rgba(141,153,174,0.16)] border border-[var(--glass-border-hover)]"
                      />
                    )}
                    <span className="relative">{label}</span>
                  </button>
                );
              })}
            </div>
          </div>

          <div className="relative flex items-center">
            <Search className="w-3.5 h-3.5 text-[var(--text-muted)] absolute left-2 pointer-events-none" strokeWidth={1.75} />
            <input
              ref={searchRef}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Filter stream…"
              className="glass-input w-44 sm:w-56 rounded-md pl-7 pr-8 py-1 font-sans text-[11px]"
            />
            <kbd className="absolute right-2 font-mono text-[10px] text-[var(--text-muted)] pointer-events-none">
              ⌘K
            </kbd>
          </div>
        </div>

        <div className="flex items-center gap-1 font-sans text-[11px]">
          {SCORE_FILTERS.map((opt) => (
            <button
              key={opt.label}
              onClick={() => setMinScore(opt.val)}
              className={`h-7 px-2 flex items-center border-b-2 transition-colors duration-[120ms] ease-out ${
                minScore === opt.val
                  ? 'border-duskmist text-[var(--text-primary)]'
                  : 'border-transparent text-[var(--text-muted)] hover:text-[var(--text-secondary)]'
              }`}
            >
              {opt.label}
            </button>
          ))}
          <span className="h-3.5 w-px bg-[var(--glass-border)] mx-1.5" />
          {SOURCE_FILTERS.map((opt) => (
            <button
              key={opt.label}
              onClick={() => setSourceFilter(opt.val)}
              className={`h-7 px-2 flex items-center border-b-2 transition-colors duration-[120ms] ease-out ${
                sourceFilter === opt.val
                  ? 'border-duskmist text-[var(--text-primary)]'
                  : 'border-transparent text-[var(--text-muted)] hover:text-[var(--text-secondary)]'
              }`}
            >
              {opt.label}
            </button>
          ))}
          <span className="h-3.5 w-px bg-[var(--glass-border)] mx-1.5" />
          {STATUS_FILTERS.map((opt) => (
            <button
              key={opt.label}
              onClick={() => setStatusFilter(opt.val)}
              className={`h-7 px-2 flex items-center border-b-2 transition-colors duration-[120ms] ease-out ${
                statusFilter === opt.val
                  ? 'border-duskmist text-[var(--text-primary)]'
                  : 'border-transparent text-[var(--text-muted)] hover:text-[var(--text-secondary)]'
              }`}
            >
              {opt.label}
            </button>
          ))}
        </div>
      </div>

      {/* Split workspace */}
      <main className="flex-1 grid grid-cols-1 lg:grid-cols-12 min-h-0 overflow-hidden">
        {/* Left feed */}
        <div className="lg:col-span-7 border-b lg:border-b-0 lg:border-r border-[var(--glass-border)] overflow-y-auto divide-y divide-[rgba(141,153,174,0.07)] min-h-0">
          {leads.length === 0 ? (
            <div className="h-full flex flex-col items-center justify-center gap-3 px-4">
              <p className="font-sans text-xs text-[var(--text-muted)]">{emptyMessage}</p>
              {(conn === 'offline' || conn === 'reconnecting') && (
                <span className="inline-flex items-center gap-2 font-sans text-[11px] text-[var(--text-muted)]">
                  <Loader2 className="w-3.5 h-3.5 animate-spin" strokeWidth={1.75} />
                  retrying every 4s
                </span>
              )}
            </div>
          ) : filtered.length === 0 ? (
            <div className="h-full flex flex-col items-center justify-center gap-3 px-4">
              <p className="font-sans text-xs text-[var(--text-muted)]">0 signals match current filters</p>
              <button
                onClick={resetFilters}
                className="inline-flex items-center gap-2 px-3 py-1.5 rounded-md glass-input font-sans text-xs text-[var(--text-primary)] transition-colors duration-[120ms] ease-out hover:border-[var(--glass-border-hover)]"
              >
                <RotateCcw className="w-3.5 h-3.5" strokeWidth={1.75} />
                Reset Filters
              </button>
            </div>
          ) : (
            filtered.map((item) => {
              const isSelected = activeLead?.id === item.id;
              return (
                <article
                  key={item.id}
                  onClick={() => {
                    setSelectedId(item.id);
                    if (window.innerWidth < 1024) setMobileSheet(true);
                  }}
                  className={`px-4 py-3 cursor-pointer transition-colors duration-[120ms] ease-out ${
                    item.status === 'archived' ? 'opacity-55 ' : ''
                  }${
                    isSelected
                      ? 'bg-[rgba(141,153,174,0.09)] shadow-[inset_2px_0_0_0_#8D99AE]'
                      : 'hover:bg-[rgba(141,153,174,0.05)]'
                  }`}
                >
                  <div className="flex items-center gap-3">
                    <span className={`font-mono text-xs w-9 shrink-0 tabular-nums ${scoreTextClass(item.score)}`}>
                      {item.score}/10
                    </span>
                    <h2 className="flex-1 min-w-0 text-sm font-medium text-[var(--text-primary)] truncate">
                      {item.title}
                    </h2>
                    {item.budget && (
                      <span className="font-mono text-xs text-[var(--text-secondary)] shrink-0 tabular-nums">
                        {item.budget}
                      </span>
                    )}
                  </div>
                  <div className="mt-1 flex items-center gap-2 min-w-0">
                    {item.job_type && (
                      <span
                        className={`shrink-0 px-1.5 py-0.5 rounded border font-sans text-[10px] font-medium uppercase tracking-wide ${
                          item.job_type === 'vacancy'
                            ? 'bg-sky-400/[0.08] border-sky-400/20 text-sky-300'
                            : 'bg-emerald-400/[0.08] border-emerald-400/20 text-emerald-300'
                        }`}
                      >
                        {item.job_type === 'vacancy' ? 'vac' : 'frl'}
                      </span>
                    )}
                    {item.status === 'contacted' && (
                      <span className="shrink-0 px-1.5 py-0.5 rounded-full border border-emerald-400/30 bg-emerald-400/10 font-sans text-[10px] font-medium uppercase tracking-wide text-emerald-300">
                        contacted
                      </span>
                    )}
                    {item.status === 'archived' && (
                      <span className="shrink-0 px-1.5 py-0.5 rounded-full border border-[var(--glass-border)] bg-[rgba(141,153,174,0.06)] font-sans text-[10px] font-medium uppercase tracking-wide text-[var(--text-muted)]">
                        archived
                      </span>
                    )}
                    <span className="font-mono text-[11px] text-[var(--text-muted)] truncate">
                      {item.source} · {item.origin_name} · {item.captured_at}
                    </span>
                    {item.tech_stack.length > 0 && (
                      <span className="flex items-center gap-1 shrink-0">
                        {item.tech_stack.slice(0, 4).map((tech) => (
                          <span
                            key={tech}
                            className="px-1.5 py-0.5 rounded bg-[rgba(141,153,174,0.07)] border border-[var(--glass-border)] font-sans text-[10px] text-[var(--text-secondary)]"
                          >
                            {tech}
                          </span>
                        ))}
                      </span>
                    )}
                  </div>
                </article>
              );
            })
          )}
        </div>

        {/* Right inspector */}
        <div className="hidden lg:block lg:col-span-5 overflow-y-auto p-5 select-text min-h-0">
          {activeLead ? (
            <motion.div
              key={activeLead.id}
              initial={{ opacity: 0, x: 4 }}
              animate={{ opacity: 1, x: 0 }}
              transition={INSPECTOR_MOTION}
            >
              <InspectorContent
                lead={activeLead}
                filter={filter}
                onLeadUpdated={onLeadUpdated}
              />
            </motion.div>
          ) : (
            <div className="h-full flex items-center justify-center">
              <p className="font-sans text-[11px] text-[var(--text-muted)]">Select a signal to inspect</p>
            </div>
          )}
        </div>
      </main>

      {/* Mobile slide-over inspector */}
      {mobileSheet && activeLead && (
        <div className="lg:hidden fixed inset-0 z-50">
          <div className="absolute inset-0 bg-[rgba(10,11,17,0.6)]" onClick={() => setMobileSheet(false)} />
          <div className="absolute inset-x-0 bottom-0 max-h-[85vh] overflow-y-auto rounded-t-xl glass-elevated p-4">
            <div className="flex items-center justify-between mb-4">
              <span className="font-sans text-[11px] font-semibold uppercase tracking-wider text-[var(--text-muted)]">
                Signal Inspector
              </span>
              <button
                onClick={() => setMobileSheet(false)}
                className="p-1.5 rounded-md text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[rgba(141,153,174,0.1)] transition-colors duration-[120ms]"
                aria-label="Close inspector"
              >
                <X className="w-4 h-4" strokeWidth={1.75} />
              </button>
            </div>
            <motion.div
              key={activeLead.id}
              initial={{ opacity: 0, x: 4 }}
              animate={{ opacity: 1, x: 0 }}
              transition={INSPECTOR_MOTION}
            >
              <InspectorContent
                lead={activeLead}
                filter={filter}
                onLeadUpdated={onLeadUpdated}
              />
            </motion.div>
          </div>
        </div>
      )}
    </div>
  );
}

function StatusBadge({ status }: { status: LeadStatus }) {
  const styles: Record<LeadStatus, string> = {
    new: 'border-[var(--glass-border)] bg-[rgba(141,153,174,0.06)] text-[var(--text-muted)]',
    contacted: 'border-emerald-400/30 bg-emerald-400/10 text-emerald-300',
    archived: 'border-[var(--glass-border)] bg-[rgba(141,153,174,0.04)] text-[var(--text-muted)] line-through',
  };
  return (
    <span
      className={`shrink-0 px-1.5 py-0.5 rounded-full border font-sans text-[10px] font-medium uppercase tracking-wide ${styles[status]}`}
    >
      {status}
    </span>
  );
}

function InspectorContent({
  lead,
  filter,
  onLeadUpdated,
}: {
  lead: QualifiedLead;
  filter: TrackFilter;
  onLeadUpdated: (id: string, patch: PatchLeadPayload) => void;
}) {
  const [pitch, setPitch] = useState(lead.draft_proposal);
  const [busy, setBusy] = useState<'idle' | 'copy' | 'save'>('idle');
  const [notice, setNotice] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  useEffect(() => {
    setPitch(lead.draft_proposal);
    setNotice(null);
  }, [lead.id, lead.draft_proposal]);

  const pitchDirty = pitch !== lead.draft_proposal;

  const applyPatch = async (payload: PatchLeadPayload): Promise<boolean> => {
    const res = await patchLead(lead.id, payload);
    if (res.ok) {
      onLeadUpdated(lead.id, {
        ...(res.data.status !== undefined ? { status: res.data.status } : {}),
        ...(res.data.draft_proposal !== undefined
          ? { draft_proposal: res.data.draft_proposal }
          : {}),
      });
      return true;
    }
    setNotice({ kind: 'err', text: res.message });
    return false;
  };

  const saveDraft = async () => {
    setBusy('save');
    setNotice(null);
    const ok = await applyPatch({ draft_proposal: pitch });
    setBusy('idle');
    if (ok) setNotice({ kind: 'ok', text: 'Draft saved' });
  };

  const setStatus = async (status: LeadStatus) => {
    setBusy('save');
    setNotice(null);
    await applyPatch({ status });
    setBusy('idle');
  };

  const copyOnly = async () => {
    setBusy('copy');
    setNotice(null);
    try {
      await navigator.clipboard.writeText(pitch);
      setNotice({ kind: 'ok', text: 'Pitch copied' });
    } catch {
      setNotice({ kind: 'err', text: 'Clipboard blocked' });
    }
    setBusy('idle');
  };

  const copyPitchAndOpen = async () => {
    const url = lead.post_url || lead.author_url;
    if (url) window.open(url, '_blank', 'noopener,noreferrer');
    setBusy('copy');
    setNotice(null);
    try {
      await navigator.clipboard.writeText(pitch);
    } catch {
      setBusy('idle');
      setNotice({ kind: 'err', text: 'Opened source, but clipboard blocked — pitch not copied' });
      return;
    }
    const ok = await applyPatch({ status: 'contacted', draft_proposal: pitch });
    setBusy('idle');
    if (ok) {
      setNotice({ kind: 'ok', text: 'Opened source · pitch copied · marked contacted' });
    } else {
      setNotice((n) =>
        n ?? { kind: 'err', text: 'Opened source and copied, but status update failed' },
      );
    }
  };

  return (
    <div className="space-y-6">
      <div className="space-y-2">
        <div className="flex items-center justify-between gap-3">
          <span className="font-mono text-[11px] uppercase tracking-wider text-[var(--text-muted)]">
            {lead.origin_name} · {lead.captured_at}
          </span>
          <a
            href={lead.post_url || lead.author_url}
            target="_blank"
            rel="noreferrer"
            className="shrink-0 inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md glass-input font-sans text-[11px] text-[var(--text-secondary)] transition-colors duration-[120ms] ease-out hover:text-[var(--text-primary)] hover:border-[var(--glass-border-hover)]"
          >
            Source
            <ExternalLink className="w-3 h-3" strokeWidth={1.75} />
          </a>
        </div>
        <h2 className="text-sm font-semibold text-[var(--text-primary)] leading-snug">{lead.title}</h2>
        <div className="flex items-center gap-3 font-mono text-[11px] text-[var(--text-muted)] tabular-nums">
          <span className={scoreTextClass(lead.score)}>{lead.score}/10</span>
          {lead.budget && <span>{lead.budget}</span>}
          <StatusBadge status={lead.status} />
        </div>
        <div className="flex items-center gap-2 pt-1">
          {lead.status === 'archived' ? (
            <button
              onClick={() => void setStatus('new')}
              disabled={busy !== 'idle'}
              className="inline-flex items-center gap-1.5 px-2 py-1 rounded-md border border-[var(--glass-border)] font-sans text-[10px] font-medium uppercase tracking-wide text-[var(--text-secondary)] transition-colors duration-[120ms] ease-out hover:text-[var(--text-primary)] hover:border-[var(--glass-border-hover)] disabled:opacity-50"
            >
              <ArchiveRestore className="w-3 h-3" strokeWidth={1.75} />
              Restore to New
            </button>
          ) : (
            <button
              onClick={() => void setStatus('archived')}
              disabled={busy !== 'idle'}
              className="inline-flex items-center gap-1.5 px-2 py-1 rounded-md border border-[var(--glass-border)] font-sans text-[10px] font-medium uppercase tracking-wide text-[var(--text-secondary)] transition-colors duration-[120ms] ease-out hover:text-[var(--text-primary)] hover:border-[var(--glass-border-hover)] disabled:opacity-50"
            >
              <Archive className="w-3 h-3" strokeWidth={1.75} />
              Archive
            </button>
          )}
        </div>
      </div>

      <section className="border-t border-[var(--glass-border)] pt-5 space-y-2">
        <span className="font-sans text-[11px] font-semibold uppercase tracking-wider text-[var(--text-muted)] block">
          Captured Payload
        </span>
        <p className="text-sm leading-relaxed text-[var(--text-secondary)] whitespace-pre-wrap">
          {lead.body}
        </p>
        {lead.tech_stack.length > 0 && (
          <div className="flex flex-wrap gap-1.5 pt-1">
            {lead.tech_stack.map((tech) => (
              <span
                key={tech}
                className="px-1.5 py-0.5 rounded bg-[rgba(141,153,174,0.07)] border border-[var(--glass-border)] font-sans text-[10px] text-[var(--text-secondary)]"
              >
                {tech}
              </span>
            ))}
          </div>
        )}
      </section>

      <section className="border-t border-[var(--glass-border)] pt-5 space-y-3">
        <div className="flex items-center justify-between gap-3">
          <span className="font-sans text-[11px] font-semibold uppercase tracking-wider text-[var(--text-muted)]">
            {filter === 'contracts' ? 'Draft Pitch' : 'Cover Letter'}
          </span>
          <div className="flex items-center gap-1.5">
            <button
              onClick={() => void copyOnly()}
              disabled={busy !== 'idle'}
              title="Copy pitch only"
              className="inline-flex items-center gap-1.5 px-2.5 py-1.5 rounded-md border border-[var(--glass-border)] font-sans text-[11px] font-medium text-[var(--text-secondary)] transition-colors duration-[120ms] ease-out hover:text-[var(--text-primary)] hover:border-[var(--glass-border-hover)] disabled:opacity-50"
            >
              <Copy className="w-3.5 h-3.5" strokeWidth={1.75} />
              Copy
            </button>
            <button
              onClick={() => void copyPitchAndOpen()}
              disabled={busy !== 'idle'}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-emerald-400/30 bg-emerald-400/15 font-sans text-[11px] font-medium text-emerald-200 transition-colors duration-[120ms] ease-out hover:bg-emerald-400/25 disabled:opacity-50"
            >
              {busy === 'copy' ? (
                <Loader2 className="w-3.5 h-3.5 animate-spin" strokeWidth={1.75} />
              ) : (
                <Send className="w-3.5 h-3.5" strokeWidth={1.75} />
              )}
              Copy Pitch &amp; Open
            </button>
          </div>
        </div>
        <textarea
          value={pitch}
          onChange={(e) => setPitch(e.target.value)}
          rows={10}
          spellCheck={false}
          className="glass-input w-full rounded-md px-3 py-2 font-sans text-[13px] leading-relaxed text-[var(--text-secondary)] resize-y"
        />
        <div className="flex items-center justify-between gap-3 min-h-6">
          {pitchDirty ? (
            <button
              onClick={() => void saveDraft()}
              disabled={busy !== 'idle'}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-[var(--glass-border)] bg-[rgba(141,153,174,0.1)] font-sans text-[11px] font-medium text-[var(--text-primary)] transition-colors duration-[120ms] ease-out hover:bg-[rgba(141,153,174,0.16)] disabled:opacity-50"
            >
              {busy === 'save' ? (
                <Loader2 className="w-3.5 h-3.5 animate-spin" strokeWidth={1.75} />
              ) : null}
              Save Draft
            </button>
          ) : (
            <span />
          )}
          {notice && (
            <span
              className={`font-sans text-[11px] ${
                notice.kind === 'ok' ? 'text-emerald-300' : 'text-amber-300'
              }`}
            >
              {notice.text}
            </span>
          )}
        </div>
      </section>
    </div>
  );
}
