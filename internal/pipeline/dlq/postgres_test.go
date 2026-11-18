package dlq

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestNormalizeDedupeKey(t *testing.T) {
	if got := normalizeDedupeKey(""); got != nil {
		t.Errorf("empty key = %v, want nil (NULL)", got)
	}
	short := "dedup:reddit:abc"
	if got := normalizeDedupeKey(short); got != short {
		t.Errorf("short key = %v, want passthrough %q", got, short)
	}
	long := "dedup:" + strings.Repeat("x", 300)
	got, ok := normalizeDedupeKey(long).(string)
	if !ok {
		t.Fatalf("long key type = %T, want string", normalizeDedupeKey(long))
	}
	sum := sha256.Sum256([]byte(long))
	want := hex.EncodeToString(sum[:])
	if got != want {
		t.Errorf("long key = %q, want sha256 %q", got, want)
	}
	if len(got) > 255 {
		t.Errorf("hashed key len = %d, exceeds column", len(got))
	}
}