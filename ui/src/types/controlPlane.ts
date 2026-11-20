// Locked contracts for the upcoming Go Control Plane backend (/api/v1/*).

export type SourceType = 'tg' | 'hh' | 'reddit';

export interface Source {
  id: string;
  type: SourceType;
  target: string;
  enabled: boolean;
  created_at: string;
}

export interface CreateSourcePayload {
  type: SourceType;
  target: string;
}

export interface UpdateSourcePayload {
  enabled?: boolean;
}

export interface EngineSettings {
  system_prompt: string;
  min_score: number;
}
