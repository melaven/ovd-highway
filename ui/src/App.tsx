import { useEffect, useMemo, useRef, useState } from 'react';
import {
  Check,
  Copy,
  Loader2,
  RotateCcw,
  Search,
  X,
} from 'lucide-react';
import { extractOriginName, formatRelativeTime, getSourceFromSignal } from './utils/formatters';

const WS_URL = 'ws://localhost:8080/ws';

type ConnState = 'connecting' | 'live' | 'fallback_error';

export interface QualifiedLead {
  id: string;
  source: 'mtproto' | 'habr' | 'web';
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
}

const SEED_LEADS: QualifiedLead[] = [
  {
    id: 'sig-901',
    source: 'mtproto',
    origin_name: '@golang_jobs_cis',
    title: 'Разработка высоконагруженного шлюза агрегации вебхуков (Go + Redis)',
    body: 'Ищем системного бэкенд-инженера для переноса текущего пайплайна с Python на Go. Нагрузка около 4k RPS в пике, нужна строгая идемпотентность через Redis и прозрачные метрики задержек.',
    author_url: 'https://t.me',
    post_url: 'https://t.me',
    budget: '$3,200',
    captured_at: '2m ago',
    score: 9,
    is_relevant: true,
    tech_stack: ['Go', 'Redis', 'PostgreSQL', 'Docker'],
    pain_summary: 'Миграция узкого места с Python на Go с гарантией идемпотентности при 4k RPS.',
    draft_proposal: 'Привет! Недавно собирал аналогичный неблокирующий конвейер на Go (errgroup + буферизованные каналы) с дедупликацией в Redis за доли миллисекунды. Готов показать архитектуру в коде и подключиться к миграции на этой неделе.',
    job_type: 'freelance',
  },
  {
    id: 'sig-902',
    source: 'habr',
    origin_name: 'habr.freelance',
    title: 'Full-Stack панель телеметрии для B2B аналитического сервиса',
    body: 'Есть готовое ядро сбора данных, требуется спроектировать WebSocket-стриминг на Go и чистый дашборд на React + TypeScript без тормозов на больших списках событий.',
    author_url: 'https://habr.com',
    post_url: 'https://habr.com',
    budget: '180 000 ₽',
    captured_at: '11m ago',
    score: 8,
    is_relevant: true,
    tech_stack: ['Go', 'WebSockets', 'React', 'TypeScript'],
    pain_summary: 'Нужна связка Go WebSocket-хаба и реактивного интерфейса реального времени.',
    draft_proposal: 'Добрый день. Специализируюсь ровно на стыке системного бэкенда (Go/FastAPI) и реактивных консолей на TypeScript. Могу развернуть прототип со стримингом и кольцевым буфером истории за пару дней — давайте обсудим контракты API.',
    job_type: 'freelance',
  },
  {
    id: 'sig-903',
    source: 'mtproto',
    origin_name: '@dev_orders',
    title: 'Интеграция кастомного парсера Telegram-каналов с CRM и скорингом лидов',
    body: 'Нужно мониторить 40+ тематических чатов напрямую через протокол Telegram (обычный бот не подходит), отсеивать дубли и пересылать целевые заявки менеджерам.',
    author_url: 'https://t.me',
    post_url: 'https://t.me',
    budget: '95 000 ₽',
    captured_at: '24m ago',
    score: 9,
    is_relevant: true,
    tech_stack: ['MTProto', 'Go', 'LLM API', 'PostgreSQL'],
    pain_summary: 'Прямой перехват трафика чатов по MTProto с фильтрацией кросс-постинга.',
    draft_proposal: 'Приветствую! У меня уже реализован промышленный движок на Go + gotd/td (MTProto) с SHA-256 дедупликацией и автоматической квалификацией через LLM. Адаптирую решение под ваши 40 чатов и свяжу с CRM в кратчайшие сроки.',
    job_type: 'freelance',
  },
  {
    id: 'sig-904',
    source: 'web',
    origin_name: 'direct.feed',
    title: 'Оптимизация Multi-tenant схемы PostgreSQL и настройка RLS политик',
    body: 'В растущем SaaS-проекте начались просадки по сложным выборкам JSONB и есть подозрения на некорректную изоляцию ролей в Supabase.',
    author_url: 'https://github.com',
    post_url: 'https://github.com',
    budget: '$1,100',
    captured_at: '47m ago',
    score: 7,
    is_relevant: true,
    tech_stack: ['PostgreSQL', 'Supabase RLS', 'JSONB'],
    pain_summary: 'Аудит изоляции тенантов RLS и ускорение тяжелых JSONB-запросов.',
    draft_proposal: 'Здравствуйте. Плотно работал с Multi-tenant архитектурой на PostgreSQL + Supabase RLS и оптимизацией индексов по JSONB в собственном B2B-продукте. Готов провести ревью схем и планов запросов.',
    job_type: 'vacancy',
  }
];

