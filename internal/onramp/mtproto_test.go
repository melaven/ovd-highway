package onramp

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/gotd/td/tg"

	"ovd/internal/domain"
)

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

func newTestMTProtoSource(t *testing.T) *MTProtoSource {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := NewMTProtoSource(1, "hash", "+10000000000", "session.test.json", log)
	m.allowedMu.Lock()
	m.allowed[12345] = "testchan"
	m.allowedMu.Unlock()
	return m
}

func albumMsg(text string, gid int64) *tg.Message {
	msg := &tg.Message{
		ID:      1,
		PeerID:  &tg.PeerChannel{ChannelID: 12345},
		Message: text,
	}
	if gid != 0 {
		msg.SetGroupedID(gid)
	}
	return msg
}

func TestHandleMessageDropsEmptyMediaPost(t *testing.T) {
	m := newTestMTProtoSource(t)
	out := make(chan domain.RawSignal, 1)

	m.handleMessage(context.Background(), albumMsg("", 0), out)
	m.handleMessage(context.Background(), albumMsg("   ", 0), out)

	if len(out) != 0 {
		t.Fatalf("empty media posts emitted %d signals, want 0", len(out))
	}
}

func TestHandleMessageAlbumDedup(t *testing.T) {
	m := newTestMTProtoSource(t)
	out := make(chan domain.RawSignal, 4)
	ctx := context.Background()

	// First album frame has no caption: dropped AND must not reserve gid.
	m.handleMessage(ctx, albumMsg("", 7), out)
	// Second frame carries the caption: emitted.
	m.handleMessage(ctx, albumMsg("real caption", 7), out)
	// Third frame in the same album: duplicate text/frame, dropped.
	m.handleMessage(ctx, albumMsg("another caption", 7), out)
	// A different album id is independent.
	m.handleMessage(ctx, albumMsg("other album", 9), out)

	if len(out) != 2 {
		t.Fatalf("album dedup emitted %d signals, want 2", len(out))
	}
	first := <-out
	second := <-out
	if first.Text != "real caption" || second.Text != "other album" {
		t.Fatalf("unexpected signals: %q, %q", first.Text, second.Text)
	}
}

func TestGroupedSeenCacheTTL(t *testing.T) {
	c := newGroupedSeenCache(50*time.Millisecond, 8)
	now := time.Now()

	if c.checkAndRecord(1, now) {
		t.Fatal("first record should return false")
	}
	if !c.checkAndRecord(1, now.Add(10*time.Millisecond)) {
		t.Fatal("second record within TTL should return true")
	}
	if c.checkAndRecord(1, now.Add(100*time.Millisecond)) {
		t.Fatal("record after TTL expiry should return false")
	}
}

func TestGroupedSeenCacheBounded(t *testing.T) {
	c := newGroupedSeenCache(time.Hour, 4)
	now := time.Now()
	for i := int64(0); i < 10; i++ {
		c.checkAndRecord(i, now)
	}
	c.mu.Lock()
	n := len(c.items)
	c.mu.Unlock()
	if n != 4 {
		t.Fatalf("cache size = %d, want bounded at 4", n)
	}
}
