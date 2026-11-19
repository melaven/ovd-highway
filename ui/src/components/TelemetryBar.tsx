import { Loader2, LockKeyhole, LockOpen, RefreshCw } from 'lucide-react';
import type { ConnState } from '../App';

interface TelemetryBarProps {
  conn: ConnState;
  sourcesCount: number | null;
  sourcesOffline: boolean;
  minScore: number | null;
  hasToken: boolean;
  onOpenTokenLock: () => void;
  onReconnect: () => void;
}

function wsLabel(conn: ConnState): { text: string; tone: string } {
  switch (conn) {
    case 'live':
      return { text: 'WS: LIVE', tone: 'bg-emerald-400/80 cp-status-live' };
    case 'connecting':
      return { text: 'WS: CONNECTING', tone: 'bg-amber-300/70' };
    case 'auth_required':
      return { text: 'WS: AUTH REQUIRED', tone: 'bg-amber-300/70' };
    case 'reconnecting':
      return { text: 'WS: RECONNECTING', tone: 'bg-amber-300/70' };
    default:
      return { text: 'WS: OFFLINE', tone: 'bg-[var(--text-muted)]' };
  }
}

export default function TelemetryBar({
  conn,
  sourcesCount,
  sourcesOffline,
  minScore,
  hasToken,
  onOpenTokenLock,
  onReconnect,
}: TelemetryBarProps) {
  const ws = wsLabel(conn);
  const showRetry = conn === 'offline' || conn === 'reconnecting';

  return (
    <div className="glass-elevated fixed bottom-3 left-1/2 -translate-x-1/2 z-40 flex items-center gap-4 rounded-full px-4 py-1.5 font-mono text-[11px] tracking-wide text-[var(--text-secondary)] whitespace-nowrap">
      <span className="flex items-center gap-1.5">
        <span className={`w-1.5 h-1.5 rounded-full ${ws.tone}`} />
        {ws.text}
        {conn === 'connecting' && (
          <Loader2 className="w-3 h-3 animate-spin text-[var(--text-muted)]" strokeWidth={2} />
        )}
      </span>

      <span className="h-3 w-px bg-[var(--glass-border)]" />

      <span>
        SOURCES:{' '}
        <span className="text-[var(--text-primary)] tabular-nums">
          {sourcesOffline ? 'API OFFLINE' : sourcesCount === null ? '…' : sourcesCount}
        </span>
      </span>

      <span className="h-3 w-px bg-[var(--glass-border)]" />

      <span>
        MIN SCORE:{' '}
        <span className="text-[var(--text-primary)] tabular-nums">
          {minScore === null ? '—' : minScore}
        </span>
      </span>

      <span className="h-3 w-px bg-[var(--glass-border)]" />

      <button
        onClick={onOpenTokenLock}
        title={hasToken ? 'Admin token configured' : 'No admin token — click to configure'}
        className={`flex items-center gap-1.5 transition-colors duration-[120ms] ease-out ${
          hasToken
            ? 'text-emerald-300/90 hover:text-emerald-200'
            : 'text-[var(--text-muted)] hover:text-[var(--text-secondary)]'
        }`}
      >
        {hasToken ? (
          <LockKeyhole className="w-3 h-3" strokeWidth={2} />
        ) : (
          <LockOpen className="w-3 h-3" strokeWidth={2} />
        )}
        {hasToken ? 'AUTH: SET' : 'AUTH: OPEN'}
      </button>

      {showRetry && (
        <>
          <span className="h-3 w-px bg-[var(--glass-border)]" />
          <button
            onClick={onReconnect}
            title="Reconnect WebSocket"
            className="flex items-center gap-1 text-[var(--text-muted)] hover:text-[var(--text-primary)] transition-colors duration-[120ms] ease-out"
          >
            <RefreshCw className="w-3 h-3" strokeWidth={2} />
            RETRY
          </button>
        </>
      )}
    </div>
  );
}
