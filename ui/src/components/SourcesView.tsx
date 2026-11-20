import { useState } from 'react';
import { AnimatePresence, motion } from 'framer-motion';
import { Loader2, Pause, Play, Plus, Server, Trash2 } from 'lucide-react';
import {
  createSource,
  deleteSource,
  updateSource,
  type ApiResult,
} from '../lib/api';
import type { CreateSourcePayload, Source, SourceType } from '../types/controlPlane';

const ROW_MOTION = { duration: 0.14, ease: 'easeOut' as const };

interface SourcesViewProps {
  sources: Source[] | null;
  offline: boolean;
  loading: boolean;
  onChanged: () => void;
}

const TYPE_OPTIONS: { value: SourceType; label: string }[] = [
  { value: 'tg', label: 'TG' },
  { value: 'hh', label: 'HH' },
  { value: 'reddit', label: 'Reddit' },
];

function typeBadgeClass(type: SourceType): string {
  switch (type) {
    case 'tg':
      return 'bg-sky-400/[0.08] border-sky-400/25 text-sky-300';
    case 'hh':
      return 'bg-amber-300/[0.08] border-amber-300/25 text-amber-200';
    case 'reddit':
      return 'bg-orange-400/[0.08] border-orange-400/25 text-orange-300';
    default:
      return 'bg-[rgba(141,153,174,0.08)] border-[var(--glass-border)] text-[var(--text-secondary)]';
  }
}

