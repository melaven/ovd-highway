package main

import "testing"

func TestLLMConcurrency(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  int
	}{
		{"empty uses default", "", defaultQualifierWorkers},
		{"valid value", "5", 5},
		{"minimum clamp", "1", 1},
		{"above ceiling clamps", "50", maxQualifierWorkers},
		{"non-numeric uses default", "abc", defaultQualifierWorkers},
		{"zero uses default", "0", defaultQualifierWorkers},
		{"negative uses default", "-2", defaultQualifierWorkers},
		{"whitespace trimmed", " 7 ", 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("LLM_CONCURRENCY", tt.value)
			if got := llmConcurrency(); got != tt.want {
				t.Errorf("llmConcurrency() = %d, want %d", got, tt.want)
			}
		})
	}
}
