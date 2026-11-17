package domain

import "time"

type RawSignal struct {
	ID         string    `json:"id"`
	Source     string    `json:"source"`
	URL        string    `json:"url"`
	Text       string    `json:"text"`
	Budget     string    `json:"budget,omitempty"`
	ReceivedAt time.Time `json:"received_at"`
	JobType    string    `json:"job_type"` // "freelance" | "vacancy"
}

type Qualification struct {
	IsRelevant      bool     `json:"is_relevant"`
	Score           int      `json:"score"`
	EstimatedBudget string   `json:"estimated_budget"`
	PainPoints      string   `json:"pain_points"`
	DraftProposal   string   `json:"draft_proposal"`
	TechStack       []string `json:"tech_stack"`
	DetectedJobType string   `json:"detected_job_type"` // "freelance" | "vacancy" | "other"
}

type QualifiedLead struct {
	Signal        RawSignal     `json:"signal"`
	Qualification Qualification `json:"qualification"`
	QualifiedAt   time.Time     `json:"qualified_at"`
	JobType       string        `json:"job_type"` // "freelance" | "vacancy", mirrored from Signal.JobType
}
