package main

import (
	"context"
	"flag"
	"fmt"
	"mime"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/tgdrive/teldrive/internal/api"
	"github.com/tgdrive/teldrive/internal/config"
	"github.com/tgdrive/teldrive/internal/database"
	"github.com/tgdrive/teldrive/internal/logging"
	"github.com/tgdrive/teldrive/pkg/models"
	"go.uber.org/zap"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Simple update handler that logs all updates
type updateHandler struct {
	db        *gorm.DB
	channelID int64
	logger    *zap.SugaredLogger
	parentID  string // Parent directory ID for uploaded files
}

// Handle implements telegram.UpdateHandler interface
func (h *updateHandler) Handle(ctx context.Context, updates tg.UpdatesClass) error {
	h.logger.Infow("Received update", "type", fmt.Sprintf("%T", updates))

	switch u := updates.(type) {
	case *tg.Updates:
		h.logger.Infow("Processing batch updates", "count", len(u.Updates))
		for _, update := range u.Updates {
			switch update := update.(type) {
			case *tg.UpdateNewChannelMessage:
				h.handleChannelMessage(ctx, update)
			default:
				h.logger.Debugw("Ignoring update", "type", fmt.Sprintf("%T", update))
			}
		}
	case *tg.UpdateShortMessage:
		h.logger.Infow("Received short message", "message", u.Message)
	case *tg.UpdateShortChatMessage:
		h.logger.Infow("Received short chat message", "message", u.Message)
	default:
		h.logger.Debugw("Ignoring update class", "type", fmt.Sprintf("%T", u))
	}

	return nil
}

// handleChannelMessage processes channel messages
func (h *updateHandler) handleChannelMessage(ctx context.Context, update *tg.UpdateNewChannelMessage) {
	msg, ok := update.Message.(*tg.Message)
	if !ok {
		h.logger.Warnw("Unexpected message type", "type", fmt.Sprintf("%T", update.Message))
		return
	}

	h.logger.Infow("Channel message received",
		"message_id", msg.ID,
		"has_media", msg.Media != nil,
		"media_type", fmt.Sprintf("%T", msg.Media),
		"date", msg.Date)

	// Extract channel ID from the message
	var channelID int64
	if msg.PeerID != nil {
		if channel, ok := msg.PeerID.(*tg.PeerChannel); ok {
			channelID = channel.ChannelID
		}
	}

	// Check if this message is from our configured channel
	// For channels with IDs like -1002523726746, we need to extract the actual ID (2523726746)
	// by removing the -100 prefix for comparison
	actualConfiguredID := h.channelID
	if h.channelID < 0 {
		// If the channel ID starts with -100, extract the actual ID
		if h.channelID <= -1000000000 {
			// Extract the actual channel ID by removing the -100 prefix
			// For example, -1002523726746 should become 2523726746
			strID := fmt.Sprintf("%d", -h.channelID) // Convert to positive string
			if len(strID) > 3 && strID[:3] == "100" {
				// Parse the ID without the 100 prefix
				actualID, err := strconv.ParseInt(strID[3:], 10, 64)
				if err == nil {
					actualConfiguredID = actualID
				} else {
					h.logger.Errorw("Failed to parse channel ID", "error", err)
					actualConfiguredID = -h.channelID % 1000000000
				}
			} else {
				actualConfiguredID = -h.channelID % 1000000000
			}
		} else {
			actualConfiguredID = -h.channelID
		}
	}
	
	h.logger.Infow("Checking channel ID",
		"message_channel_id", channelID,
		"configured_channel_id", h.channelID,
		"actual_configured_channel_id", actualConfiguredID,
		"direct_match", channelID == h.channelID,
		"actual_match", channelID == actualConfiguredID)

	if channelID != actualConfiguredID {
		h.logger.Debugw("Message is not from our channel", 
			"message_channel_id", channelID, 
			"our_channel_id", h.channelID)
		return
	}

	h.logger.Info("Processing message from our channel")

	// Check if the message has a document
	if msg.Media != nil {
		if mediaDoc, ok := msg.Media.(*tg.MessageMediaDocument); ok {
			h.processDocument(ctx, mediaDoc, channelID)
		} else {
			h.logger.Info("Message does not contain a document")
		}
	} else {
		h.logger.Info("Message does not contain a document")
	}
}

// processDocument handles document media in messages
func (h *updateHandler) processDocument(ctx context.Context, doc *tg.MessageMediaDocument, channelID int64) {
	document, ok := doc.Document.(*tg.Document)
	if !ok {
		h.logger.Warn("Document is not of type *tg.Document")
		return
	}

	h.logger.Infow("Document media found", "doc_type", fmt.Sprintf("%T", document))
	h.logger.Infow("Document details",
		"doc_id", document.ID,
		"doc_size", document.Size,
		"attributes_count", len(document.Attributes))

	// Extract filename from attributes
	var fileName string
	for i, attr := range document.Attributes {
		h.logger.Infow("Checking attribute", 
			"index", i, 
			"attr_type", fmt.Sprintf("%T", attr))
		if fileAttr, ok := attr.(*tg.DocumentAttributeFilename); ok {
			fileName = fileAttr.FileName
			h.logger.Infow("Found filename attribute", "filename", fileName)
			break
		}
	}
	
	if fileName == "" {
		h.logger.Warn("Document has no filename attribute")
		return
	}
	
	// Create new file entry in database
	size := document.Size
	
	// Get MIME type from the document
	mimeType := "application/octet-stream" // Default MIME type
	for _, attr := range document.Attributes {
		if mimeAttr, ok := attr.(*tg.DocumentAttributeFilename); ok {
			ext := filepath.Ext(mimeAttr.FileName)
			mimeType = mime.TypeByExtension(ext)
			if mimeType == "" {
				mimeType = "application/octet-stream"
			}
			break
		}
	}
	
	// Generate a new UUID for the file
	fileID := uuid.New().String()
	
	// Helper function to convert string to *string
	strToPtr := func(s string) *string {
		return &s
	}
	
	// Log the parent ID being used
	h.logger.Infow("Creating file with parent ID", "parent_id", h.parentID)
	logToFile(fmt.Sprintf("CREATING FILE WITH PARENT ID: %s", h.parentID))
	
	// Create the file instance using the correct model
	file := models.File{
		ID:        fileID,
		Name:      fileName,
		Type:      "file",
		MimeType:  mimeType,
		Size:      &size,
		Category:  "document",
		Encrypted: false,
		UserId:    7331706161, // Set to the specified user ID
		Status:    "active",
		ChannelId: &channelID,
		ParentId:  strToPtr(h.parentID), // Use the dynamic parent ID
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Parts:     datatypes.NewJSONSlice([]api.Part{}), // Empty parts array
	}
	
	// Test database connection
	var result int
	if err := h.db.Raw("SELECT 1").Scan(&result).Error; err != nil {
		h.logger.Errorw("Database connection test failed", "error", err)
		logToFile(fmt.Sprintf("DATABASE CONNECTION TEST FAILED: %v", err))
		return
	}
	h.logger.Info("Database connection test successful")
	
	// Log file entry details
	h.logger.Infow("Attempting to create file entry", 
		"filename", fileName, 
		"size", size, 
		"channel_id", channelID)
	
	// Log the SQL parameters being used
	parentIDValue := "<nil>"
	if file.ParentId != nil {
		parentIDValue = *file.ParentId
	}
	h.logger.Infow("SQL parameters", 
		"file_id", file.ID, 
		"name", file.Name,
		"parent_id", parentIDValue,
		"user_id", file.UserId)
	logToFile(fmt.Sprintf("SQL PARAMETERS: ID=%s, Name=%s, ParentID=%s, UserID=%d", 
		file.ID, file.Name, parentIDValue, file.UserId))
	
	// Use the direct SQL approach with positional parameters for PostgreSQL
	sql := `INSERT INTO teldrive.files (id, name, type, mime_type, size, category, encrypted, user_id, status, channel_id, parent_id, created_at, updated_at, parts) 
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14::jsonb)`
	
	if err := h.db.Exec(sql, 
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
			
			h.logger.Infow("Retrying with modified filename to avoid duplicate", 
				"original_name", originalName,
				"new_name", file.Name)
			
			// Try again with the modified filename
			if err := h.db.Exec(sql, 
				file.ID, file.Name, file.Type, file.MimeType, file.Size, file.Category, 
				file.Encrypted, file.UserId, file.Status, file.ChannelId, file.ParentId,
				file.CreatedAt, file.UpdatedAt, "[]").Error; err != nil {
				h.logger.Errorw("Failed to insert file with modified name", 
					"error", err, 
					"error_type", fmt.Sprintf("%T", err))
				logToFile(fmt.Sprintf("DATABASE INSERT FAILED AFTER RETRY: %v", err))
				return
			}
			
			h.logger.Infow("Successfully inserted file with modified name", 
				"file_id", file.ID,
				"original_name", originalName,
				"new_name", file.Name)
			logToFile(fmt.Sprintf("FILE INSERTED SUCCESSFULLY WITH MODIFIED NAME: %s (Original: %s, ID: %s)", file.Name, originalName, file.ID))
			return
		}
		
		// Handle other errors
		h.logger.Errorw("Failed to insert file into database", 
			"error", err, 
			"error_type", fmt.Sprintf("%T", err))
		logToFile(fmt.Sprintf("DATABASE INSERT FAILED: %v", err))
		return
	}
	
	h.logger.Infow("Successfully inserted file into database", 
		"file_id", file.ID,
		"file_name", file.Name)
	logToFile(fmt.Sprintf("FILE INSERTED SUCCESSFULLY: %s (ID: %s)", file.Name, file.ID))
}

