import { useEffect, useState } from 'react';
import { AnimatePresence, MotionConfig, motion } from 'framer-motion';
import IconRail from './components/IconRail';
import RulesView from './components/RulesView';
import SourcesView from './components/SourcesView';
import StreamView from './components/StreamView';
import TelemetryBar from './components/TelemetryBar';
import TokenLockModal from './components/TokenLockModal';
import {
  getSettings,
  hasAdminToken,
  isOfflineResult,
  listSources,
  onUnauthorized,
  wsUrl,
} from './lib/api';
import { normalizeIncoming, type QualifiedLead } from './lib/leads';
import type { EngineSettings, Source } from './types/controlPlane';

export type ViewId = 'stream' | 'sources' | 'rules';
export type ConnState =
  | 'connecting'
  | 'live'
  | 'reconnecting'
  | 'offline'
  | 'auth_required';

const VIEW_EASE = [0.22, 1, 0.36, 1] as const;

export default function App() {
  const [view, setView] = useState<ViewId>('stream');
  const [leads, setLeads] = useState<QualifiedLead[]>([]);
  const [conn, setConn] = useState<ConnState>('connecting');
  const [wsNonce, setWsNonce] = useState(0);
  const [tokenTick, setTokenTick] = useState(0);
  const [sourcesTick, setSourcesTick] = useState(0);

  const [sources, setSources] = useState<Source[] | null>(null);
  const [sourcesLoading, setSourcesLoading] = useState(true);
  const [sourcesOffline, setSourcesOffline] = useState(false);
  const [sourcesAuthRequired, setSourcesAuthRequired] = useState(false);

  const [settings, setSettings] = useState<EngineSettings | null>(null);
  const [settingsLoading, setSettingsLoading] = useState(true);
  const [settingsOffline, setSettingsOffline] = useState(false);
  const [settingsAuthRequired, setSettingsAuthRequired] = useState(false);

  const [tokenOpen, setTokenOpen] = useState(false);
  const [tokenReason, setTokenReason] = useState<string | undefined>(undefined);

  const hasToken = hasAdminToken();

  // 401s from any REST call surface the Token Lock modal exactly once.
  useEffect(() => onUnauthorized((message) => {
    setTokenReason(message);
    setTokenOpen(true);
  }), []);

  // Sources hydration — refetches on token change OR local CRUD mutations.
  useEffect(() => {
    let disposed = false;
    setSourcesLoading(true);

    void listSources().then((r) => {
      if (disposed) return;
      if (r.ok) {
        setSources(r.data ?? []);
        setSourcesOffline(false);
        setSourcesAuthRequired(false);
      } else if (r.status === 401) {
        setSources(null);
        setSourcesOffline(false);
        setSourcesAuthRequired(true);
      } else {
        setSources(null);
        setSourcesOffline(isOfflineResult(r) || !r.ok);
        setSourcesAuthRequired(false);
      }
      setSourcesLoading(false);
    });

    return () => {
      disposed = true;
    };
  }, [tokenTick, sourcesTick]);

  // Settings hydration — refetches only when the token changes.
  useEffect(() => {
    let disposed = false;
    setSettingsLoading(true);

    void getSettings().then((r) => {
      if (disposed) return;
      if (r.ok) {
        setSettings(r.data ?? null);
        setSettingsOffline(false);
        setSettingsAuthRequired(false);
      } else if (r.status === 401) {
        setSettings(null);
        setSettingsOffline(false);
        setSettingsAuthRequired(true);
      } else {
        setSettings(null);
        setSettingsOffline(isOfflineResult(r) || !r.ok);
        setSettingsAuthRequired(false);
      }
      setSettingsLoading(false);
    });

    return () => {
      disposed = true;
    };
  }, [tokenTick]);

  // WebSocket lead stream — reconnects on token change so ?token= is fresh.
  useEffect(() => {
    let ws: WebSocket | null = null;
    let timer: number | undefined;
    let disposed = false;

    const isAuthClose = (code: number, reason: string) =>
      code === 4401 || code === 4001 || /auth/i.test(reason);

    const connect = () => {
      if (disposed) return;
      try {
        ws = new WebSocket(wsUrl());

        ws.onopen = () => {
          if (!disposed) setConn('live');
        };
        ws.onerror = () => {
          /* close handler drives state */
        };
        ws.onclose = (event) => {
          if (disposed) return;
          if (isAuthClose(event.code, event.reason)) {
            setConn('auth_required');
            setTokenReason('WebSocket rejected the session — authentication required.');
            setTokenOpen(true);
            return;
          }
          setConn('reconnecting');
          timer = window.setTimeout(connect, 4000);
        };
        ws.onmessage = (ev) => {
          try {
            const msg: unknown = JSON.parse(ev.data);
            if (msg && typeof msg === 'object' && 'type' in msg) {
              const typedMsg = msg as { type?: unknown; payload?: unknown };
              if (typedMsg.type === 'init') {
                const payload = Array.isArray(typedMsg.payload) ? typedMsg.payload : [];
                const incoming = payload
                  .map(normalizeIncoming)
                  .filter((lead): lead is QualifiedLead => lead !== null);
                setLeads(incoming);
              } else if (typedMsg.type === 'lead' && typedMsg.payload) {
                const newLead = normalizeIncoming(typedMsg.payload);
                if (newLead) setLeads((prev) => [newLead, ...prev]);
              }
            } else {
              const newLead = normalizeIncoming(msg);
              if (newLead) setLeads((prev) => [newLead, ...prev]);
            }
          } catch {
            /* malformed frame */
          }
        };
      } catch {
        if (!disposed) {
          setConn('reconnecting');
          timer = window.setTimeout(connect, 4000);
        }
      }
    };

    setConn('connecting');
    connect();
    return () => {
      disposed = true;
      if (timer !== undefined) clearTimeout(timer);
      ws?.close();
    };
  }, [wsNonce, tokenTick]);

  const reconnect = () => {
    setConn('connecting');
    setWsNonce((n) => n + 1);
  };

  const bumpToken = () => setTokenTick((t) => t + 1);

  const openTokenLock = () => {
    setTokenReason(undefined);
    setTokenOpen(true);
  };

  const refreshSources = () => setSourcesTick((k) => k + 1);

  const sourcesCount =
    sourcesOffline || sourcesAuthRequired || sources === null ? null : sources.length;
  const minScore =
    settings && !settingsOffline && !settingsAuthRequired ? settings.min_score : null;

  return (
    <MotionConfig reducedMotion="user">
      <div className="h-screen w-screen overflow-hidden relative select-none">
        <IconRail
          view={view}
          onNavigate={setView}
          hasToken={hasToken}
          onOpenTokenLock={openTokenLock}
        />

        <main className="h-full pl-14 sm:pl-16 pr-3 pt-3 pb-14 flex flex-col min-w-0">
          <AnimatePresence mode="wait" initial={false}>
            <motion.div
              key={view}
              className="flex-1 min-h-0 flex flex-col"
              initial={{ opacity: 0, y: 6 }}
              animate={{ opacity: 1, y: 0 }}
              exit={{ opacity: 0, y: -4 }}
              transition={{ duration: 0.14, ease: VIEW_EASE }}
            >
              {view === 'stream' && <StreamView leads={leads} conn={conn} />}
              {view === 'sources' && (
                <SourcesView
                  sources={sources}
                  offline={sourcesOffline}
                  authRequired={sourcesAuthRequired}
                  loading={sourcesLoading}
                  onChanged={refreshSources}
                  onOpenTokenLock={openTokenLock}
                />
              )}
              {view === 'rules' && (
                <RulesView
                  settings={settings}
                  offline={settingsOffline}
                  authRequired={settingsAuthRequired}
                  loading={settingsLoading}
                  onOpenTokenLock={openTokenLock}
                  onSaved={(s) => {
                    setSettings(s);
                    setSettingsOffline(false);
                    setSettingsAuthRequired(false);
                  }}
                />
              )}
            </motion.div>
          </AnimatePresence>
        </main>

        <TelemetryBar
          conn={conn}
          sourcesCount={sourcesCount}
          sourcesOffline={sourcesOffline}
          minScore={minScore}
          hasToken={hasToken}
          onOpenTokenLock={openTokenLock}
          onReconnect={reconnect}
        />

        <TokenLockModal
          open={tokenOpen}
          reason={tokenReason}
          onClose={() => setTokenOpen(false)}
          onTokenSaved={bumpToken}
        />
      </div>
    </MotionConfig>
  );
}
