package onramp

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

var controlCommandRe = regexp.MustCompile(`^/(add|remove|list)\b`)

// ControlBot is an admin-only Telegram bot that manages the MTProto
// channel whitelist at runtime: /add, /remove, /list.
type ControlBot struct {
	b       *bot.Bot
	adminID int64
	source  *MTProtoSource
	log     *slog.Logger
}

func NewControlBot(token string, adminID int64, source *MTProtoSource, log *slog.Logger) (*ControlBot, error) {
	if token == "" || adminID == 0 || source == nil || log == nil {
		return nil, fmt.Errorf("control bot: token, admin id, source and logger are required")
	}
	b, err := bot.New(token)
	if err != nil {
		return nil, fmt.Errorf("control bot init: %w", err)
	}
	c := &ControlBot{b: b, adminID: adminID, source: source, log: log.With("component", "control_bot")}
	b.RegisterHandlerRegexp(bot.HandlerTypeMessageText, controlCommandRe, c.handleUpdate)
	return c, nil
}

// Run starts long polling until ctx is cancelled.
func (c *ControlBot) Run(ctx context.Context) error {
	c.log.Info("control bot started", "admin_id", c.adminID)
	c.b.Start(ctx)
	return nil
}

func (c *ControlBot) handleUpdate(ctx context.Context, b *bot.Bot, update *models.Update) {
	msg := update.Message
	if msg == nil || msg.Text == "" {
		return
	}
	// Strict admin gate: only private messages from ADMIN_TG_ID.
	if msg.Chat.ID != c.adminID {
		return
	}

	fields := strings.Fields(msg.Text)
	if len(fields) == 0 {
		return
	}

	switch strings.ToLower(strings.TrimPrefix(fields[0], "@")) {
	case "/add":
		c.cmdAdd(ctx, b, msg.Chat.ID, fields[1:])
	case "/remove":
		c.cmdRemove(ctx, b, msg.Chat.ID, fields[1:])
	case "/list":
		c.cmdList(ctx, b, msg.Chat.ID)
	}
}

func (c *ControlBot) cmdAdd(ctx context.Context, b *bot.Bot, chatID int64, args []string) {
	if len(args) == 0 {
		c.reply(ctx, b, chatID, "usage: /add <channel_username>")
		return
	}
	target := strings.Join(args, " ")

	resolveCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	channelID, err := c.source.AddTarget(resolveCtx, target)
	if err != nil {
		c.reply(ctx, b, chatID, fmt.Sprintf("failed: %v", err))
		return
	}
	c.reply(ctx, b, chatID, fmt.Sprintf("added @%s (channel id %d)", target, channelID))
}

func (c *ControlBot) cmdRemove(ctx context.Context, b *bot.Bot, chatID int64, args []string) {
	if len(args) == 0 {
		c.reply(ctx, b, chatID, "usage: /remove <channel_username>")
		return
	}
	target := strings.Join(args, " ")

	resolveCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	removed, err := c.source.RemoveTarget(resolveCtx, target)
	if err != nil {
		c.reply(ctx, b, chatID, fmt.Sprintf("failed: %v", err))
		return
	}
	if !removed {
		c.reply(ctx, b, chatID, fmt.Sprintf("@%s was not in whitelist", target))
		return
	}
	c.reply(ctx, b, chatID, fmt.Sprintf("removed @%s", target))
}

func (c *ControlBot) cmdList(ctx context.Context, b *bot.Bot, chatID int64) {
	targets := c.source.ListTargets()
	if len(targets) == 0 {
		c.reply(ctx, b, chatID, "whitelist is empty")
		return
	}
	c.reply(ctx, b, chatID, "tracked channels:\n"+strings.Join(targets, "\n"))
}

func (c *ControlBot) reply(ctx context.Context, b *bot.Bot, chatID int64, text string) {
	if _, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   text,
	}); err != nil {
		c.log.Error("control bot reply failed", "chat_id", chatID, "err", err)
	}
}
