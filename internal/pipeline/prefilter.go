package pipeline

import (
	"fmt"
	"strings"
)

// defaultRejectKeywords are spam / low-value markers checked
// case-insensitively against the signal text before it reaches the LLM.
var defaultRejectKeywords = []string{
	"за отзыв",
	"копейки",
	"без опыта",
	"рулетка",
	"скам",
	"казино",
}

// PreFilter drops low-value signals before they consume LLM tokens.
// It is stateless and safe for concurrent use.
type PreFilter struct {
	keywords []string
}

// NewPreFilter returns a PreFilter with the default keyword set.
// Pass custom keywords to override it entirely.
func NewPreFilter(keywords ...string) *PreFilter {
	if len(keywords) == 0 {
		keywords = defaultRejectKeywords
	}
	lowered := make([]string, 0, len(keywords))
	for _, kw := range keywords {
		kw = strings.ToLower(strings.TrimSpace(kw))
		if kw != "" {
			lowered = append(lowered, kw)
		}
	}
	return &PreFilter{keywords: lowered}
}

// Evaluate returns (true, "") when the text passes, or
// (false, reason) when it matches a reject keyword.
func (f *PreFilter) Evaluate(text string) (bool, string) {
	if f == nil || len(f.keywords) == 0 {
		return true, ""
	}
	lower := strings.ToLower(text)
	for _, kw := range f.keywords {
		if strings.Contains(lower, kw) {
			return false, fmt.Sprintf("matched reject keyword %q", kw)
		}
	}
	return true, ""
}
