package tgc

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/tgdrive/teldrive/internal/api"
	"github.com/tgdrive/teldrive/internal/config"
	"github.com/tgdrive/teldrive/internal/logging"
	"github.com/tgdrive/teldrive/pkg/models"
	"go.uber.org/zap"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// StandaloneBotHandler is a simplified bot handler that doesn't rely on the complex initialization
type StandaloneBotHandler struct {
	config    *config.TGConfig
	botToken  string
	channelId int64
	db        *gorm.DB
	client    *telegram.Client
	logger    *zap.SugaredLogger
}

// NewStandaloneBotHandler creates a new standalone bot handler
func NewStandaloneBotHandler(config *config.TGConfig, botToken string, channelId int64, db *gorm.DB) *StandaloneBotHandler {
	return &StandaloneBotHandler{
		config:    config,
		botToken:  botToken,
		channelId: channelId,
		db:        db,
		logger:    logging.DefaultLogger().Sugar(),
	}
}

// Start starts the bot handler
func (h *StandaloneBotHandler) Start(ctx context.Context) error {
	h.logger.Infow("Starting standalone bot handler", 
		"channel_id", h.channelId,
		"bot_token_prefix", h.botToken[:10] + "...")

	// Write to a log file
	logToFile(fmt.Sprintf("STANDALONE BOT STARTING with channel ID: %d", h.channelId))

	// Create a memory storage for the session
	storage := new(session.StorageMemory)

	// Create update handler
	updateHandler := &standaloneUpdateHandler{bot: h}

	// Initialize the client
	h.client = telegram.NewClient(h.config.AppId, h.config.AppHash, telegram.Options{
		SessionStorage: storage,
		UpdateHandler:  updateHandler,
	})

	// Run the client in a goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- h.client.Run(ctx, func(ctx context.Context) error {
			h.logger.Info("Checking authorization status")
			
			// Check auth status
			status, err := h.client.Auth().Status(ctx)
			if err != nil {
				h.logger.Errorw("Failed to get auth status", "error", err)
				return err
			}

			h.logger.Infow("Auth status", "authorized", status.Authorized)
			
			if !status.Authorized {
				h.logger.Info("Bot not authorized, authorizing now...")
				
				// Create a dedicated context for authorization
				authCtx, authCancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer authCancel()
				
				// Bot authorization
				_, err := h.client.Auth().Bot(authCtx, h.botToken)
				if err != nil {
					h.logger.Errorw("Failed to authorize bot", "error", err)
					return err
				}
				
				h.logger.Info("Bot authorized successfully!")
			}
			
			// Extract actual channel ID (without -100 prefix)
			actualChannelID := h.channelId
			if h.channelId < 0 {
				// Remove the -100 prefix if it exists
				if h.channelId < -1000000000000 {
					actualChannelID = -h.channelId - 1000000000000
				} else if h.channelId < -1000000 {
					actualChannelID = -h.channelId - 1000000
				}
			}
			
			h.logger.Infow("Listening for updates from channel", 
				"channel_id", h.channelId,
				"actual_channel_id", actualChannelID)
			
			logToFile(fmt.Sprintf("Bot is now listening for updates from channel ID: %d (actual: %d)", 
				h.channelId, actualChannelID))
			
			// Wait for context to be done
			<-ctx.Done()
			return nil
		})
	}()
	
	// Wait for a short time to see if there are immediate errors
	select {
	case err := <-errChan:
		if err != nil {
			h.logger.Errorw("Bot client run failed immediately", "error", err)
			return err
		}
	case <-time.After(2 * time.Second):
		// No immediate error, continue
	}
	
	return nil
}

// Custom update handler for the standalone bot
type standaloneUpdateHandler struct {
	bot *StandaloneBotHandler
}