// Helper function to log to a file
func logToFile(message string) {
	f, err := os.OpenFile("fixedbot.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Printf("Error opening log file: %v\n", err)
		return
	}
	defer f.Close()
	timestamp := time.Now().Format("2006-01-02T15:04:05-07:00")
	fmt.Fprintf(f, "[%s] %s\n", timestamp, message)
	// Also print to console for debugging
	fmt.Printf("[LOG] %s\n", message)
}

func main() {
	// Initialize logger
	logging.SetConfig(&logging.Config{
		Level: zap.DebugLevel,
	})
	lg := logging.DefaultLogger().Sugar()
	defer lg.Sync()

	// Command-line flags
	var parentID string
	flag.StringVar(&parentID, "parent", "0196a580-e141-70f1-b269-b8846e881142", "Parent directory ID for uploaded files")
	flag.Parse()

	lg.Infow("Using parent directory ID", "parent_id", parentID)

	// Configuration
	botToken := "7298222441:AAF7p3R3q6gIN0i1vtKPgeWY--7wA_654LM"
	channelID := int64(-1002523726746)

	// Database connection string
	dsn := "postgresql://postgres.qrwadtuuhhzbhckeyhbl:Barabanki1%4012@aws-0-ap-south-1.pooler.supabase.com:6543/postgres"

	// Database configuration
	dbConfig := &config.DBConfig{
		DataSource:  dsn,
		PrepareStmt: false, // Disable prepared statements to avoid conflicts
		LogLevel:    "1",
	}

	// Connect to database
	lg.Info("Connecting to database...")
	db, err := database.NewDatabase(dbConfig, lg)
	if err != nil {
		lg.Fatalw("Failed to connect to database", "error", err)
	}

	// Create a context that will be canceled on SIGINT or SIGTERM
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Create update handler
	handler := &updateHandler{
		db:        db,
		channelID: channelID,
		logger:    lg,
		parentID:  parentID,
	}

	// Initialize Telegram client
	lg.Info("Initializing Telegram client...")
	client := telegram.NewClient(22806755, "c6c12dbbee8bac63e9091dbaf6ef3b1d", telegram.Options{
		UpdateHandler: handler,
	})

	// Start the bot
	lg.Info("Starting bot...")
	if err := client.Run(ctx, func(ctx context.Context) error {
		// Check if the bot is authorized
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return err
		}
		lg.Infow("Auth status", "authorized", status.Authorized)

		if !status.Authorized {
			lg.Info("Authorizing bot...")
			// Authorize the bot using the token
			if _, err := client.Auth().Bot(ctx, botToken); err != nil {
				return fmt.Errorf("failed to authorize bot: %w", err)
			}
			lg.Info("Bot authorized successfully!")

			// Check the authorization status again
			status, err := client.Auth().Status(ctx)
			if err != nil {
				return err
			}
			lg.Infow("Final auth status", "authorized", status.Authorized)
		}

		// Get the actual channel ID
		actualChannelID := channelID
		if channelID < 0 {
			// Extract the actual ID by removing the -100 prefix
			actualChannelID = channelID * -1
		}

		lg.Infow("Listening for updates from channel", 
			"channel_id", channelID,
			"actual_channel_id", actualChannelID)

		// Wait for the context to be done
		<-ctx.Done()
		lg.Info("Bot exited gracefully")
		return nil
	}); err != nil {
		lg.Fatalw("Bot error", "error", err)
	}
}
