package onramp

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/google/uuid"

	"ovd/internal/domain"
)

type HabrSource struct {
	client *http.Client
	log    *slog.Logger
}

func NewHabrSource(log *slog.Logger) *HabrSource {
	return &HabrSource{
		client: &http.Client{Timeout: 15 * time.Second},
		log:    log.With("component", "habr_onramp"),
	}
}

func (h *HabrSource) Start(ctx context.Context, out chan<- domain.RawSignal) error {
	h.log.Info("habr poller started")
	h.poll(ctx, out)

	for {
		delay := 45*time.Second + time.Duration(rand.IntN(30))*time.Second
		timer := time.NewTimer(delay)

		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
			h.poll(ctx, out)
		}
	}
}

func (h *HabrSource) poll(ctx context.Context, out chan<- domain.RawSignal) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://freelance.habr.com/tasks", nil)
	if err != nil {
		h.log.Error("failed to build habr request", "err", err)
		return
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/120.0.0.0")

	resp, err := h.client.Do(req)
	if err != nil {
		h.log.Error("habr request failed", "err", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		h.log.Warn("habr returned non-200 status", "status", resp.StatusCode)
		return
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		h.log.Error("failed to parse habr html", "err", err)
		return
	}

	parsedCount := 0
	doc.Find("li.content-list__item").Each(func(_ int, s *goquery.Selection) {
		titleNode := s.Find("div.task__title a")
		title := strings.TrimSpace(titleNode.Text())
		path, exists := titleNode.Attr("href")
		if !exists || title == "" {
			return
		}

		price := strings.TrimSpace(s.Find("div.task__price").Text())

		sig := domain.RawSignal{
			ID:         uuid.NewString(),
			Source:     "habr",
			URL:        "https://freelance.habr.com" + path,
			Text:       fmt.Sprintf("%s\nБюджет: %s", title, price),
			ReceivedAt: time.Now().UTC(),
		}

		select {
		case out <- sig:
			parsedCount++
		case <-ctx.Done():
			return
		}
	})

	h.log.Debug("habr poll cycle completed", "parsed", parsedCount)
}
