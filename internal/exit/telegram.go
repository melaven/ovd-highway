package exit

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"ovd/internal/domain"
)

const (
	sendTimeout   = 15 * time.Second
	drainTimeout  = 10 * time.Second
	maxDraftRunes = 2500 // keeps the whole message under Telegram's 4096 limit
)

type TelegramNotifier struct {
	bot    *bot.Bot
	chatID int64
	log    *slog.Logger
}

func NewTelegramNotifier(token string, chatID int64, log *slog.Logger) (*TelegramNotifier, error) {
	if token == "" || log == nil {
		return nil, errors.New("telegram: token and logger are required")
	}
	b, err := bot.New(token)
	if err != nil {
		return nil, fmt.Errorf("telegram: init bot: %w", err)
	}
	return &TelegramNotifier{bot: b, chatID: chatID, log: log.With("component", "telegram")}, nil
}

// Run returns when in is closed or ctx is cancelled. On cancel it drains
// leads already queued in in (bounded by drainTimeout).
func (n *TelegramNotifier) Run(ctx context.Context, in <-chan domain.QualifiedLead) error {
	for {
		select {
		case <-ctx.Done():
			n.drain(in)
			return nil
		case lead, ok := <-in:
			if !ok {
				return nil
			}
			n.send(ctx, lead)
		}
	}
}

func (n *TelegramNotifier) drain(in <-chan domain.QualifiedLead) {
	ctx, cancel := context.WithTimeout(context.Background(), drainTimeout)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			n.log.Warn("drain timed out, remaining leads dropped")
			return
		case lead, ok := <-in:
			if !ok {
				return
			}
			n.send(ctx, lead)
		}
	}
}

func (n *TelegramNotifier) send(ctx context.Context, lead domain.QualifiedLead) {
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()

	disablePreview := true
	_, err := n.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:             n.chatID,
		Text:               formatLead(lead),
		ParseMode:          models.ParseModeHTML,
		LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: &disablePreview},
	})
	if err != nil {
		n.log.Error("send message failed, lead dropped",
			"signal_id", lead.Signal.ID, "source", lead.Signal.Source, "err", err)
		return
	}
	n.log.Info("lead delivered", "signal_id", lead.Signal.ID, "score", lead.Qualification.Score)
}

func formatLead(l domain.QualifiedLead) string {
	q := l.Qualification
	var sb strings.Builder
	fmt.Fprintf(&sb, "🎯 <b>Score: %d/10</b>\n", q.Score)
	fmt.Fprintf(&sb, "📡 Источник: %s\n", html.EscapeString(l.Signal.Source))
	fmt.Fprintf(&sb, "💰 Бюджет: %s\n", html.EscapeString(q.EstimatedBudget))
	fmt.Fprintf(&sb, "🔥 Боль: %s\n", html.EscapeString(q.PainPoints))
	fmt.Fprintf(&sb, "🔗 <a href=\"%s\">Открыть заказ</a>", html.EscapeString(l.Signal.URL))
	if q.DraftProposal != "" {
		fmt.Fprintf(&sb, "\n\n<code>%s</code>", html.EscapeString(truncateRunes(q.DraftProposal, maxDraftRunes)))
	}
	return sb.String()
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