// Raw payload shape streamed by api/ws.go: Go domain.QualifiedLead
interface RawQualifiedLead {
  signal: {
    id?: string;
    source?: string;
    url?: string;
    text?: string;
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
}

function isRawLead(msg: unknown): msg is RawQualifiedLead {
  if (!msg || typeof msg !== 'object') return false;
  const m = msg as Record<string, unknown>;
  return (
    typeof m.signal === 'object' && m.signal !== null &&
    typeof m.qualification === 'object' && m.qualification !== null
  );
}

function isFlatLead(msg: unknown): msg is QualifiedLead {
  if (!msg || typeof msg !== 'object') return false;
  const m = msg as Record<string, unknown>;
  return typeof m.title === 'string' && typeof m.score === 'number';
}

function normalizeRawLead(raw: RawQualifiedLead): QualifiedLead {
  const sig = raw.signal ?? {};
  const qual = raw.qualification ?? {};
  const source = getSourceFromSignal(String(sig.source ?? ''));
  const url = String(sig.url ?? '');
  const text = String(sig.text ?? '');
  const title = text.length > 140 ? `${text.slice(0, 137).trimEnd()}…` : text;

  return {
    id: String(sig.id ?? `${url}-${raw.qualified_at ?? Date.now()}`),
    source: source.toLowerCase() as QualifiedLead['source'],
    origin_name: extractOriginName(String(sig.source ?? ''), url),
    title,
    body: text,
    author_url: url,
    post_url: url,
    budget: String(qual.estimated_budget ?? ''),
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
  };
}

function normalizeIncoming(msg: unknown): QualifiedLead | null {
  if (isFlatLead(msg)) return msg;
  if (isRawLead(msg)) return normalizeRawLead(msg);
  return null;
}

const SCORE_FILTERS = [
  { label: 'All', val: 0 },
  { label: '7+', val: 7 },
  { label: '8+', val: 8 },
];

const SOURCE_FILTERS = [
  { label: 'All', val: 'all' },
  { label: 'MTProto', val: 'mtproto' },
  { label: 'Habr', val: 'habr' },
  { label: 'Web', val: 'web' },
];

function scoreTextClass(score: number): string {
  if (score >= 8) return 'text-emerald-300';
  if (score >= 6) return 'text-amber-200';
  return 'text-slate-500';
}

export default function App() {
  const [leads, setLeads] = useState<QualifiedLead[]>(SEED_LEADS);
  const [selectedId, setSelectedId] = useState<string>(SEED_LEADS[0].id);
  const [conn, setConn] = useState<ConnState>('connecting');
  const [wsNonce, setWsNonce] = useState(0);
  const [minScore, setMinScore] = useState<number>(0);
  const [sourceFilter, setSourceFilter] = useState<string>('all');
  const [query, setQuery] = useState<string>('');
  const [copyState, setCopyState] = useState<Record<string, 'copied' | 'error'>>({});
  const [mobileSheet, setMobileSheet] = useState(false);
  const searchRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        searchRef.current?.focus();
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  useEffect(() => {
    let ws: WebSocket | null = null;
    let timer: number;

    const connect = () => {
      try {
        ws = new WebSocket(WS_URL);

        ws.onopen = () => setConn('live');
        ws.onclose = () => {
          setConn('fallback_error');
          timer = window.setTimeout(connect, 4000);
        };
        ws.onmessage = (ev) => {
          try {
            const msg = JSON.parse(ev.data);
            const pushLead = (lead: QualifiedLead | null) => {
              if (!lead) return;
              setLeads((prev) => [lead, ...prev.filter((l) => l.id !== lead.id)].slice(0, 100));
            };

            if (msg.type === 'init' && Array.isArray(msg.payload) && msg.payload.length > 0) {
              setLeads(msg.payload);
              setSelectedId(msg.payload[0].id);
            } else if (msg.type === 'lead' && msg.payload) {
              pushLead(normalizeIncoming(msg.payload));
            } else {
              // Raw domain.QualifiedLead from api/ws.go Broadcast().
              pushLead(normalizeIncoming(msg));
            }
          } catch {
            // ignore malformed frame
          }
        };
      } catch {
        setConn('fallback_error');
      }
    };

    setConn('connecting');
    connect();
    return () => {
      clearTimeout(timer);
      ws?.close();
    };
  }, [wsNonce]);

  const retryConnection = () => {
    setConn('connecting');
    setWsNonce((n) => n + 1);
  };

  const filtered = useMemo(() => {
    return leads.filter((item) => {
      if (item.score < minScore) return false;
      if (sourceFilter !== 'all' && item.source !== sourceFilter) return false;
      if (query.trim()) {
        const q = query.toLowerCase();
        const hay = `${item.title} ${item.body} ${item.origin_name} ${item.tech_stack.join(' ')}`.toLowerCase();
        if (!hay.includes(q)) return false;
      }
      return true;
    });
  }, [leads, minScore, sourceFilter, query]);

  const activeLead = useMemo(
    () => filtered.find((l) => l.id === selectedId) || filtered[0] || null,
    [filtered, selectedId]
  );

  const avgScore = useMemo(() => {
    if (!leads.length) return '0.0';
    const sum = leads.reduce((acc, l) => acc + l.score, 0);
    return (sum / leads.length).toFixed(1);
  }, [leads]);

  const resetFilters = () => {
    setMinScore(0);
    setSourceFilter('all');
    setQuery('');
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
      () => setCopyState((s) => ({ ...s, [id]: 'error' }))
    );
  };

