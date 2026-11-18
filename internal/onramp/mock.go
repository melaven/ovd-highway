package onramp

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"ovd/internal/domain"
)

// MockSource emits canned RawSignals tagged with JobType for local
// pipeline testing. Enable with MOCK_ONRAMP=1.
type MockSource struct {
	log      *slog.Logger
	interval time.Duration
}

func NewMockSource(log *slog.Logger) *MockSource {
	return &MockSource{
		log:      log.With("component", "mock_onramp"),
		interval: 20 * time.Second,
	}
}

func (m *MockSource) Name() string {
	return "mock"
}

func (m *MockSource) Start(ctx context.Context, out chan<- domain.RawSignal) error {
	m.log.Info("mock onramp started")

	signals := []domain.RawSignal{
		{
			ID:         uuid.NewString(),
			Source:     "mock_freelance",
			URL:        "https://example.com/gigs/1",
			Text:       "Нужен Senior Go-разработчик на проект: миграция пайплайна обработки заказов на Go + PostgreSQL, дедупликация, REST API для CRM. Удаленно, проектная занятость 2-3 месяца.",
			ReceivedAt: time.Now().UTC(),
			JobType:    "freelance",
		},
		{
			ID:         uuid.NewString(),
			Source:     "mock_vacancy",
			URL:        "https://example.com/jobs/2",
			Text:       "Вакансия: Middle/Senior Go Developer, полная занятость, удаленно. Стек: Go, PostgreSQL, Docker. Гибкий график, оформление по ТК.",
			ReceivedAt: time.Now().UTC(),
			JobType:    "vacancy",
		},
		{
			ID:         uuid.NewString(),
			Source:     "mock_freelance",
			URL:        "https://example.com/gigs/3",
			Text:       "Сделать парсер Telegram-каналов на Go с MTProto, фильтрацией спама и выгрузкой в Google Sheets. Разовый проект, бюджет обсуждается.",
			ReceivedAt: time.Now().UTC(),
			JobType:    "freelance",
		},
	}

	for i, sig := range signals {
		if i > 0 {
			timer := time.NewTimer(m.interval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil
			case <-timer.C:
			}
		}

		select {
		case out <- sig:
			m.log.Info("mock signal emitted", "id", sig.ID, "job_type", sig.JobType)
		case <-ctx.Done():
			return nil
		}
	}

	m.log.Info("mock signals exhausted, idling")
	<-ctx.Done()
	return nil
}
