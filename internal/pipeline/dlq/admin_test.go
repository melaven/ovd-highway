package dlq

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func newMockStore(t *testing.T) (*PostgresRetryStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := NewPostgresRetryStore(db)
	if err != nil {
		t.Fatalf("NewPostgresRetryStore: %v", err)
	}
	return store, mock
}

func TestListReturnsItems(t *testing.T) {
	store, mock := newMockStore(t)
	now := time.Now()

	mock.ExpectQuery(regexp.QuoteMeta(`FROM dead_letter_signals`)).
		WithArgs(50, dlqPreviewLen).
		WillReturnRows(sqlmock.NewRows([]string{"id", "source", "error_reason", "retry_count", "created_at", "preview"}).
			AddRow("a", "telegram_mtproto", "llm http status 503", 2, now, `{"id":"s1"}`).
			AddRow("b", "reddit", "timeout", 0, now.Add(-time.Minute), ""))

	items, err := store.List(context.Background(), 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}
	if items[0].ID != "a" || items[0].Source != "telegram_mtproto" || items[0].Attempts != 2 {
		t.Fatalf("unexpected item: %+v", items[0])
	}
	if items[1].PayloadPreview != "" {
		t.Fatalf("preview = %q, want empty", items[1].PayloadPreview)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestListClampsLimit(t *testing.T) {
	store, mock := newMockStore(t)

	mock.ExpectQuery(regexp.QuoteMeta(`FROM dead_letter_signals`)).
		WithArgs(dlqMaxListLimit, dlqPreviewLen).
		WillReturnRows(sqlmock.NewRows([]string{"id", "source", "error_reason", "retry_count", "created_at", "preview"}))

	items, err := store.List(context.Background(), 999)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if items == nil {
		t.Fatal("items = nil, want empty slice")
	}
	if len(items) != 0 {
		t.Fatalf("len(items) = %d, want 0", len(items))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestListEmptyResult(t *testing.T) {
	store, mock := newMockStore(t)

	mock.ExpectQuery(regexp.QuoteMeta(`FROM dead_letter_signals`)).
		WithArgs(10, dlqPreviewLen).
		WillReturnRows(sqlmock.NewRows([]string{"id", "source", "error_reason", "retry_count", "created_at", "preview"}))

	items, err := store.List(context.Background(), 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("len(items) = %d, want 0", len(items))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestCount(t *testing.T) {
	store, mock := newMockStore(t)

	mock.ExpectQuery(`SELECT count\(\*\) FROM dead_letter_signals`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(42))

	n, err := store.Count(context.Background())
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 42 {
		t.Fatalf("Count = %d, want 42", n)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRetryAll(t *testing.T) {
	store, mock := newMockStore(t)

	mock.ExpectExec(`UPDATE dead_letter_signals`).
		WillReturnResult(sqlmock.NewResult(0, 7))

	n, err := store.RetryAll(context.Background())
	if err != nil {
		t.Fatalf("RetryAll: %v", err)
	}
	if n != 7 {
		t.Fatalf("RetryAll = %d, want 7", n)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestDLQListLimit(t *testing.T) {
	tests := []struct {
		in   int
		want int
	}{
		{0, 50}, {-5, 50}, {1, 1}, {50, 50}, {100, 100}, {101, 100}, {9999, 100},
	}
	for _, tt := range tests {
		if got := dlqListLimit(tt.in); got != tt.want {
			t.Errorf("dlqListLimit(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}
