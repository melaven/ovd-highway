import { useMemo, useRef, useState } from 'react';
import { motion } from 'framer-motion';
import { Check, Copy, ExternalLink, Loader2, RotateCcw, Search, X } from 'lucide-react';
import {
  SCORE_FILTERS,
  SOURCE_FILTERS,
  TRACK_TABS,
  scoreTextClass,
  type QualifiedLead,
  type TrackFilter,
} from '../lib/leads';
import type { ConnState } from '../App';

const PILL_SPRING = { stiffness: 420, damping: 34, mass: 0.6 };
const INSPECTOR_MOTION = { duration: 0.13, ease: 'easeOut' as const };

interface StreamViewProps {
  leads: QualifiedLead[];
  conn: ConnState;
}

export default function StreamView({ leads, conn }: StreamViewProps) {
  const [selectedId, setSelectedId] = useState<string>('');
  const [minScore, setMinScore] = useState<number>(0);
  const [sourceFilter, setSourceFilter] = useState<string>('all');
  const [filter, setFilter] = useState<TrackFilter>('contracts');
  const [query, setQuery] = useState<string>('');
  const [copyState, setCopyState] = useState<Record<string, 'copied' | 'error'>>({});
  const [mobileSheet, setMobileSheet] = useState(false);
  const searchRef = useRef<HTMLInputElement>(null);

  const filtered = useMemo(() => {
    const expectedSource = filter === 'contracts' ? 'reddit' : 'hh';
    return leads.filter((item) => {
      if (item.source !== expectedSource) return false;
      if (item.score < minScore) return false;
      if (sourceFilter !== 'all' && item.source !== sourceFilter) return false;
      if (query.trim()) {
        const q = query.toLowerCase();
        const hay = `${item.title} ${item.body} ${item.origin_name} ${item.tech_stack.join(' ')}`.toLowerCase();
        if (!hay.includes(q)) return false;
      }
      return true;
    });
  }, [leads, minScore, sourceFilter, query, filter]);

  const activeLead = useMemo(
    () => filtered.find((l) => l.id === selectedId) || filtered[0] || null,
    [filtered, selectedId],
  );

  const resetFilters = () => {
    setMinScore(0);
    setSourceFilter('all');
    setQuery('');
    setFilter('contracts');
  };

  const copyPitch = (id: string, text: string) => {
    navigator.clipboard.writeText(text).then(
      () => {
        setCopyState((s) => ({ ...s, [id]: 'copied' }));
        setTimeout(() => {
          setCopyState((s) => {
            const next = { ...s };
            delete next[id];
            return next;
          });
        }, 1600);
      },
      () => setCopyState((s) => ({ ...s, [id]: 'error' })),
    );
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
                copyState={copyState[activeLead.id]}
                onCopy={copyPitch}
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
                copyState={copyState[activeLead.id]}
                onCopy={copyPitch}
              />
            </motion.div>
          </div>
        </div>
      )}
    </div>
  );
}

function InspectorContent({
  lead,
  filter,
  copyState,
  onCopy,
}: {
  lead: QualifiedLead;
  filter: TrackFilter;
  copyState?: 'copied' | 'error';
  onCopy: (id: string, text: string) => void;
}) {
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
          <button
            onClick={() => onCopy(lead.id, lead.draft_proposal)}
            className={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md border font-sans text-[11px] font-medium transition-colors duration-[120ms] ease-out ${
              copyState === 'copied'
                ? 'bg-emerald-400/15 border-emerald-400/30 text-emerald-200'
                : copyState === 'error'
                  ? 'bg-[rgba(141,153,174,0.06)] border-[var(--glass-border)] text-[var(--text-secondary)]'
                  : 'bg-[rgba(141,153,174,0.1)] hover:bg-[rgba(141,153,174,0.16)] border-[var(--glass-border)] text-[var(--text-primary)]'
            }`}
          >
            {copyState === 'copied' ? (
              <>
                <Check className="w-3.5 h-3.5" strokeWidth={1.75} />
                Copied
              </>
            ) : copyState === 'error' ? (
              'Clipboard blocked'
            ) : (
              <>
                <Copy className="w-3.5 h-3.5" strokeWidth={1.75} />
                Copy Pitch
              </>
            )}
          </button>
        </div>
        <p className="text-sm leading-relaxed text-[var(--text-secondary)] whitespace-pre-wrap">
          {lead.draft_proposal}
        </p>
      </section>
    </div>
  );
}
