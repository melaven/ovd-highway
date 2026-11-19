import { useEffect, useState } from 'react';
import { AnimatePresence, motion } from 'framer-motion';
import { KeyRound, Trash2, X } from 'lucide-react';
import { clearAdminToken, getAdminToken, hasAdminToken, setAdminToken } from '../lib/api';

const EASE = [0.22, 1, 0.36, 1] as const;

interface TokenLockModalProps {
  open: boolean;
  reason?: string;
  onClose: () => void;
  onTokenSaved: () => void;
}

export default function TokenLockModal({ open, reason, onClose, onTokenSaved }: TokenLockModalProps) {
  const [value, setValue] = useState('');
  const [confirmClear, setConfirmClear] = useState(false);

  useEffect(() => {
    if (open) {
      setValue('');
      setConfirmClear(false);
    }
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [open, onClose]);

  const handleSave = () => {
    if (!value.trim()) return;
    setAdminToken(value);
    onTokenSaved();
    onClose();
  };

  const handleClear = () => {
    clearAdminToken();
    setConfirmClear(false);
    onTokenSaved();
    onClose();
  };

  return (
    <AnimatePresence>
      {open && (
        <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
          <motion.div
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            transition={{ duration: 0.15, ease: EASE }}
            className="absolute inset-0 bg-[rgba(10,11,17,0.6)]"
            onClick={onClose}
          />
          <motion.div
            initial={{ opacity: 0, scale: 0.97 }}
            animate={{ opacity: 1, scale: 1 }}
            exit={{ opacity: 0, scale: 0.97 }}
            transition={{ duration: 0.15, ease: EASE }}
            className="glass-elevated relative w-full max-w-sm rounded-xl p-5"
          >
            <div className="flex items-center justify-between mb-4">
              <span className="flex items-center gap-2 font-sans text-xs font-semibold uppercase tracking-wider text-[var(--text-secondary)]">
                <KeyRound className="w-4 h-4" strokeWidth={1.75} />
                Token Lock
              </span>
              <button
                onClick={onClose}
                aria-label="Close token lock"
                className="p-1 rounded-md text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[rgba(141,153,174,0.1)] transition-colors duration-[120ms]"
              >
                <X className="w-4 h-4" strokeWidth={1.75} />
              </button>
            </div>

            {reason && (
              <p className="mb-3 font-sans text-[11px] text-amber-200/90 leading-relaxed">
                {reason}
              </p>
            )}

            <label className="block font-sans text-[10px] font-semibold uppercase tracking-wider text-[var(--text-muted)] mb-1.5">
              ADMIN_API_TOKEN
            </label>
            <input
              type="password"
              value={value}
              onChange={(e) => setValue(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') handleSave();
              }}
              placeholder={hasAdminToken() ? '•••••••• (token set — enter to replace)' : 'paste admin token'}
              autoFocus
              className="glass-input w-full rounded-md px-3 py-2 font-mono text-xs mb-4"
            />

            <div className="flex items-center gap-2">
              <button
                onClick={handleSave}
                disabled={!value.trim()}
                className="flex-1 px-3 py-1.5 rounded-md bg-[rgba(141,153,174,0.18)] hover:bg-[rgba(141,153,174,0.26)] border border-[var(--glass-border-hover)] font-sans text-xs font-medium text-[var(--text-primary)] transition-colors duration-[120ms] ease-out disabled:opacity-40 disabled:cursor-not-allowed"
              >
                Save &amp; Reconnect
              </button>
              {hasAdminToken() && (
                <button
                  onClick={() => (confirmClear ? handleClear() : setConfirmClear(true))}
                  className={`px-3 py-1.5 rounded-md border font-sans text-xs transition-colors duration-[120ms] ease-out ${
                    confirmClear
                      ? 'bg-red-500/15 border-red-400/40 text-red-300'
                      : 'border-[var(--glass-border)] text-[var(--text-muted)] hover:text-[var(--text-secondary)] hover:border-[var(--glass-border-hover)]'
                  }`}
                >
                  <span className="inline-flex items-center gap-1.5">
                    <Trash2 className="w-3.5 h-3.5" strokeWidth={1.75} />
                    {confirmClear ? 'Confirm clear' : 'Clear'}
                  </span>
                </button>
              )}
            </div>

            {getAdminToken() && (
              <p className="mt-3 font-sans text-[10px] text-[var(--text-muted)]">
                Token stored locally in <span className="text-[var(--text-secondary)]">localStorage</span> as{' '}
                <code className="font-mono text-[var(--text-secondary)]">ovd_admin_token</code>.
              </p>
            )}
          </motion.div>
        </div>
      )}
    </AnimatePresence>
  );
}
