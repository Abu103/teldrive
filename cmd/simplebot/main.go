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
	"github.com/gotd/td/session"
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
	// Log the update
	h.logger.Infow("Received update", "type", fmt.Sprintf("%T", updates))
	logToFile(fmt.Sprintf("UPDATE RECEIVED: type=%T", updates))
	
	// Process different update types
	switch u := updates.(type) {
	case *tg.Updates:
		h.logger.Infow("Processing batch updates", "count", len(u.Updates))
		
		// Process each update in the batch
		for _, update := range u.Updates {
			h.logger.Infow("Processing update", "type", fmt.Sprintf("%T", update))
			
			// Handle channel messages
			if channelMsg, ok := update.(*tg.UpdateNewChannelMessage); ok {
				h.handleChannelMessage(ctx, channelMsg)
			}
		}
		
	case *tg.UpdateShort:
		h.logger.Infow("Received short update", "update_type", fmt.Sprintf("%T", u.Update))
		
		// Handle channel messages
		if channelMsg, ok := u.Update.(*tg.UpdateNewChannelMessage); ok {
			h.handleChannelMessage(ctx, channelMsg)
		}
		
	default:
		h.logger.Infow("Received other update type", "type", fmt.Sprintf("%T", updates))
	}
	
	return nil
}

// handleChannelMessage processes channel messages
func (h *updateHandler) handleChannelMessage(ctx context.Context, update *tg.UpdateNewChannelMessage) {
	msg, ok := update.Message.(*tg.Message)
	if !ok || msg == nil {
		h.logger.Error("Failed to cast message to *tg.Message")
		return
	}
	
	// Log message details
	h.logger.Infow("Channel message received",
		"message_id", msg.ID,
		"has_media", msg.Media != nil,
		"media_type", fmt.Sprintf("%T", msg.Media),
		"date", msg.Date)
	
	// Check if this is from our target channel
	if peer, ok := msg.PeerID.(*tg.PeerChannel); ok {
		channelID := peer.ChannelID
		
		// Extract actual configured channel ID (without -100 prefix)
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
		
		// Log channel ID comparison
		h.logger.Infow("Checking channel ID",
			"message_channel_id", channelID,
			"configured_channel_id", h.channelID,
			"actual_configured_channel_id", actualConfiguredID,
			"direct_match", channelID == h.channelID,
			"actual_match", channelID == actualConfiguredID)
		
		logToFile(fmt.Sprintf("Message from channel ID: %d (our channel: %d, actual: %d)", 
			channelID, h.channelID, actualConfiguredID))
		
		// Process if it's from our channel
		if channelID == actualConfiguredID {
			h.logger.Info("Processing message from our channel")
			logToFile(fmt.Sprintf("PROCESSING MESSAGE FROM OUR CHANNEL (ID: %d)", msg.ID))
			
			// Check if message contains a document (file)
			if doc, ok := msg.Media.(*tg.MessageMediaDocument); ok {
				h.processDocument(ctx, doc, channelID)
			} else {
				h.logger.Info("Message does not contain a document")
				logToFile("Message does not contain a document")
			}
		} else {
			logToFile(fmt.Sprintf("IGNORING MESSAGE (not from our channel, ID: %d)", channelID))
		}
	}
}