  return (
    <div className="h-screen w-screen overflow-hidden flex flex-col bg-[#0d1117] text-slate-200 select-none">
      {/* Top system bar */}
      <header className="h-11 px-4 border-b border-white/[0.08] flex items-center justify-between shrink-0 bg-white/[0.02]">
        <div className="flex items-center gap-3 min-w-0">
          <span className="font-mono text-xs font-semibold tracking-wider text-white shrink-0">
            OVD // HIGHWAY
          </span>
          <span className="h-3 w-px bg-white/10 shrink-0" />
          <div className="flex items-center gap-3 font-mono text-xs text-slate-400 truncate">
            <span>Ingested: {leads.length}</span>
            <span>Avg: {avgScore}</span>
            <span>Sources: 3</span>
          </div>
        </div>

        <div className="flex items-center gap-2 font-mono text-xs shrink-0">
          {conn === 'live' ? (
            <span className="text-slate-400">ws://:8080 connected</span>
          ) : (
            <>
              <span className="text-slate-600">
                {conn === 'connecting' ? 'connecting…' : 'offline (sample feed)'}
              </span>
              {conn === 'connecting' ? (
                <Loader2 className="w-4 h-4 text-slate-500 animate-spin" strokeWidth={1.75} />
              ) : (
                <button
                  onClick={retryConnection}
                  className="text-slate-400 hover:text-white transition-colors"
                >
                  [retry]
                </button>
              )}
            </>
          )}
        </div>
      </header>

      {/* Filter & search toolbar */}
      <div className="h-10 px-4 border-b border-white/[0.08] flex items-center justify-between shrink-0 bg-black/20 font-mono text-xs">
        <div className="flex items-center gap-1 min-w-0">
          {SCORE_FILTERS.map((opt) => (
            <button
              key={opt.label}
              onClick={() => setMinScore(opt.val)}
              className={`h-10 px-2.5 flex items-center border-b-2 transition-colors ${
                minScore === opt.val
                  ? 'border-emerald-400 text-white'
                  : 'border-transparent text-slate-500 hover:text-slate-300'
              }`}
            >
              {opt.label}
            </button>
          ))}
          <span className="h-4 w-px bg-white/10 mx-2" />
          {SOURCE_FILTERS.map((opt) => (
            <button
              key={opt.label}
              onClick={() => setSourceFilter(opt.val)}
              className={`h-10 px-2.5 flex items-center border-b-2 transition-colors ${
                sourceFilter === opt.val
                  ? 'border-emerald-400 text-white'
                  : 'border-transparent text-slate-500 hover:text-slate-300'
              }`}
            >
              {opt.label}
            </button>
          ))}
        </div>

        <div className="relative flex items-center shrink-0">
          <Search className="w-4 h-4 text-slate-600 absolute left-2 pointer-events-none" strokeWidth={1.75} />
          <input
            ref={searchRef}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Filter stream..."
            className="w-44 sm:w-56 bg-transparent border border-white/[0.08] rounded pl-7 pr-8 py-1 text-xs text-slate-200 placeholder:text-slate-600 focus:outline-none focus:border-white/25 font-mono"
          />
          <kbd className="absolute right-2 font-mono text-[10px] text-slate-600 pointer-events-none">
            ⌘K
          </kbd>
        </div>
      </div>

      {/* Split workspace */}
      <main className="flex-1 grid grid-cols-1 lg:grid-cols-12 min-h-0 overflow-hidden">
        {/* Left feed */}
        <div className="lg:col-span-7 border-b lg:border-b-0 lg:border-r border-white/[0.08] overflow-y-auto divide-y divide-white/[0.06] min-h-0">
          {filtered.length === 0 ? (
            <div className="h-full flex flex-col items-center justify-center gap-3 px-4">
              <p className="font-mono text-xs text-slate-500">0 signals match current filters</p>
              <button
                onClick={resetFilters}
                className="inline-flex items-center gap-2 px-3 py-1.5 rounded-md bg-white/[0.08] hover:bg-white/[0.14] border border-white/10 font-mono text-xs text-slate-200 transition"
              >
                <RotateCcw className="w-4 h-4" strokeWidth={1.75} />
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
                  className={`px-4 py-3 cursor-pointer transition-colors ${
                    isSelected
                      ? 'bg-white/[0.06] shadow-[inset_2px_0_0_0_#34d399]'
                      : 'hover:bg-white/[0.03]'
                  }`}
                >
                  <div className="flex items-center gap-3">
                    <span className={`font-mono text-xs w-9 shrink-0 ${scoreTextClass(item.score)}`}>
                      {item.score}/10
                    </span>
                    <h2 className="flex-1 min-w-0 text-sm font-semibold text-white truncate">
                      {item.title}
                    </h2>
                    {item.budget && (
                      <span className="font-mono text-xs text-slate-400 shrink-0">
                        {item.budget}
                      </span>
                    )}
                  </div>
                  <div className="mt-1 flex items-center gap-2 min-w-0">
                    {item.job_type && (
                      <span
                        className={`shrink-0 px-1.5 py-0.5 rounded border font-mono text-[10px] uppercase ${
                          item.job_type === 'vacancy'
                            ? 'bg-sky-400/[0.08] border-sky-400/20 text-sky-300'
                            : 'bg-emerald-400/[0.08] border-emerald-400/20 text-emerald-300'
                        }`}
                      >
                        {item.job_type === 'vacancy' ? 'vac' : 'frl'}
                      </span>
                    )}
                    <span className="font-mono text-xs text-slate-500 truncate">
                      {item.source} · {item.origin_name} · {item.captured_at}
                    </span>
                    {item.tech_stack.length > 0 && (
                      <span className="flex items-center gap-1 shrink-0">
                        {item.tech_stack.slice(0, 4).map((tech) => (
                          <span
                            key={tech}
                            className="px-1.5 py-0.5 rounded bg-white/[0.05] border border-white/[0.07] font-mono text-[10px] text-slate-400"
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
        <div className="hidden lg:block lg:col-span-5 overflow-y-auto p-6 bg-black/15 select-text min-h-0">
          {activeLead && <InspectorContent lead={activeLead} copyState={copyState[activeLead.id]} onCopy={copyPitch} />}
        </div>
      </main>

      {/* Mobile slide-over inspector */}
      {mobileSheet && activeLead && (
        <div className="lg:hidden fixed inset-0 z-50">
          <div className="absolute inset-0 bg-black/50" onClick={() => setMobileSheet(false)} />
          <div className="absolute inset-x-0 bottom-0 max-h-[85vh] overflow-y-auto rounded-t-lg bg-[#0d1117] border-t border-white/10 p-4">
            <div className="flex items-center justify-between mb-4">
              <span className="font-mono text-xs uppercase tracking-wider text-slate-500">
                Signal Inspector
              </span>
              <button
                onClick={() => setMobileSheet(false)}
                className="p-1.5 rounded-md text-slate-400 hover:text-white hover:bg-white/10 transition"
                aria-label="Close inspector"
              >
                <X className="w-4 h-4" strokeWidth={1.75} />
              </button>
            </div>
            <InspectorContent lead={activeLead} copyState={copyState[activeLead.id]} onCopy={copyPitch} />
          </div>
        </div>
      )}
    </div>
  );
}

function InspectorContent({
  lead,
  copyState,
  onCopy,
}: {
  lead: QualifiedLead;
  copyState?: 'copied' | 'error';
  onCopy: (id: string, text: string) => void;
}) {
  return (
    <div className="space-y-6">
      <div className="space-y-2">
        <div className="flex items-center justify-between gap-3">
          <span className="font-mono text-xs uppercase tracking-wider text-slate-500">
            {lead.origin_name} · {lead.captured_at}
          </span>
          <a
            href={lead.post_url || lead.author_url}
            target="_blank"
            rel="noreferrer"
            className="shrink-0 inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md bg-white/[0.07] hover:bg-white/[0.12] border border-white/10 font-mono text-xs text-slate-200 transition"
          >
            Source ↗
          </a>
        </div>
        <h2 className="text-sm font-semibold text-white leading-snug">{lead.title}</h2>
        <div className="flex items-center gap-3 font-mono text-xs text-slate-500">
          <span className={scoreTextClass(lead.score)}>{lead.score}/10</span>
          {lead.budget && <span>{lead.budget}</span>}
        </div>
      </div>

      <section className="border-t border-white/[0.08] pt-5 space-y-2">
        <span className="font-mono text-xs uppercase tracking-wider text-slate-500 block">
          Captured Payload
        </span>
        <p className="text-sm leading-relaxed text-slate-300 whitespace-pre-wrap">
          {lead.body}
        </p>
        {lead.tech_stack.length > 0 && (
          <div className="flex flex-wrap gap-1.5 pt-1">
            {lead.tech_stack.map((tech) => (
              <span
                key={tech}
                className="px-1.5 py-0.5 rounded bg-white/[0.05] border border-white/[0.07] font-mono text-[10px] text-slate-400"
              >
                {tech}
              </span>
            ))}
          </div>
        )}
      </section>

      <section className="border-t border-white/[0.08] pt-5 space-y-3">
        <div className="flex items-center justify-between gap-3">
          <span className="font-mono text-xs uppercase tracking-wider text-slate-500">
            Draft Response Pitch
          </span>
          <button
            onClick={() => onCopy(lead.id, lead.draft_proposal)}
            className={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md border font-mono text-xs transition ${
              copyState === 'copied'
                ? 'bg-emerald-400/15 border-emerald-400/30 text-emerald-200'
                : copyState === 'error'
                  ? 'bg-white/[0.05] border-white/15 text-slate-300'
                  : 'bg-white/[0.08] hover:bg-white/[0.14] border-white/10 text-slate-200'
            }`}
          >
            {copyState === 'copied' ? (
              <>
                <Check className="w-4 h-4" strokeWidth={1.75} />
                Copied to Clipboard
              </>
            ) : copyState === 'error' ? (
              'Clipboard Blocked — Select Manually'
            ) : (
              <>
                <Copy className="w-4 h-4" strokeWidth={1.75} />
                Copy Pitch Text
              </>
            )}
          </button>
        </div>
        <p className="text-sm leading-relaxed text-slate-300 whitespace-pre-wrap">
          {lead.draft_proposal}
        </p>
      </section>
    </div>
  );
}
