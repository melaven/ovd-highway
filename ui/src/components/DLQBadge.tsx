import { useCallback, useEffect, useRef, useState } from 'react';
import { ArchiveRestore, Inbox, Loader2 } from 'lucide-react';
import { listDLQ, retryDLQ, type DLQList } from '../lib/api';

const POLL_MS = 30_000;
const MAX_ITEMS = 10;

interface DLQBadgeProps {
  hasToken: boolean;
}

function formatTime(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleTimeString();
}

export default function DLQBadge({ hasToken }: DLQBadgeProps) {
  const [data, setData] = useState<DLQList | null>(null);
  const [open, setOpen] = useState(false);
  const [retrying, setRetrying] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const mounted = useRef(true);

  const refresh = useCallback(async () => {
    const r = await listDLQ();
    if (!mounted.current) return;
    // Offline/5xx results keep the last known count instead of flickering.
    if (r.ok) setData(r.data ?? { count: 0, items: [] });
  }, []);

  useEffect(() => {
    mounted.current = true;
    if (!hasToken) {
      return () => {
        mounted.current = false;
      };
    }
    const tick = () => void refresh();
    const start = window.setTimeout(tick, 0);
    const id = window.setInterval(tick, POLL_MS);
    return () => {
      mounted.current = false;
      window.clearTimeout(start);
      window.clearInterval(id);
    };
  }, [hasToken, refresh]);

  const handleRetry = async () => {
    if (retrying) return;
    setRetrying(true);
    setNotice(null);
    const r = await retryDLQ();
    if (!mounted.current) return;
    setRetrying(false);
    if (r.ok) {
      await refresh();
    } else {
      setNotice(r.message || 'Retry failed');
    }
  };

  if (!hasToken) return null;

  const count = data?.count ?? null;
  const alert = count !== null && count > 0;

  return (
    <span className="relative">
      {open && (
        <div
          className="fixed inset-0 z-40"
          onClick={() => setOpen(false)}
          aria-hidden="true"
        />
      )}
      <button
        onClick={() => setOpen((o) => !o)}
        title="Dead letter queue — failed LLM evaluations"
        className={`flex items-center gap-1.5 transition-colors duration-[120ms] ease-out ${
          alert
            ? 'text-amber-300/90 hover:text-amber-200'
            : 'text-[var(--text-muted)] hover:text-[var(--text-secondary)]'
        }`}
      >
        <Inbox className="w-3 h-3" strokeWidth={2} />
        DLQ:{' '}
        <span className="text-[var(--text-primary)] tabular-nums">
          {count === null ? '…' : count}
        </span>
      </button>

      {open && (
        <div className="glass-elevated absolute bottom-full left-1/2 -translate-x-1/2 mb-2 z-50 w-80 max-h-72 flex flex-col rounded-lg overflow-hidden">
          <div className="flex items-center justify-between px-3 py-2 border-b border-[var(--glass-border)]">
            <span className="font-mono text-[10px] font-medium uppercase tracking-wider text-[var(--text-muted)]">
              Dead Letter Queue
            </span>
            <button
              onClick={() => void handleRetry()}
              disabled={retrying || !alert}
              className="inline-flex items-center gap-1 px-2 py-0.5 rounded-md bg-[rgba(141,153,174,0.14)] hover:bg-[rgba(141,153,174,0.22)] border border-[var(--glass-border)] font-sans text-[10px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-colors duration-[120ms] ease-out disabled:opacity-40 disabled:cursor-not-allowed"
            >
              {retrying ? (
                <Loader2 className="w-3 h-3 animate-spin" strokeWidth={2} />
              ) : (
                <ArchiveRestore className="w-3 h-3" strokeWidth={2} />
              )}
              Retry all
            </button>
          </div>

          {notice && (
            <p className="px-3 py-1.5 font-sans text-[10px] text-amber-300/80 border-b border-[var(--glass-border)]">
              {notice}
            </p>
          )}

          <div className="overflow-y-auto">
            {!data || data.items.length === 0 ? (
              <p className="px-3 py-4 font-sans text-[11px] text-[var(--text-muted)] text-center">
                No dead letters
              </p>
            ) : (
              data.items.slice(0, MAX_ITEMS).map((it) => (
                <div
                  key={it.id}
                  className="px-3 py-2 border-b border-[var(--glass-border)] last:border-b-0"
                >
                  <div className="flex items-center justify-between gap-2">
                    <span className="font-mono text-[10px] text-[var(--text-secondary)] truncate">
                      {it.source}
                    </span>
                    <span className="font-mono text-[10px] text-[var(--text-muted)] tabular-nums shrink-0">
                      {formatTime(it.created_at)} · ×{it.attempts}
                    </span>
                  </div>
                  <p className="mt-0.5 font-sans text-[11px] text-[var(--text-primary)] truncate">
                    {it.error || 'unknown error'}
                  </p>
                  {it.payload_preview && (
                    <p className="mt-0.5 font-mono text-[10px] text-[var(--text-muted)] truncate">
                      {it.payload_preview}
                    </p>
                  )}
                </div>
              ))
            )}
          </div>
        </div>
      )}
    </span>
  );
}