// Handle implements the telegram.UpdateHandler interface
func (h *standaloneUpdateHandler) Handle(ctx context.Context, updates tg.UpdatesClass) error {
	// Log the update
	h.bot.logger.Infow("Received update", "type", fmt.Sprintf("%T", updates))
	logToFile(fmt.Sprintf("UPDATE RECEIVED: type=%T", updates))
	
	// Process different update types
	switch u := updates.(type) {
	case *tg.Updates:
		h.bot.logger.Infow("Processing batch updates", "count", len(u.Updates))
		
		// Process each update in the batch
		for _, update := range u.Updates {
			h.bot.logger.Infow("Processing update", "type", fmt.Sprintf("%T", update))
			
			// Handle channel messages
			if channelMsg, ok := update.(*tg.UpdateNewChannelMessage); ok {
				h.handleChannelMessage(ctx, channelMsg)
			}
		}
		
	case *tg.UpdateShort:
		h.bot.logger.Infow("Received short update", "update_type", fmt.Sprintf("%T", u.Update))
		
		// Handle channel messages
		if channelMsg, ok := u.Update.(*tg.UpdateNewChannelMessage); ok {
			h.handleChannelMessage(ctx, channelMsg)
		}
		
	default:
		h.bot.logger.Infow("Received other update type", "type", fmt.Sprintf("%T", updates))
	}
	
	return nil
}

// handleChannelMessage processes channel messages
func (h *standaloneUpdateHandler) handleChannelMessage(ctx context.Context, update *tg.UpdateNewChannelMessage) {
	msg, ok := update.Message.(*tg.Message)
	if !ok || msg == nil {
		h.bot.logger.Error("Failed to cast message to *tg.Message")
		return
	}
	
	// Log message details
	h.bot.logger.Infow("Channel message received",
		"message_id", msg.ID,
		"has_media", msg.Media != nil,
		"media_type", fmt.Sprintf("%T", msg.Media),
		"date", msg.Date)
	
	// Check if this is from our target channel
	if peer, ok := msg.PeerID.(*tg.PeerChannel); ok {
		channelID := peer.ChannelID
		
		// Extract actual configured channel ID (without -100 prefix)
		actualConfiguredChannelID := h.bot.channelId
		if h.bot.channelId < 0 {
			// Remove the -100 prefix if it exists
			if h.bot.channelId < -1000000000000 {
				actualConfiguredChannelID = -h.bot.channelId - 1000000000000
			} else if h.bot.channelId < -1000000 {
				actualConfiguredChannelID = -h.bot.channelId - 1000000
			}
		}
		
		// Log channel ID comparison
		h.bot.logger.Infow("Checking channel ID",
			"message_channel_id", channelID,
			"configured_channel_id", h.bot.channelId,
			"actual_configured_channel_id", actualConfiguredChannelID,
			"direct_match", channelID == h.bot.channelId,
			"actual_match", channelID == actualConfiguredChannelID)
		
		logToFile(fmt.Sprintf("Message from channel ID: %d (our channel: %d, actual: %d)", 
			channelID, h.bot.channelId, actualConfiguredChannelID))
		
		// Process if it's from our channel
		if channelID == h.bot.channelId || channelID == actualConfiguredChannelID {
			h.bot.logger.Info("Processing message from our channel")
			logToFile(fmt.Sprintf("PROCESSING MESSAGE FROM OUR CHANNEL (ID: %d)", msg.ID))
			
			// Check if message contains a document (file)
			if doc, ok := msg.Media.(*tg.MessageMediaDocument); ok {
				h.processDocument(ctx, doc, channelID)
			} else {
				h.bot.logger.Info("Message does not contain a document")
				logToFile("Message does not contain a document")
			}
		} else {
			logToFile(fmt.Sprintf("IGNORING MESSAGE (not from our channel, ID: %d)", channelID))
		}
	}
}

