package lane

import (
	"testing"

	"ovd/internal/domain"
)

func TestDedupKeyDeterministic(t *testing.T) {
	a := domain.RawSignal{Source: "reddit", Text: "Need a  Go\n developer for API work"}
	b := domain.RawSignal{Source: "reddit", Text: "need a go developer for api work"}
	if DedupKey(a) != DedupKey(b) {
		t.Error("case/whitespace variants produced different keys")
	}
}

func TestDedupKeyDiffersBySourceAndText(t *testing.T) {
	base := domain.RawSignal{Source: "reddit", Text: "same text here"}
	otherSource := base
	otherSource.Source = "hh"
	otherText := base
	otherText.Text = "different text here"

	if DedupKey(base) == DedupKey(otherSource) {
		t.Error("different sources produced same key")
	}
	if DedupKey(base) == DedupKey(otherText) {
		t.Error("different texts produced same key")
	}
}