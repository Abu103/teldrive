package main

import (
	"flag"
	"time"

	"github.com/tgdrive/teldrive/internal/config"
	"github.com/tgdrive/teldrive/internal/database"
	"github.com/tgdrive/teldrive/internal/logging"
	"github.com/tgdrive/teldrive/pkg/models"
	"go.uber.org/zap"
)

func main() {
	// Command-line flags
	var parentID string
	flag.StringVar(&parentID, "parent", "0196a91a-55f8-7414-8379-a9dde4c3ef6c", "Parent directory ID to set for files")
	flag.Parse()

	// Initialize logger
	logging.SetConfig(&logging.Config{
		Level: zap.DebugLevel,
	})
	lg := logging.DefaultLogger().Sugar()
	defer lg.Sync()

	lg.Infow("Using parent directory ID", "parent_id", parentID)

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

	// Test connection
	var result int
	if err := db.Raw("SELECT 1").Scan(&result).Error; err != nil {
		lg.Fatalw("Database connection test failed", "error", err)
	}
	lg.Infow("Database connection test successful", "result", result)

	// Check if the parent directory exists
	var parentDir models.File
	if err := db.Table("teldrive.files").Where("id = ?", parentID).First(&parentDir).Error; err != nil {
		lg.Fatalw("Parent directory not found", "parent_id", parentID, "error", err)
	}
	lg.Infow("Parent directory found", "id", parentDir.ID, "name", parentDir.Name, "type", parentDir.Type)

	// Find files with NULL parent_id or with the default root ID
	lg.Info("Finding files to update...")
	var filesToUpdate []models.File
	if err := db.Table("teldrive.files").Where("(parent_id IS NULL OR parent_id = '0196a580-e141-70f1-b269-b8846e881142') AND id != ? AND type = 'file'", parentID).Find(&filesToUpdate).Error; err != nil {
		lg.Errorw("Failed to find files to update", "error", err)
	}
	lg.Infow("Found files to update", "count", len(filesToUpdate))

	// Update files with the specified parent ID
	if len(filesToUpdate) > 0 {
		lg.Infow("Updating files with new parent ID", "parent_id", parentID, "file_count", len(filesToUpdate))
		
		// Update in batches to avoid overwhelming the database
		batchSize := 50
		for i := 0; i < len(filesToUpdate); i += batchSize {
			end := i + batchSize
			if end > len(filesToUpdate) {
				end = len(filesToUpdate)
			}
			
			batch := filesToUpdate[i:end]
			var fileIDs []string
			for _, file := range batch {
				fileIDs = append(fileIDs, file.ID)
			}
			
			// Update the parent_id for this batch
			if err := db.Table("teldrive.files").Where("id IN ?", fileIDs).Update("parent_id", parentID).Error; err != nil {
				lg.Errorw("Failed to update files", "error", err, "batch", i/batchSize)
			} else {
				lg.Infow("Updated files", "batch", i/batchSize, "count", len(batch))
			}
			
			// Sleep briefly to avoid overwhelming the database
			time.Sleep(100 * time.Millisecond)
		}
	}

	// Verify the update
	var updatedCount int64
	if err := db.Table("teldrive.files").Where("parent_id = ?", parentID).Count(&updatedCount).Error; err != nil {
		lg.Errorw("Failed to count updated files", "error", err)
	}
	lg.Infow("Files with specified parent ID after update", "parent_id", parentID, "count", updatedCount)

	lg.Info("Parent ID update completed")
}
