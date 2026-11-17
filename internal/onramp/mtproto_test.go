package onramp

import "testing"

func TestSanitizeChannelInput(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"Clean URL", "https://t.me/frilancetest", "https://t.me/frilancetest"},
		{"Markdown Link", "[Channel](https://t.me/frilancetest)", "https://t.me/frilancetest"},
		{"With Newlines", " \n t.me/frilancetest \t ", "t.me/frilancetest"},
		{"Invisible Chars", "t.me/frilancetest\u0000", "t.me/frilancetest"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SanitizeChannelInput(tt.input); got != tt.expected {
				t.Errorf("SanitizeChannelInput() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestParseTargetChannelsSanitizesMarkdownLinks(t *testing.T) {
	got := parseTargetChannels("[Channel](https://t.me/frilancetest), t.me/another\u0000")
	want := []string{"frilancetest", "another"}
	if len(got) != len(want) {
		t.Fatalf("parseTargetChannels() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("parseTargetChannels()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
