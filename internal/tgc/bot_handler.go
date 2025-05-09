package tgc

import (
	"context"
	"log"
	"sync"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/tgdrive/teldrive/internal/api"
	"github.com/tgdrive/teldrive/internal/config"
	"github.com/tgdrive/teldrive/internal/logging"
	"github.com/tgdrive/teldrive/pkg/models"
	"gorm.io/gorm"
)

type BotHandler struct {
	config     *config.TGConfig
	botToken   string
	channelId  int64
	db         *gorm.DB
	logger     *zap.SugaredLogger
	client     *telegram.Client
	middleware telegram.Middleware
	mu         sync.Mutex
}

func NewBotHandler(config *config.TGConfig, botToken string, channelId int64, db *gorm.DB) *BotHandler {
	return &BotHandler{
		config:    config,
		botToken:  botToken,
		channelId: channelId,
		db:        db,
		logger:    logging.DefaultLogger().Sugar(),
		middleware: tgc.NewMiddleware(
			config,
			tgc.WithFloodWait(),
			tgc.WithRateLimit(),
		),
	}
}

func (h *BotHandler) Start(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	var err error
	client, err := telegram.Client(
		ctx,
		h.config,
		h.middleware,
	)
	if err != nil {
		return err
	}

	h.client = client

	// Set up message handler
	client.Use(func(next telegram.UpdateHandler) telegram.UpdateHandler {
		return func(ctx context.Context, update tg.UpdateClass) error {
			switch update := update.(type) {
			case *tg.UpdateNewChannelMessage:
				if update.ChannelId == h.channelId {
					h.handleNewMessage(ctx, update)
				}
			}
			return next(ctx, update)
		}
	})

	// Start the bot
	err = h.client.Run(ctx, func(ctx context.Context) error {
		status, err := h.client.Auth().Status(ctx)
		if err != nil {
			return err
		}
		if !status.Authorized {
			_, err = h.client.Auth().Bot(ctx, h.botToken)
			if err != nil {
				return err
			}
		}
		return nil
	})

	return err
}

func (h *BotHandler) handleNewMessage(ctx context.Context, update *tg.UpdateNewChannelMessage) {
	message := update.Message.AsNotEmpty()
	if message == nil {
		return
	}

	// Check if message contains a document (file)
	if doc, ok := message.Media.(*tg.MessageMediaDocument); ok {
		document := doc.Document.AsNotEmpty()
		if document == nil {
			return
		}

		// Create new file entry in database
		file := models.File{
			Name:        document.Attributes[0].(*tg.DocumentAttributeFilename).FileName,
			Size:        document.Size,
			ChannelId:   h.channelId,
			MessageId:   update.MessageId,
			UserId:      auth.GetUser(ctx),
			CreatedAt:   time.Now().UTC(),
			ModifiedAt:  time.Now().UTC(),
			Type:        "file",
		}

		if err := h.db.Create(&file).Error; err != nil {
			h.logger.Errorw("failed to create file entry", "error", err)
			return
		}

		h.logger.Infow("new file added from channel",
			"file_id", file.ID,
			"file_name", file.Name,
			"channel_id", h.channelId,
		)
	}
}
