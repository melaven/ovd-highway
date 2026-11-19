import { useEffect, useMemo, useState } from 'react';
import { FileText, Loader2, RotateCcw, Save, SlidersHorizontal } from 'lucide-react';
import { putSettings, type ApiResult } from '../lib/api';
import type { EngineSettings } from '../types/controlPlane';

interface RulesViewProps {
  settings: EngineSettings | null;
  offline: boolean;
  loading: boolean;
  onSaved: (settings: EngineSettings) => void;
}

export default function RulesView({ settings, offline, loading, onSaved }: RulesViewProps) {
  const [prompt, setPrompt] = useState('');
  const [minScore, setMinScore] = useState(0);
  const [saving, setSaving] = useState(false);
  const [status, setStatus] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  useEffect(() => {
    if (settings) {
      setPrompt(settings.system_prompt ?? '');
      setMinScore(Number(settings.min_score ?? 0));
    }
  }, [settings]);

  const stats = useMemo(() => {
    const chars = prompt.length;
    const lines = prompt.length === 0 ? 0 : prompt.split('\n').length;
    return { chars, lines };
  }, [prompt]);

  const flash = (kind: 'ok' | 'err', text: string) => {
    setStatus({ kind, text });
    setTimeout(() => setStatus((s) => (s?.text === text ? null : s)), 5000);
  };

  const handleSave = async () => {
    if (saving) return;
    setSaving(true);
    setStatus(null);
    const payload: EngineSettings = {
      system_prompt: prompt,
      min_score: Math.max(0, Math.min(100, Math.round(minScore))),
    };
    const r: ApiResult<EngineSettings> = await putSettings(payload);
    setSaving(false);
    if (r.ok) {
      onSaved(r.data ?? payload);
      flash('ok', 'Settings saved');
    } else if (r.status === 401) {
      flash('err', 'Auth required — unlock via the rail token button');
    } else {
      flash('err', `Save failed: ${r.status === 0 ? 'network unreachable' : r.message}`);
    }
  };

  const handleReset = () => {
    if (settings) {
      setPrompt(settings.system_prompt ?? '');
      setMinScore(Number(settings.min_score ?? 0));
      setStatus(null);
    }
  };

  const disabled = saving || offline || !settings;

  return (
    <div className="h-full flex flex-col min-h-0 p-4 gap-4 overflow-y-auto">
      <div className="flex items-center gap-2 shrink-0">
        <SlidersHorizontal className="w-4 h-4 text-[var(--text-muted)]" strokeWidth={1.75} />
        <span className="font-sans text-[10px] font-semibold uppercase tracking-widest text-[var(--text-muted)]">
          Prompt &amp; Threshold Studio
        </span>
      </div>

      {offline && (
        <p className="shrink-0 font-sans text-[11px] text-[var(--text-muted)]">
          Control Plane API endpoint offline / awaiting backend — showing cached/empty state
        </p>
      )}
      {loading && settings === null && (
        <p className="shrink-0 flex items-center gap-2 font-sans text-[11px] text-[var(--text-muted)]">
          <Loader2 className="w-3 h-3 animate-spin" strokeWidth={2} />
          Loading settings…
        </p>
      )}

      {/* Prompt editor */}
      <section className="glass-panel rounded-lg p-3 flex flex-col gap-2 shrink-0">
        <div className="flex items-center justify-between">
          <span className="flex items-center gap-1.5 font-mono text-[10px] font-medium uppercase tracking-wider text-[var(--text-muted)]">
            <FileText className="w-3.5 h-3.5" strokeWidth={1.75} />
            system_prompt
          </span>
          <span className="font-mono text-[10px] text-[var(--text-muted)] tabular-nums">
            {stats.chars} chars · {stats.lines} lines
          </span>
        </div>
        <textarea
          value={prompt}
          onChange={(e) => setPrompt(e.target.value)}
          spellCheck={false}
          placeholder="Qualification system prompt…"
          className="glass-input w-full h-56 rounded-md p-3 font-mono text-xs leading-relaxed resize-y"
        />
      </section>

      {/* Threshold */}
      <section className="glass-panel rounded-lg p-3 flex flex-col gap-3 shrink-0">
        <div className="flex items-center justify-between">
          <span className="font-mono text-[10px] font-medium uppercase tracking-wider text-[var(--text-muted)]">
            min_score
          </span>
          <span className="font-mono text-sm text-[var(--text-primary)] tabular-nums">
            {Math.max(0, Math.min(100, Math.round(minScore)))}
            <span className="text-[var(--text-muted)] text-[10px]"> /100</span>
          </span>
        </div>
        <input
          type="range"
          min={0}
          max={100}
          step={1}
          value={Math.max(0, Math.min(100, Math.round(minScore)))}
          onChange={(e) => setMinScore(Number(e.target.value))}
          className="w-full"
        />
        <div className="flex items-center gap-2">
          <input
            type="number"
            min={0}
            max={100}
            value={minScore}
            onChange={(e) => setMinScore(Number(e.target.value))}
            className="glass-input w-20 rounded-md px-2 py-1 font-mono text-xs tabular-nums"
          />
          <span className="font-sans text-[10px] text-[var(--text-muted)]">
            signals below this score are dropped before persistence
          </span>
        </div>
      </section>

      {/* Actions + status */}
      <div className="flex items-center gap-2 shrink-0">
        <button
          onClick={() => void handleSave()}
          disabled={disabled}
          className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md bg-[rgba(141,153,174,0.18)] hover:bg-[rgba(141,153,174,0.26)] border border-[var(--glass-border-hover)] font-sans text-xs font-medium text-[var(--text-primary)] transition-colors duration-[120ms] ease-out disabled:opacity-40 disabled:cursor-not-allowed"
        >
          {saving ? (
            <Loader2 className="w-3.5 h-3.5 animate-spin" strokeWidth={2} />
          ) : (
            <Save className="w-3.5 h-3.5" strokeWidth={1.75} />
          )}
          Save Settings
        </button>
        <button
          onClick={handleReset}
          disabled={!settings || saving}
          className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-[var(--glass-border)] font-sans text-xs text-[var(--text-muted)] hover:text-[var(--text-secondary)] hover:border-[var(--glass-border-hover)] transition-colors duration-[120ms] ease-out disabled:opacity-40"
        >
          <RotateCcw className="w-3.5 h-3.5" strokeWidth={1.75} />
          Reset
        </button>
        {status && (
          <span
            className={`ml-auto font-sans text-[11px] ${
              status.kind === 'ok' ? 'text-emerald-300/90' : 'text-red-300/90'
            }`}
          >
            {status.text}
          </span>
        )}
      </div>
    </div>
  );
}
