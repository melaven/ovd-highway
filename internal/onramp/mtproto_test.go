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

func TestNormalizeTargetName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain username", "channel", "channel"},
		{"at prefix", "@channel", "channel"},
		{"t.me URL", "t.me/channel", "channel"},
		{"https t.me URL", "https://t.me/channel", "channel"},
		{"www.t.me URL", "www.t.me/channel", "channel"},
		{"https www.t.me URL", "https://www.t.me/channel", "channel"},
		{"telegram.me URL", "telegram.me/channel", "channel"},
		{"https telegram.me URL", "https://telegram.me/channel", "channel"},
		{"www.telegram.me URL", "www.telegram.me/channel", "channel"},
		{"t.me/s preview", "t.me/s/channel", "channel"},
		{"https t.me/s preview", "https://t.me/s/channel", "channel"},
		{"telegram.me/s preview", "telegram.me/s/channel", "channel"},
		{"query params", "t.me/channel?start=abc123", "channel"},
		{"https query params", "https://t.me/channel?start=abc123", "channel"},
		{"trailing slash", "t.me/channel/", "channel"},
		{"uppercase URL", "HTTPS://T.ME/Channel", "channel"},
		{"markdown link", "[chan](https://t.me/channel)", "channel"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeTargetName(tt.input); got != tt.want {
				t.Errorf("normalizeTargetName(%q) = %q, want %q", tt.input, got, tt.want)
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
