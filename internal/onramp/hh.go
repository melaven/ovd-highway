package onramp

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"ovd/internal/domain"
)

const (
	hhVacanciesURL   = "https://api.hh.ru/vacancies?text=developer&per_page=10"
	hhUserAgent      = "ovd-highway/1.0 (maxim.nesterov84@gmail.com)"
	hhPollPeriod     = 60 * time.Second
	hhRequestTimeout = 20 * time.Second
)

type HHConnector struct {
	client   *http.Client
	endpoint string
}

func NewHHConnector() *HHConnector {
	return &HHConnector{
		client:   &http.Client{Timeout: hhRequestTimeout},
		endpoint: hhVacanciesURL,
	}
}

func (h *HHConnector) Name() string {
	return "hh"
}

func (h *HHConnector) Start(ctx context.Context, signals chan<- domain.RawSignal) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		if err := h.poll(ctx, signals); err != nil && ctx.Err() == nil {
			log.Printf("hh connector poll failed: %v", err)
		}

		timer := time.NewTimer(hhPollPeriod)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func (h *HHConnector) poll(ctx context.Context, signals chan<- domain.RawSignal) error {
	req, err := http.NewRequest(http.MethodGet, h.endpoint, nil)
	if err != nil {
		return fmt.Errorf("create HH request: %w", err)
	}
	req = req.WithContext(ctx)
	req.Header.Set("User-Agent", hhUserAgent)

	resp, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("request HH vacancies: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("HH returned HTTP status %d", resp.StatusCode)
	}

	var vacancies hhVacanciesResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&vacancies); err != nil {
		return fmt.Errorf("decode HH vacancies: %w", err)
	}

	for _, vacancy := range vacancies.Items {
		title := strings.TrimSpace(vacancy.Name)
		if title == "" {
			continue
		}

		description := strings.Join(filterNonEmpty(
			cleanHHSnippet(vacancy.Snippet.Requirement),
			cleanHHSnippet(vacancy.Snippet.Responsibility),
		), "\n")

		signal := domain.RawSignal{
			ID:         uuid.NewString(),
			Source:     "hh",
			URL:        vacancy.AlternateURL,
			Text:       strings.TrimSpace(title + "\n" + description),
			Budget:     formatHHSalary(vacancy.Salary),
			ReceivedAt: time.Now().UTC(),
			JobType:    "vacancy",
		}
		select {
		case signals <- signal:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func cleanHHSnippet(snippet string) string {
	snippet = strings.ReplaceAll(snippet, "<highlighttext>", "")
	snippet = strings.ReplaceAll(snippet, "</highlighttext>", "")
	return strings.TrimSpace(html.UnescapeString(snippet))
}

func filterNonEmpty(values ...string) []string {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			filtered = append(filtered, value)
		}
	}
	return filtered
}

func formatHHSalary(salary *hhSalary) string {
	if salary == nil {
		return ""
	}

	var amount string
	switch {
	case salary.From != nil && salary.To != nil:
		amount = fmt.Sprintf("%d - %d", *salary.From, *salary.To)
	case salary.From != nil:
		amount = fmt.Sprintf("from %d", *salary.From)
	case salary.To != nil:
		amount = fmt.Sprintf("up to %d", *salary.To)
	default:
		return ""
	}
	if salary.Currency != "" {
		return amount + " " + salary.Currency
	}
	return amount
}

type hhVacanciesResponse struct {
	Items []hhVacancy `json:"items"`
}

type hhVacancy struct {
	Name         string `json:"name"`
	AlternateURL string `json:"alternate_url"`
	Snippet      struct {
		Requirement    string `json:"requirement"`
		Responsibility string `json:"responsibility"`
	} `json:"snippet"`
	Salary *hhSalary `json:"salary"`
}

type hhSalary struct {
	From     *int   `json:"from"`
	To       *int   `json:"to"`
	Currency string `json:"currency"`
}
