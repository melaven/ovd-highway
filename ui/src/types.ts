// TypeScript types mirroring Go backend JSON contract

export interface RawSignal {
  id: string;
  source: string;
  url: string;
  text: string;
  received_at: string;
}

export interface Qualification {
  is_relevant: boolean;
  score: number;
  estimated_budget: string;
  pain_points: string;
  draft_proposal: string;
  tech_stack?: string[];
}

export interface QualifiedLead {
  signal: RawSignal;
  qualification: Qualification;
  qualified_at: string;
}

export interface TelemetryStats {
  ingested_signals: number;
  qualified_leads: number;
  avg_score: number;
  active_onramps: string[];
}

export type ScoreTier = 'high' | 'medium' | 'low';
export type SourceFilter = 'ALL' | 'MTPROTO' | 'HABR' | 'WEB';
export type ScoreFilter = 'ALL' | '6+' | '8+';

export interface FilterState {
  scoreFilter: ScoreFilter;
  sourceFilter: SourceFilter;
  searchQuery: string;
}
