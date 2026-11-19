import { LockKeyhole, LockOpen, Radio, Server, SlidersHorizontal } from 'lucide-react';
import { motion } from 'framer-motion';
import type { ViewId } from '../App';

const PILL_SPRING = { stiffness: 420, damping: 34, mass: 0.6 };

interface RailItem {
  id: ViewId;
  label: string;
  icon: typeof Radio;
}

const ITEMS: RailItem[] = [
  { id: 'stream', label: 'Stream', icon: Radio },
  { id: 'sources', label: 'Sources', icon: Server },
  { id: 'rules', label: 'Rules', icon: SlidersHorizontal },
];

interface IconRailProps {
  view: ViewId;
  onNavigate: (view: ViewId) => void;
  hasToken: boolean;
  onOpenTokenLock: () => void;
}

function RailTooltip({ label }: { label: string }) {
  return (
    <span className="pointer-events-none absolute left-full ml-2.5 px-2 py-1 rounded-md glass-elevated font-sans text-[10px] font-medium uppercase tracking-wider text-[var(--text-secondary)] whitespace-nowrap opacity-0 -translate-x-1 group-hover:opacity-100 group-hover:translate-x-0 transition-[opacity,transform] duration-[110ms] ease-out">
      {label}
    </span>
  );
}

export default function IconRail({ view, onNavigate, hasToken, onOpenTokenLock }: IconRailProps) {
  return (
    <nav
      aria-label="Control plane sections"
      className="glass-panel fixed left-3 top-1/2 -translate-y-1/2 z-40 flex flex-col items-center gap-1 rounded-full px-1.5 py-2"
    >
      {ITEMS.map(({ id, label, icon: Icon }) => {
        const active = view === id;
        return (
          <button
            key={id}
            onClick={() => onNavigate(id)}
            aria-label={label}
            aria-current={active ? 'page' : undefined}
            className={`group relative w-9 h-9 rounded-full flex items-center justify-center transition-colors duration-[120ms] ease-out ${
              active
                ? 'text-[var(--text-primary)]'
                : 'text-[var(--text-muted)] hover:text-[var(--text-secondary)]'
            }`}
          >
            {active && (
              <motion.span
                layoutId="rail-active-pill"
                transition={PILL_SPRING}
                className="absolute inset-0 rounded-full bg-[rgba(141,153,174,0.16)]"
              />
            )}
            <Icon className="relative w-4 h-4" strokeWidth={1.75} />
            <RailTooltip label={label} />
          </button>
        );
      })}

      <span className="my-1 h-px w-5 bg-[var(--glass-border)]" />

      <button
        onClick={onOpenTokenLock}
        aria-label="Auth token lock"
        className="group relative w-9 h-9 rounded-full flex items-center justify-center text-[var(--text-muted)] hover:text-[var(--text-secondary)] hover:bg-[rgba(141,153,174,0.08)] transition-colors duration-[120ms] ease-out"
      >
        {hasToken ? (
          <LockKeyhole className="w-4 h-4" strokeWidth={1.75} />
        ) : (
          <LockOpen className="w-4 h-4" strokeWidth={1.75} />
        )}
        <span
          className={`absolute top-1.5 right-1.5 w-1.5 h-1.5 rounded-full ${
            hasToken ? 'bg-emerald-400/80' : 'bg-[var(--text-muted)]'
          }`}
        />
        <RailTooltip label={hasToken ? 'Token set' : 'No token'} />
      </button>
    </nav>
  );
}
