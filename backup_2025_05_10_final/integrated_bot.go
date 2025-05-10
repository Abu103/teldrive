package tgc

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/tgdrive/teldrive/internal/config"
	"github.com/tgdrive/teldrive/internal/logging"
	"github.com/tgdrive/teldrive/pkg/models"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// IntegratedBotHandler is a bot handler that can be integrated with TelDrive
type IntegratedBotHandler struct {
	config    *config.TGConfig
	botToken  string
	channelId int64
	parentId  string
	db        *gorm.DB
	client    *telegram.Client
	logger    *zap.SugaredLogger
}

// NewIntegratedBotHandler creates a new integrated bot handler
func NewIntegratedBotHandler(config *config.TGConfig, botToken string, channelId int64, parentId string, db *gorm.DB) *IntegratedBotHandler {
	return &IntegratedBotHandler{
		config:    config,
		botToken:  botToken,
		channelId: channelId,
		parentId:  parentId,
		db:        db,
		logger:    logging.DefaultLogger().Sugar(),
	}
}

// Start starts the bot handler
func (h *IntegratedBotHandler) Start(ctx context.Context) error {
	h.logger.Infow("Starting integrated bot handler", 
		"channel_id", h.channelId,
		"parent_id", h.parentId)

	// Write to a log file
	logToFile(fmt.Sprintf("INTEGRATED BOT STARTING with channel ID: %d and parent ID: %s", 
		h.channelId, h.parentId))

	// Create a memory storage for the session
	storage := new(session.StorageMemory)

	// Create update handler
	updateHandler := &integratedUpdateHandler{bot: h}

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
				"actual_channel_id", actualChannelID,
				"parent_id", h.parentId)
			
			logToFile(fmt.Sprintf("Integrated bot is now listening for updates from channel ID: %d (actual: %d)", 
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

// Custom update handler for the integrated bot
type integratedUpdateHandler struct {
	bot *IntegratedBotHandler
}

// Handle implements the telegram.UpdateHandler interface
func (h *integratedUpdateHandler) Handle(ctx context.Context, updates tg.UpdatesClass) error {
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
func (h *integratedUpdateHandler) handleChannelMessage(ctx context.Context, update *tg.UpdateNewChannelMessage) {
	msg, ok := update.Message.(*tg.Message)
	if !ok || msg == nil {
		h.bot.logger.Error("Failed to cast message to *tg.Message")
		return
	}
	
	// Log message details
	h.bot.logger.Infow("Channel message received",
		"message_id", msg.ID,
		"has_media", msg.Media != nil,
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
			"actual_configured_channel_id", actualConfiguredChannelID)
		
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
func (h *integratedUpdateHandler) processDocument(ctx context.Context, doc *tg.MessageMediaDocument, channelID int64) {
	h.bot.logger.Infow("Document media found", "doc_type", fmt.Sprintf("%T", doc.Document))
	logToFile(fmt.Sprintf("Processing document from channel %d", channelID))
	
	document, ok := doc.Document.(*tg.Document)
	if !ok || document == nil {
		h.bot.logger.Error("Failed to cast document to *tg.Document")
		logToFile("ERROR: Failed to cast document to *tg.Document")
		return
	}
	
	h.bot.logger.Infow("Document details", 
		"doc_id", document.ID,
		"doc_size", document.Size,
		"attributes_count", len(document.Attributes))
	
	// Find filename attribute
	var fileName string
	for _, attr := range document.Attributes {
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
	
	// Check if file with this name already exists
	var existingFile models.File
	err := h.bot.db.Table("teldrive.files").
		Where("name = ? AND user_id = ?", fileName, 7331706161).
		First(&existingFile).Error
		
	if err == nil {
		// File with this name already exists, append timestamp to make it unique
		h.bot.logger.Infow("File with this name already exists, making filename unique", 
			"original_name", fileName)
		
		ext := ""
		baseName := fileName
		
		// Extract extension if present
		if idx := strings.LastIndex(fileName, "."); idx >= 0 {
			ext = fileName[idx:]
			baseName = fileName[:idx]
		}
		
		// Append timestamp and a UUID to ensure uniqueness
		timestamp := time.Now().Format("20060102_150405")
		uniqueID := uuid.New().String()[:8]
		fileName = fmt.Sprintf("%s_%s_%s%s", baseName, timestamp, uniqueID, ext)
		
		h.bot.logger.Infow("Using unique filename", "new_name", fileName)
	}
	
	// Create new file entry in database
	size := document.Size
	
	// Generate a new UUID for the file
	fileID := uuid.New().String()
	
	// Create the file record
	newFile := models.File{
		ID:     fileID,
		Name:   fileName,
		Size:   &size,
		Type:   "file",
		UserId: 7331706161, // Using the fixed user ID
	}
	
	// Set parent ID if specified
	if h.bot.parentId != "" {
		newFile.ParentId = &h.bot.parentId
	}
	
	// Log the file details before insertion
	logToFile(fmt.Sprintf("Attempting to insert file: %s (ID: %s, Parent: %s, User: %d)", 
		fileName, fileID, h.bot.parentId, newFile.UserId))
	
	// Use direct SQL for better control and debugging
	var parentIDValue interface{} = nil
	if h.bot.parentId != "" {
		parentIDValue = h.bot.parentId
	}
	
	// Determine mime type based on file extension
	mimeType := "application/octet-stream" // Default mime type
	if ext := filepath.Ext(fileName); ext != "" {
		switch strings.ToLower(ext) {
		case ".jpg", ".jpeg":
			mimeType = "image/jpeg"
		case ".png":
			mimeType = "image/png"
		case ".gif":
			mimeType = "image/gif"
		case ".pdf":
			mimeType = "application/pdf"
		case ".mp4":
			mimeType = "video/mp4"
		case ".mp3":
			mimeType = "audio/mpeg"
		case ".txt":
			mimeType = "text/plain"
		case ".doc", ".docx":
			mimeType = "application/msword"
		case ".xls", ".xlsx":
			mimeType = "application/vnd.ms-excel"
		case ".zip":
			mimeType = "application/zip"
		case ".exe":
			mimeType = "application/x-msdownload"
		default:
			mimeType = "application/octet-stream"
		}
	}
	
	// Log the mime type
	h.bot.logger.Infow("Determined mime type", "filename", fileName, "mime_type", mimeType)
	logToFile(fmt.Sprintf("Using mime type: %s for file: %s", mimeType, fileName))
	
	// Insert using direct SQL
	result := h.bot.db.Exec(
		"INSERT INTO teldrive.files (id, name, size, type, user_id, parent_id, mime_type, created_at, updated_at) "+
		"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		fileID, fileName, size, "file", 7331706161, parentIDValue, mimeType, time.Now(), time.Now())
	
	if result.Error != nil {
		h.bot.logger.Errorw("Failed to insert file into database", "error", result.Error)
		logToFile(fmt.Sprintf("ERROR inserting file: %s - %v", fileName, result.Error))
		return
	}
	
	if result.RowsAffected == 0 {
		h.bot.logger.Warn("No rows affected when inserting file")
		logToFile(fmt.Sprintf("WARNING: No rows affected when inserting file: %s", fileName))
		return
	}
	
	h.bot.logger.Infow("File added to database successfully", 
		"file_id", fileID,
		"file_name", fileName,
		"parent_id", h.bot.parentId,
		"rows_affected", result.RowsAffected)
	
	logToFile(fmt.Sprintf("SUCCESS: File added to database: %s (ID: %s, Parent: %s)", 
		fileName, fileID, h.bot.parentId))
}