// processDocument handles document media in messages
func (h *standaloneUpdateHandler) processDocument(ctx context.Context, doc *tg.MessageMediaDocument, channelID int64) {
	h.bot.logger.Infow("Document media found", "doc_type", fmt.Sprintf("%T", doc.Document))
	
	document, ok := doc.Document.(*tg.Document)
	if !ok || document == nil {
		h.bot.logger.Error("Failed to cast document to *tg.Document")
		return
	}
	
	h.bot.logger.Infow("Document details", 
		"doc_id", document.ID,
		"doc_size", document.Size,
		"attributes_count", len(document.Attributes))
	
	// Find filename attribute
	var fileName string
	for i, attr := range document.Attributes {
		h.bot.logger.Infow("Checking attribute", 
			"index", i, 
			"attr_type", fmt.Sprintf("%T", attr))
		if fileAttr, ok := attr.(*tg.DocumentAttributeFilename); ok {
			fileName = fileAttr.FileName
			h.bot.logger.Infow("Found filename attribute", "filename", fileName)
			break
		}
	}
	
	if fileName == "" {
		h.bot.logger.Warn("Document has no filename attribute")
		return
	}
	
	// Create new file entry in database
	size := document.Size
	
	// Generate a new UUID for the file
	fileID := uuid.New().String()
	
	// Create the file instance using the correct model
	file := models.File{
		ID:        fileID,
		Name:      fileName,
		Type:      "file",
		MimeType:  "application/octet-stream", // Default mime type
		Size:      &size,
		Category:  "document", // Default category
		Encrypted: false,
		UserId:    7331706161, // Using the specified user ID
		Status:    "active",
		ChannelId: &channelID,
		ParentId:  nil, // Set to root directory so it appears in the main view
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Parts:     datatypes.NewJSONSlice([]api.Part{}), // Empty parts array
	}
	
	// Test database connection
	var result int
	if err := h.bot.db.Raw("SELECT 1").Scan(&result).Error; err != nil {
		h.bot.logger.Errorw("Database connection test failed", "error", err)
		logToFile(fmt.Sprintf("DATABASE CONNECTION TEST FAILED: %v", err))
		return
	}
	h.bot.logger.Info("Database connection test successful")
	
	// Log file entry details
	h.bot.logger.Infow("Attempting to create file entry", 
		"filename", fileName, 
		"size", size, 
		"channel_id", channelID)
	
	// Use the direct SQL approach with PostgreSQL-style positional parameters
	sql := `INSERT INTO teldrive.files (id, name, type, mime_type, size, category, encrypted, user_id, status, channel_id, parent_id, created_at, updated_at, parts) 
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14::jsonb)`
	
	if err := h.bot.db.Exec(sql, 
		file.ID, file.Name, file.Type, file.MimeType, file.Size, file.Category, 
		file.Encrypted, file.UserId, file.Status, file.ChannelId, file.ParentId,
		file.CreatedAt, file.UpdatedAt, "[]").Error; err != nil {
		// Check if this is a duplicate key error
		if strings.Contains(err.Error(), "duplicate key value violates unique constraint") {
			// Append timestamp to filename to make it unique
			timestamp := time.Now().Format("20060102_150405")
			originalName := file.Name
			file.Name = fmt.Sprintf("%s_%s", originalName, timestamp)
			file.ID = uuid.New().String() // Generate a new UUID as well
			
			h.bot.logger.Infow("Retrying with modified filename to avoid duplicate", 
				"original_name", originalName,
				"new_name", file.Name)
			
			// Try again with the modified filename
			if err := h.bot.db.Exec(sql, 
				file.ID, file.Name, file.Type, file.MimeType, file.Size, file.Category, 
				file.Encrypted, file.UserId, file.Status, file.ChannelId, file.ParentId,
				file.CreatedAt, file.UpdatedAt, "[]").Error; err != nil {
				h.bot.logger.Errorw("Failed to insert file with modified name", 
					"error", err, 
					"error_type", fmt.Sprintf("%T", err))
				logToFile(fmt.Sprintf("DATABASE INSERT FAILED AFTER RETRY: %v", err))
				return
			}
			
			h.bot.logger.Infow("Successfully inserted file with modified name", 
				"file_id", file.ID,
				"original_name", originalName,
				"new_name", file.Name)
			return
		}
		
		// Handle other errors
		h.bot.logger.Errorw("Failed to insert file into database", 
			"error", err, 
			"error_type", fmt.Sprintf("%T", err))
		logToFile(fmt.Sprintf("DATABASE INSERT FAILED: %v", err))
		return
	}
	
	h.bot.logger.Infow("Successfully inserted file into database", 
		"file_id", file.ID,
		"file_name", file.Name)
	logToFile(fmt.Sprintf("FILE INSERTED SUCCESSFULLY: %s (ID: %s)", file.Name, file.ID))
}

// Helper function to log to a file
func logToFile(message string) {
	f, _ := os.OpenFile("teldrive_standalone_bot.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if f != nil {
		defer f.Close()
		f.WriteString(fmt.Sprintf("[%s] %s\n", time.Now().Format(time.RFC3339), message))
	}
}
