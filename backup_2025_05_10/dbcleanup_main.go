package main

import (
	"fmt"
	"time"

	"github.com/tgdrive/teldrive/internal/config"
	"github.com/tgdrive/teldrive/internal/database"
	"github.com/tgdrive/teldrive/internal/logging"
	"github.com/tgdrive/teldrive/pkg/models"
	"go.uber.org/zap"
)

type DuplicateFile struct {
	Name  string
	Count int
}

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

	// Find duplicate files by name
	lg.Info("Finding duplicate files by name...")
	var duplicates []DuplicateFile
	if err := db.Raw(`
		SELECT name, COUNT(*) as count
		FROM teldrive.files
		GROUP BY name
		HAVING COUNT(*) > 1
		ORDER BY count DESC
	`).Scan(&duplicates).Error; err != nil {
		lg.Fatalw("Failed to find duplicate files", "error", err)
	}

	lg.Infow("Found duplicate files", "count", len(duplicates))

	// Process each duplicate file
	for _, dup := range duplicates {
		lg.Infow("Processing duplicate file", "name", dup.Name, "count", dup.Count)

		// Get all instances of this file
		var files []models.File
		if err := db.Table("teldrive.files").Where("name = ?", dup.Name).Find(&files).Error; err != nil {
			lg.Errorw("Failed to retrieve duplicate files", "name", dup.Name, "error", err)
			continue
		}

		// Keep the first file and rename the rest
		for i, file := range files {
			if i == 0 {
				lg.Infow("Keeping original file", "id", file.ID, "name", file.Name)
				continue
			}

			// Update the file name to make it unique
			timestamp := time.Now().Format("20060102_150405")
			newName := fmt.Sprintf("%s_%s_%d", file.Name, timestamp, i)
			
			// Update the user ID to 7331706161
			if err := db.Table("teldrive.files").Where("id = ?", file.ID).Updates(map[string]interface{}{
				"name":    newName,
				"user_id": 7331706161,
			}).Error; err != nil {
				lg.Errorw("Failed to update duplicate file", 
					"id", file.ID, 
					"old_name", file.Name, 
					"new_name", newName, 
					"error", err)
				continue
			}

			lg.Infow("Updated duplicate file", 
				"id", file.ID, 
				"old_name", file.Name, 
				"new_name", newName)
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

	// Find files with NULL parent_id and update them
	lg.Info("Finding files with NULL parent_id...")
	var nullParentIDFiles []models.File
	if err := db.Table("teldrive.files").Where("parent_id IS NULL").Find(&nullParentIDFiles).Error; err != nil {
		lg.Errorw("Failed to find files with NULL parent_id", "error", err)
	} else {
		lg.Infow("Found files with NULL parent_id", "count", len(nullParentIDFiles))
	}

	lg.Info("Database cleanup completed")
}
