package tgc

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/tgdrive/teldrive/internal/api"
	"github.com/tgdrive/teldrive/internal/config"
	"github.com/tgdrive/teldrive/internal/logging"
	"github.com/tgdrive/teldrive/pkg/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"go.uber.org/zap/zapcore"
)

type BotHandler struct {
	config    *config.TGConfig
	botToken  string
	channelId int64
	db        *gorm.DB
	client    *telegram.Client
	mu        sync.Mutex
}

// Custom update handler
type botUpdateHandler struct {
	bot *BotHandler
}

// Write debug info to a file
func writeDebugInfo(format string, args ...interface{}) {
	f, _ := os.OpenFile("teldrive_bot_log.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if f != nil {
		defer f.Close()
		f.WriteString(fmt.Sprintf("[%s] %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, args...)))
	}
}

// Implement the telegram.UpdateHandler interface
func (h *botUpdateHandler) Handle(ctx context.Context, updates tg.UpdatesClass) error {
	// Write to a file immediately to ensure we capture the update
	writeDebugInfo("UPDATE RECEIVED: type=%T", updates)
	
	// Also log to the standard logger
	logging.DefaultLogger().Sugar().Infow("UPDATE RECEIVED", 
		"update_type", fmt.Sprintf("%T", updates))

	// Log the update type
	logging.DefaultLogger().Sugar().Infow("UPDATE RECEIVED", 
		"update_type", fmt.Sprintf("%T", updates),
		"channel_id", h.bot.channelId)

	// Process different update types
	switch u := updates.(type) {
	case *tg.Updates:
		logging.DefaultLogger().Sugar().Infow("Processing batch updates", "count", len(u.Updates))
		
		// Process each update in the batch
		for _, update := range u.Updates {
			logging.DefaultLogger().Sugar().Infow("Processing update", "update_type", fmt.Sprintf("%T", update))
			
			// Handle channel messages
			if channelMsg, ok := update.(*tg.UpdateNewChannelMessage); ok {
				if msg, ok := channelMsg.Message.(*tg.Message); ok {
					// Log message details
					logging.DefaultLogger().Sugar().Infow("Channel message received",
						"message_id", msg.ID,
						"has_media", msg.Media != nil,
						"date", msg.Date)
					
					// Check if this is from our target channel
					if peer, ok := msg.PeerID.(*tg.PeerChannel); ok {
						channelID := peer.ChannelID
						
						// For channels with ID like -1002523726746, we need to extract the actual ID
						// by removing the -100 prefix for comparison
						actualConfiguredChannelID := h.bot.channelId
						if h.bot.channelId < 0 {
							// Remove the -100 prefix if it exists
							if h.bot.channelId < -1000000000000 {
								actualConfiguredChannelID = -h.bot.channelId - 1000000000000
							} else if h.bot.channelId < -1000000 {
								actualConfiguredChannelID = -h.bot.channelId - 1000000
							}
						}
						
						// Write to debug log file
						f, _ := os.OpenFile("teldrive_bot_debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
						if f != nil {
							defer f.Close()
							f.WriteString(fmt.Sprintf("[%s] Message from channel ID: %d (our channel: %d, actual: %d)\n", 
								time.Now().Format(time.RFC3339), channelID, h.bot.channelId, actualConfiguredChannelID))
						}
						
						// Log channel ID comparison
						logging.DefaultLogger().Sugar().Infow("Checking channel ID",
							"message_channel_id", channelID,
							"configured_channel_id", h.bot.channelId,
							"actual_configured_channel_id", actualConfiguredChannelID,
							"direct_match", channelID == h.bot.channelId,
							"actual_match", channelID == actualConfiguredChannelID)
						
						// Check both the direct ID and the processed ID
						if channelID == h.bot.channelId || channelID == actualConfiguredChannelID {
							logging.DefaultLogger().Sugar().Infow("Processing message from our channel")
							
							// Write to debug log file
							f, _ := os.OpenFile("teldrive_bot_debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
							if f != nil {
								defer f.Close()
								f.WriteString(fmt.Sprintf("[%s] PROCESSING MESSAGE FROM CHANNEL %d (message ID: %d)\n", 
									time.Now().Format(time.RFC3339), channelID, msg.ID))
							}
							
							h.bot.handleNewMessage(ctx, channelMsg)
						} else {
							// Write to debug log file
							f, _ := os.OpenFile("teldrive_bot_debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
							if f != nil {
								defer f.Close()
								f.WriteString(fmt.Sprintf("[%s] IGNORING MESSAGE FROM CHANNEL %d (not matching our channel ID)\n", 
									time.Now().Format(time.RFC3339), channelID))
							}
						}
					}
				}
			}
		}
	
	case *tg.UpdateShort:
		logging.DefaultLogger().Sugar().Infow("Received short update", "update", u.Update)
	
	case *tg.UpdatesTooLong:
		logging.DefaultLogger().Sugar().Infow("Received updates too long notification")
	
	default:
		logging.DefaultLogger().Sugar().Infow("Received other update type", "type", fmt.Sprintf("%T", updates))
	}

	return nil
}

func NewBotHandler(config *config.TGConfig, botToken string, channelId int64, db *gorm.DB) *BotHandler {
	return &BotHandler{
		config:    config,
		botToken:  botToken,
		channelId: channelId,
		db:        db,
	}
}

func (h *BotHandler) Start(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Write to a file to log the start time
	f, _ := os.OpenFile("teldrive_bot_log.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if f != nil {
		defer f.Close()
		f.WriteString(fmt.Sprintf("[%s] BOT STARTING with channel ID: %d\n", time.Now().Format(time.RFC3339), h.channelId))
	}

	// Set debug level logging
	logging.SetConfig(&logging.Config{
		Level:    zapcore.DebugLevel,
		FilePath: "",
	})

	// Validate configuration
	if h.botToken == "" {
		logging.DefaultLogger().Sugar().Errorw("Bot token is empty")
		return fmt.Errorf("bot token is required")
	}

	if h.channelId == 0 {
		logging.DefaultLogger().Sugar().Errorw("Channel ID is not set")
		return fmt.Errorf("channel ID is required")
	}

	// Log configuration details
	logging.DefaultLogger().Sugar().Infow("Starting bot handler", 
		"channel_id", h.channelId,
		"bot_token_length", len(h.botToken),
		"app_id", h.config.AppId,
		"app_hash", h.config.AppHash)

	// Create the handler
	updateHandler := &botUpdateHandler{bot: h}

	// Create a memory storage for the session
	storage := new(session.StorageMemory)

	// Create the client
	var err error
	h.client, err = NoAuthClient(ctx, h.config, updateHandler, storage)
	if err != nil {
		logging.DefaultLogger().Sugar().Errorw("Failed to create Telegram client", "error", err)
		return err
	}

	logging.DefaultLogger().Sugar().Infow("Telegram client created successfully")

	// Create a completely separate context for the bot client
	// This ensures it won't be canceled when the server context is canceled
	botCtx, botCancel := context.WithCancel(context.Background())
	
	// Use a goroutine to handle server shutdown
	go func() {
		<-ctx.Done() // Wait for server context to be done
		time.Sleep(5 * time.Second) // Give the bot some time to finish
		botCancel()   // Then cancel the bot context
	}()

	logging.DefaultLogger().Sugar().Infow("Running bot client with independent context")
	
	// Use a channel to capture errors from the bot client
	errChan := make(chan error, 1)
	
	// Run the client in a goroutine
	go func() {
		errChan <- h.client.Run(botCtx, func(ctx context.Context) error {
			logging.DefaultLogger().Sugar().Infow("Checking authorization status")
			
			// Create a timeout context for the auth check
			authCheckCtx, authCheckCancel := context.WithTimeout(ctx, 30*time.Second)
			defer authCheckCancel()
			
			status, err := h.client.Auth().Status(authCheckCtx)
			if err != nil {
				logging.DefaultLogger().Sugar().Errorw("Failed to get auth status", 
					"error", err, 
					"error_type", fmt.Sprintf("%T", err))
				return err
			}

			logging.DefaultLogger().Sugar().Infow("Auth status", "authorized", status.Authorized)
			
			if !status.Authorized {
				logging.DefaultLogger().Sugar().Infow("Bot not authorized. Checking for FLOOD_WAIT", 
					"token_length", len(h.botToken))
				
				// Create a completely separate context for authorization
				authCtx, authCancel := context.WithTimeout(context.Background(), 120*time.Second)
				defer authCancel()
				
				// Log the token prefix (first 10 chars) to verify it's being read correctly
				tokenPrefix := ""
				if len(h.botToken) > 10 {
					tokenPrefix = h.botToken[:10]
				} else if len(h.botToken) > 0 {
					tokenPrefix = h.botToken
				}
				logging.DefaultLogger().Sugar().Infow("Using bot token", "prefix", tokenPrefix)
				
				// Try direct bot authorization with detailed error logging
				logging.DefaultLogger().Sugar().Infow("Calling Auth().Bot() directly")
				_, err = h.client.Auth().Bot(authCtx, h.botToken)
				if err != nil {
					// Check if it's a FLOOD_WAIT error
					errStr := err.Error()
					if strings.Contains(errStr, "FLOOD_WAIT") {
						logging.DefaultLogger().Sugar().Errorw("FLOOD_WAIT detected. Telegram is rate-limiting your requests", 
							"error", err)
						logging.DefaultLogger().Sugar().Infow("IMPORTANT: You need to wait before trying again. Also consider getting your own API credentials from https://my.telegram.org/apps")
						
						// Extract wait time if possible
						waitTimeStr := regexp.MustCompile(`FLOOD_WAIT \((\d+)\)`).FindStringSubmatch(errStr)
						if len(waitTimeStr) > 1 {
							waitTime, _ := strconv.Atoi(waitTimeStr[1])
							logging.DefaultLogger().Sugar().Infow("You need to wait before trying again", 
								"seconds", waitTime, 
								"minutes", waitTime/60)
						}
						return fmt.Errorf("Telegram FLOOD_WAIT error. Please wait before trying again")
					}
					
					logging.DefaultLogger().Sugar().Errorw("Failed to authorize bot", 
						"error", err, 
						"error_type", fmt.Sprintf("%T", err), 
						"token_prefix", tokenPrefix)
					return err
				}
				
				logging.DefaultLogger().Sugar().Infow("Bot authorized successfully!")
			}

			// Bot must be manually added to the channel as an admin/member.
			logging.DefaultLogger().Sugar().Infow("Bot is now listening for messages in channel", "channel_id", h.channelId)
			return nil
		})
	}()
	
	// Wait for a short time to see if there are immediate errors
	select {
	case err := <-errChan:
		if err != nil {
			logging.DefaultLogger().Sugar().Errorw("Bot client run failed immediately", "error", err)
			return err
		}
	case <-time.After(2 * time.Second):
		// No immediate error, continue
	}
	
	// Return nil to allow the server to continue running
	return nil
}

func (h *BotHandler) handleNewMessage(ctx context.Context, update *tg.UpdateNewChannelMessage) {
	logging.DefaultLogger().Sugar().Infow("Handling new channel message")
	msg, ok := update.Message.(*tg.Message)
	if !ok || msg == nil {
		logging.DefaultLogger().Sugar().Errorw("Failed to cast message to *tg.Message")
		return
	}

	// Log detailed message information
	logging.DefaultLogger().Sugar().Infow("Message details", 
		"message_id", msg.ID,
		"has_media", msg.Media != nil,
		"media_type", fmt.Sprintf("%T", msg.Media),
		"message_text", msg.Message,
		"date", msg.Date,
		"flags", msg.Flags)

	// Check if message contains a document (file)
	if doc, ok := msg.Media.(*tg.MessageMediaDocument); ok {
		logging.DefaultLogger().Sugar().Infow("Document media found", "doc_type", fmt.Sprintf("%T", doc.Document))
		document, ok := doc.Document.(*tg.Document)
		if !ok || document == nil {
			logging.DefaultLogger().Sugar().Errorw("Failed to cast document to *tg.Document")
			return
		}

		logging.DefaultLogger().Sugar().Infow("Document details", 
			"doc_id", document.ID,
			"doc_size", document.Size,
			"attributes_count", len(document.Attributes))

		// Find filename attribute
		var fileName string
		for i, attr := range document.Attributes {
			logging.DefaultLogger().Sugar().Infow("Checking attribute", 
				"index", i, 
				"attr_type", fmt.Sprintf("%T", attr))
			if fileAttr, ok := attr.(*tg.DocumentAttributeFilename); ok {
				fileName = fileAttr.FileName
				logging.DefaultLogger().Sugar().Infow("Found filename attribute", "filename", fileName)
				break
			}
		}

		if fileName == "" {
			logging.DefaultLogger().Sugar().Warnw("Document has no filename attribute")
			return
		}

		// Create new file entry in database
		size := document.Size
		channelID := h.channelId
		
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

		// Log database connection details
		logging.DefaultLogger().Sugar().Infow("Database connection details", 
			"db_connected", h.db != nil)

		// Test database connection
		var result int
		if err := h.db.Raw("SELECT 1").Scan(&result).Error; err != nil {
			logging.DefaultLogger().Sugar().Errorw("Database connection test failed", "error", err)
			return
		}
		logging.DefaultLogger().Sugar().Infow("Database connection test successful")

		// Log file entry details
		logging.DefaultLogger().Sugar().Infow("Attempting to create file entry", 
			"filename", fileName, 
			"size", size, 
			"channel_id", channelID,
			"user_id", file.UserId)

		// Try to create the file entry with the approach that we know works
		logging.DefaultLogger().Sugar().Infow("Attempting to create file in database", 
			"file_id", file.ID,
			"file_name", file.Name,
			"file_size", *file.Size,
			"channel_id", *file.ChannelId)
		
		// Use the direct SQL approach with positional parameters for PostgreSQL
		sql := `INSERT INTO teldrive.files (id, name, type, mime_type, size, category, encrypted, user_id, status, channel_id, parent_id, created_at, updated_at, parts) 
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14::jsonb)`
		
		if err := h.db.Exec(sql, 
			file.ID, file.Name, file.Type, file.MimeType, file.Size, file.Category, 
			file.Encrypted, file.UserId, file.Status, file.ChannelId, file.ParentId,
			file.CreatedAt, file.UpdatedAt, "[]").Error; err != nil {
			logging.DefaultLogger().Sugar().Errorw("Failed to insert file into database", 
				"error", err, 
				"error_type", fmt.Sprintf("%T", err))
			return
		}
		
		logging.DefaultLogger().Sugar().Infow("Successfully inserted file into database")

		logging.DefaultLogger().Sugar().Infow("New file added from channel",
			"file_id", file.ID,
			"file_name", file.Name,
			"channel_id", h.channelId,
		)
	} else {
		logging.DefaultLogger().Sugar().Infow("Message does not contain a document")
	}
}