export default function SourcesView({ sources, offline, loading, onChanged }: SourcesViewProps) {
  const [type, setType] = useState<SourceType>('tg');
  const [target, setTarget] = useState('');
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const flash = (msg: string, isError = false) => {
    if (isError) {
      setError(msg);
      setNotice(null);
    } else {
      setNotice(msg);
      setError(null);
    }
    setTimeout(() => {
      setError((e) => (e === msg ? null : e));
      setNotice((n) => (n === msg ? null : n));
    }, 4000);
  };

  const handleResult = (label: string) => (r: ApiResult<unknown>) => {
    if (r.ok) {
      flash(`${label} — ok`);
      onChanged();
      return true;
    }
    flash(`${label} failed: ${r.status === 0 ? 'network unreachable' : r.message}`, true);
    return false;
  };

  const handleAdd = async () => {
    const t = target.trim();
    if (!t) {
      flash('Target is required', true);
      return;
    }
    setBusy('add');
    setError(null);
    const payload: CreateSourcePayload = { type, target: t };
    const ok = handleResult('Source added')(await createSource(payload));
    setBusy(null);
    if (ok) setTarget('');
  };

  const handleToggle = async (s: Source) => {
    setBusy(`toggle-${s.id}`);
    setError(null);
    handleResult(s.enabled ? 'Source paused' : 'Source resumed')(
      await updateSource(s.id, { enabled: !s.enabled }),
    );
    setBusy(null);
  };

  const handleDelete = async (s: Source) => {
    setBusy(`del-${s.id}`);
    setError(null);
    handleResult('Source deleted')(await deleteSource(s.id));
    setBusy(null);
  };

  return (
    <div className="h-full flex flex-col min-h-0 p-4 gap-4 overflow-y-auto">
      <div className="flex items-center gap-2 shrink-0">
        <Server className="w-4 h-4 text-[var(--text-muted)]" strokeWidth={1.75} />
        <span className="font-sans text-[10px] font-semibold uppercase tracking-widest text-[var(--text-muted)]">
          Connectors
        </span>
      </div>

      {/* Creation bar */}
      <div className="glass-panel rounded-lg p-3 flex flex-col sm:flex-row items-stretch sm:items-center gap-2 shrink-0">
        <div className="flex rounded-md overflow-hidden border border-[var(--glass-border)]">
          {TYPE_OPTIONS.map((opt) => (
            <button
              key={opt.value}
              onClick={() => setType(opt.value)}
              className={`px-3 py-1.5 font-sans text-[11px] font-medium uppercase tracking-wide transition-colors duration-[120ms] ease-out ${
                type === opt.value
                  ? 'bg-[rgba(141,153,174,0.18)] text-[var(--text-primary)]'
                  : 'text-[var(--text-muted)] hover:text-[var(--text-secondary)] bg-[rgba(17,19,28,0.4)]'
              }`}
            >
              {opt.label}
            </button>
          ))}
        </div>
        <input
          value={target}
          onChange={(e) => setTarget(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') void handleAdd();
          }}
          placeholder={
            type === 'tg'
              ? '@channel or t.me/…'
              : type === 'hh'
                ? 'search query, e.g. golang developer'
                : 'r/subreddit'
          }
          className="glass-input flex-1 rounded-md px-3 py-1.5 font-mono text-xs min-w-0"
        />
        <button
          onClick={() => void handleAdd()}
          disabled={busy === 'add' || offline}
          className="inline-flex items-center justify-center gap-1.5 px-3 py-1.5 rounded-md bg-[rgba(141,153,174,0.18)] hover:bg-[rgba(141,153,174,0.26)] border border-[var(--glass-border-hover)] font-sans text-xs font-medium text-[var(--text-primary)] transition-colors duration-[120ms] ease-out disabled:opacity-40 disabled:cursor-not-allowed"
        >
          {busy === 'add' ? (
            <Loader2 className="w-3.5 h-3.5 animate-spin" strokeWidth={2} />
          ) : (
            <Plus className="w-3.5 h-3.5" strokeWidth={2} />
          )}
          Add Source
        </button>
      </div>

      {(error || notice) && (
        <p
          className={`shrink-0 font-sans text-[11px] ${
            error ? 'text-red-300/90' : 'text-emerald-300/90'
          }`}
        >
          {error ?? notice}
        </p>
      )}

      {/* Sources table */}
      <div className="glass-panel rounded-lg overflow-hidden shrink-0">
        {offline ? (
          <div className="px-4 py-8 flex flex-col items-center gap-2">
            <p className="font-sans text-xs text-[var(--text-muted)]">
              Control Plane API endpoint offline / awaiting backend
            </p>
            <p className="font-sans text-[10px] text-[var(--text-muted)]">
              /api/v1/sources is not reachable yet — stream ingestion is unaffected
            </p>
          </div>
        ) : loading && sources === null ? (
          <div className="px-4 py-8 flex items-center justify-center gap-2 font-sans text-xs text-[var(--text-muted)]">
            <Loader2 className="w-3.5 h-3.5 animate-spin" strokeWidth={2} />
            Loading sources…
          </div>
        ) : !sources || sources.length === 0 ? (
          <div className="px-4 py-8 text-center font-sans text-xs text-[var(--text-muted)]">
            No sources configured — add one above
          </div>
        ) : (
          <table className="w-full text-left">
            <thead>
              <tr className="border-b border-[var(--glass-border)] font-sans text-[10px] font-semibold uppercase tracking-wider text-[var(--text-muted)]">
                <th className="px-3 py-2 font-medium">Type</th>
                <th className="px-3 py-2 font-medium">Target</th>
                <th className="px-3 py-2 font-medium">Status</th>
                <th className="px-3 py-2 font-medium">Added</th>
                <th className="px-3 py-2 font-medium text-right">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-[rgba(141,153,174,0.07)]">
              <AnimatePresence initial={false}>
                {sources.map((s) => (
                  <motion.tr
                    key={s.id}
                    layout
                    initial={{ opacity: 0 }}
                    animate={{ opacity: 1 }}
                    exit={{ opacity: 0 }}
                    transition={ROW_MOTION}
                    className="hover:bg-[rgba(141,153,174,0.04)] transition-colors duration-[120ms] ease-out"
                  >
                    <td className="px-3 py-2">
                      <span
                        className={`inline-block px-1.5 py-0.5 rounded border font-sans text-[10px] font-medium uppercase tracking-wide ${typeBadgeClass(s.type)}`}
                      >
                        {s.type}
                      </span>
                    </td>
                    <td className="px-3 py-2 font-mono text-xs text-[var(--text-primary)] truncate max-w-[280px]">
                      {s.target}
                    </td>
                    <td className="px-3 py-2">
                      <span className="inline-flex items-center gap-1.5 font-sans text-[11px]">
                        <span
                          className={`w-1.5 h-1.5 rounded-full ${
                            s.enabled ? 'bg-emerald-400/80 cp-status-live' : 'bg-[var(--text-muted)]'
                          }`}
                        />
                        <span className={s.enabled ? 'text-emerald-300/90' : 'text-[var(--text-muted)]'}>
                          {s.enabled ? 'ACTIVE' : 'PAUSED'}
                        </span>
                      </span>
                    </td>
                    <td className="px-3 py-2 font-mono text-[11px] text-[var(--text-muted)] tabular-nums">
                      {s.created_at ? new Date(s.created_at).toLocaleDateString() : '—'}
                    </td>
                    <td className="px-3 py-2">
                      <div className="flex items-center justify-end gap-1">
                        <button
                          onClick={() => void handleToggle(s)}
                          disabled={busy !== null || offline}
                          title={s.enabled ? 'Pause source' : 'Resume source'}
                          className="p-1.5 rounded-md text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[rgba(141,153,174,0.1)] transition-colors duration-[120ms] ease-out disabled:opacity-40"
                        >
                          {busy === `toggle-${s.id}` ? (
                            <Loader2 className="w-3.5 h-3.5 animate-spin" strokeWidth={2} />
                          ) : s.enabled ? (
                            <Pause className="w-3.5 h-3.5" strokeWidth={1.75} />
                          ) : (
                            <Play className="w-3.5 h-3.5" strokeWidth={1.75} />
                          )}
                        </button>
                        <button
                          onClick={() => void handleDelete(s)}
                          disabled={busy !== null || offline}
                          title="Delete source"
                          className="p-1.5 rounded-md text-[var(--text-muted)] hover:text-red-300 hover:bg-[rgba(141,153,174,0.1)] transition-colors duration-[120ms] ease-out disabled:opacity-40"
                        >
                          {busy === `del-${s.id}` ? (
                            <Loader2 className="w-3.5 h-3.5 animate-spin" strokeWidth={2} />
                          ) : (
                            <Trash2 className="w-3.5 h-3.5" strokeWidth={1.75} />
                          )}
                        </button>
                      </div>
                    </td>
                  </motion.tr>
                ))}
              </AnimatePresence>
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
