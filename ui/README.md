# 🎯 OVD // HIGHWAY Telemetry Console

**Professional-grade glassmorphism telemetry UI** for multi-protocol opportunity ingestion engine.

## Features

✨ **Real-time WebSocket Stream** (`ws://localhost:8080/ws`)  
🎨 **Glassmorphism Design** (frosted glass cards, ambient gradient)  
📊 **4-Column Metrics** (Ingested, Qualified, Avg Score, Active Streams)  
🔍 **Interactive Filters** (Score 6+/8+, Source, Live Search)  
📋 **Copyable Draft Proposals** (One-click pitch copy)  
🎯 **Score-Based Coloring** (High/Medium/Low tier glassmorphism)  
🔄 **Demo Mode Fallback** (Sample data when backend offline)

## Quick Start

```bash
npm install
npm run dev    # → http://localhost:5173
```

## Architecture

```
src/
├── types.ts              # TypeScript contracts (Go backend mirror)
├── utils/formatters.ts   # Time/score/source utilities
├── components/
│   ├── Header.tsx        # Status badge + demo toggle
│   ├── MetricsRow.tsx    # 4-column glass metrics
│   ├── FilterBar.tsx     # Interactive filters + search
│   └── LeadCard.tsx      # High-density signal cards
└── App.tsx               # WebSocket orchestrator
```

## Tech Stack

- React 19.3 + TypeScript (strict mode)
- Framer Motion (animations)
- Lucide React (icons)
- TailwindCSS (glassmorphism)
- Vite 8 (build)

## WebSocket Contract

```typescript
interface QualifiedLead {
  signal: {
    id: string;
    source: string;
    url: string;
    text: string;
    received_at: string;
  };
  qualification: {
    is_relevant: boolean;
    score: number;
    estimated_budget: string;
    pain_points: string;
    draft_proposal: string;
    tech_stack?: string[];
  };
  qualified_at: string;
}
```

## Color System

**Glassmorphism:**
- Cards: `bg-[#EEFIF6]/10 backdrop-blur-2xl border-[#EEFIF6]/20`
- Gradient: `from-[#8D99AE] via-[#23253A] to-[#121420]`

**Score Tiers:**
- High (8-10): Emerald glass (`bg-emerald-500/10 text-emerald-300`)
- Medium (5-7): Amber glass (`bg-amber-500/10 text-amber-200`)
- Low (1-4): Muted slate (`bg-white/5 text-slate-400`)

---

**OVD // HIGHWAY** — Zero-Blocking Multi-Protocol Ingestion Engine
