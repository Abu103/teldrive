package main

import (
	"fmt"

	"github.com/tgdrive/teldrive/internal/config"
	"github.com/tgdrive/teldrive/internal/database"
	"github.com/tgdrive/teldrive/internal/logging"
	"github.com/tgdrive/teldrive/pkg/models"
	"go.uber.org/zap"
)

const (
	// The root directory ID for the file system
	rootID = "0196a580-e141-70f1-b269-b8846e881142"
)

func main() {
	// Initialize logger
	logging.SetConfig(&logging.Config{
		Level: zap.DebugLevel,
	})
	lg := logging.DefaultLogger().Sugar()
	defer lg.Sync()

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
	lg.Info("Testing database connection...")
	var result int
	if err := db.Raw("SELECT 1").Scan(&result).Error; err != nil {
		lg.Fatalw("Database connection test failed", "error", err)
	}
	lg.Infow("Database connection test successful", "result", result)

	// Verify root directory exists
	var rootDir models.File
	if err := db.Table("teldrive.files").Where("id = ?", rootID).First(&rootDir).Error; err != nil {
		lg.Warnw("Root directory not found in database", "root_id", rootID, "error", err)
	} else {
		lg.Infow("Root directory found", "root_id", rootID, "name", rootDir.Name)
	}

	// Find files with NULL parent_id and update them to use the root ID
	lg.Info("Finding files with NULL parent_id...")
	var nullParentIDFiles []models.File
	if err := db.Table("teldrive.files").Where("parent_id IS NULL AND id != ?", rootID).Find(&nullParentIDFiles).Error; err != nil {
		lg.Errorw("Failed to find files with NULL parent_id", "error", err)
	} else {
		lg.Infow("Found files with NULL parent_id", "count", len(nullParentIDFiles))

		// Update all files with NULL parent_id to use the root ID
		if err := db.Table("teldrive.files").Where("parent_id IS NULL AND id != ?", rootID).Update("parent_id", rootID).Error; err != nil {
			lg.Errorw("Failed to update files with NULL parent_id", "error", err)
		} else {
			lg.Infow("Updated files with NULL parent_id to use root ID", "count", len(nullParentIDFiles))
		}
	}

	// Find files with user_id = 1 and update them to 7331706161
	lg.Info("Finding files with user_id = 1...")
	var oldUserIDFiles []models.File
	if err := db.Table("teldrive.files").Where("user_id = ?", 1).Find(&oldUserIDFiles).Error; err != nil {
		lg.Errorw("Failed to find files with old user ID", "error", err)
	} else {
		lg.Infow("Found files with old user ID", "count", len(oldUserIDFiles))

		// Update all files with user_id = 1 to user_id = 7331706161
		if err := db.Table("teldrive.files").Where("user_id = ?", 1).Update("user_id", 7331706161).Error; err != nil {
			lg.Errorw("Failed to update files with old user ID", "error", err)
		} else {
			lg.Infow("Updated files with old user ID", "count", len(oldUserIDFiles))
		}
	}

	// List all files in the root directory
	lg.Info("Listing files in root directory...")
	var rootFiles []models.File
	if err := db.Table("teldrive.files").Where("parent_id = ?", rootID).Find(&rootFiles).Error; err != nil {
		lg.Errorw("Failed to list files in root directory", "error", err)
	} else {
		lg.Infow("Files in root directory", "count", len(rootFiles))
		for i, file := range rootFiles {
			lg.Infow(fmt.Sprintf("File %d", i+1), 
				"id", file.ID, 
				"name", file.Name, 
				"size", file.Size, 
				"user_id", file.UserId)
		}
	}

	lg.Info("Database root directory fix completed")
}