// processDocument handles document media in messages
func (h *updateHandler) processDocument(ctx context.Context, doc *tg.MessageMediaDocument, channelID int64) {
	h.logger.Infow("Document media found", "doc_type", fmt.Sprintf("%T", doc.Document))
	
	document, ok := doc.Document.(*tg.Document)
	if !ok || document == nil {
		h.logger.Error("Failed to cast document to *tg.Document")
		return
	}
	
	h.logger.Infow("Document details", 
		"doc_id", document.ID,
		"doc_size", document.Size,
		"attributes_count", len(document.Attributes))
	
	// Find filename attribute
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
	
	// Use the direct SQL approach with positional parameters for PostgreSQL
	sql := `INSERT INTO teldrive.files (id, name, type, mime_type, size, category, encrypted, user_id, status, channel_id, parent_id, created_at, updated_at, parts) 
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14::jsonb)`
	
	if err := h.db.Exec(sql, 
		file.ID, file.Name, file.Type, file.MimeType, file.Size, file.Category, 
		file.Encrypted, file.UserId, file.Status, file.ChannelId, file.ParentId,
		file.CreatedAt, file.UpdatedAt, "[]").Error; err != nil {
		// If we get a duplicate key error, try again with a modified filename
		if strings.Contains(err.Error(), "duplicate key value") {
			// Add timestamp and random UUID suffix to filename to make it unique
			timestamp := time.Now().Format("20060102_150405")
			randomSuffix := uuid.New().String()[:8] // Use first 8 chars of a UUID for uniqueness
			newFileName := fmt.Sprintf("%s_%s_%s", fileName, timestamp, randomSuffix)
			h.logger.Infow("Retrying with modified filename to avoid duplicate", "original_name", fileName, "new_name", newFileName)
			
			// Generate a new UUID for the file
			file.ID = uuid.New().String()
			file.Name = newFileName
			
			// Try again with the modified filename using the same SQL approach
			if err := h.db.Exec(sql, 
				file.ID, file.Name, file.Type, file.MimeType, file.Size, file.Category, 
				file.Encrypted, file.UserId, file.Status, file.ChannelId, file.ParentId,
				file.CreatedAt, file.UpdatedAt, "[]").Error; err != nil {
				// If still getting duplicate error, try one more time with a completely different approach
				if strings.Contains(err.Error(), "duplicate key value") {
					// Generate a completely random name and new UUID
					file.ID = uuid.New().String()
					randomName := fmt.Sprintf("%s_%s", uuid.New().String()[:12], filepath.Base(fileName))
					h.logger.Infow("Second retry with completely random filename", "original_name", fileName, "random_name", randomName)
					file.Name = randomName
					
					// Try one more time with the random name
					if err := h.db.Exec(sql, 
						file.ID, file.Name, file.Type, file.MimeType, file.Size, file.Category, 
						file.Encrypted, file.UserId, file.Status, file.ChannelId, file.ParentId,
						file.CreatedAt, file.UpdatedAt, "[]").Error; err != nil {
						h.logger.Errorw("Failed to insert file with random name", "error", err, "error_type", fmt.Sprintf("%T", err))
						logToFile(fmt.Sprintf("DATABASE INSERT FAILED WITH RANDOM NAME: %v", err))
						return
					}
					h.logger.Infow("Successfully inserted file with random name", "file_id", file.ID, "original_name", fileName, "random_name", randomName)
					logToFile(fmt.Sprintf("FILE INSERTED SUCCESSFULLY WITH RANDOM NAME: %s (Original: %s, ID: %s)", randomName, fileName, file.ID))
					return
				}
				h.logger.Errorw("Failed to insert file with modified name", "error", err, "error_type", fmt.Sprintf("%T", err))
				logToFile(fmt.Sprintf("DATABASE INSERT FAILED WITH MODIFIED NAME: %v", err))
				return
			}
			
			h.logger.Infow("Successfully inserted file with modified name", "file_id", file.ID, "original_name", fileName, "new_name", newFileName)
			logToFile(fmt.Sprintf("FILE INSERTED SUCCESSFULLY WITH MODIFIED NAME: %s (Original: %s, ID: %s)", newFileName, fileName, file.ID))
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
	f, _ := os.OpenFile("simplebot.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if f != nil {
		defer f.Close()
		f.WriteString(fmt.Sprintf("[%s] %s\n", time.Now().Format(time.RFC3339), message))
	}
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
	appID := 22806755
	appHash := "c6c12dbbee8bac63e9091dbaf6ef3b1d"

	// Create a context with cancellation
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

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

	// Create a memory storage for the session
	storage := new(session.StorageMemory)

	// Create update handler
	handler := &updateHandler{
		db:        db,
		channelID: channelID,
		logger:    lg,
		parentID:  parentID,
	}

	// Initialize the client
	lg.Info("Initializing Telegram client...")
	client := telegram.NewClient(appID, appHash, telegram.Options{
		SessionStorage: storage,
		UpdateHandler:  handler,
	})

	// Run the client
	lg.Info("Starting bot...")
	err = client.Run(ctx, func(ctx context.Context) error {
		// Check auth status
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return fmt.Errorf("failed to get auth status: %w", err)
		}

		lg.Infow("Auth status", "authorized", status.Authorized)

		if !status.Authorized {
			lg.Info("Authorizing bot...")
			// Bot authorization flow
			_, err := client.Auth().Bot(ctx, botToken)
			if err != nil {
				return fmt.Errorf("failed to authorize: %w", err)
			}
			lg.Info("Bot authorized successfully!")
		}

		// Check if we're authorized now
		status, _ = client.Auth().Status(ctx)
		lg.Infow("Final auth status", "authorized", status.Authorized)
		
		// Extract actual channel ID (without -100 prefix)
		actualChannelID := channelID
		if channelID < 0 {
			// Remove the -100 prefix if it exists
			if channelID < -1000000000000 {
				actualChannelID = -channelID - 1000000000000
			} else if channelID < -1000000 {
				actualChannelID = -channelID - 1000000
			}
		}
		lg.Infow("Listening for updates from channel", 
			"channel_id", channelID,
			"actual_channel_id", actualChannelID)
		
		logToFile(fmt.Sprintf("Bot is now listening for updates from channel ID: %d (actual: %d)", 
			channelID, actualChannelID))
		
		// Wait for context to be done
		<-ctx.Done()
		return nil
	})

	if err != nil {
		lg.Errorw("Error running bot", "error", err)
		os.Exit(1)
	}

	lg.Info("Bot exited gracefully")
}
