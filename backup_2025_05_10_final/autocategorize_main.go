package main

import (
	"flag"
	"path/filepath"
	"strings"

	"github.com/tgdrive/teldrive/internal/config"
	"github.com/tgdrive/teldrive/internal/database"
	"github.com/tgdrive/teldrive/internal/logging"
	"github.com/tgdrive/teldrive/pkg/models"
	"go.uber.org/zap"
)

func main() {
	// Command-line flags
	var dryRun bool
	flag.BoolVar(&dryRun, "dry-run", true, "Dry run (don't actually move files)")
	flag.Parse()

	// Initialize logger
	logging.SetConfig(&logging.Config{
		Level: zap.InfoLevel,
	})
	lg := logging.DefaultLogger().Sugar()
	defer lg.Sync()

	// Database connection
	dsn := "postgresql://postgres.qrwadtuuhhzbhckeyhbl:Barabanki1%4012@aws-0-ap-south-1.pooler.supabase.com:6543/postgres"
	dbConfig := &config.DBConfig{
		DataSource:  dsn,
		PrepareStmt: false,
		LogLevel:    "1",
	}

	// Connect to database
	lg.Info("Connecting to database...")
	db, err := database.NewDatabase(dbConfig, lg)
	if err != nil {
		lg.Fatalw("Failed to connect to database", "error", err)
	}

	// Define category directories
	categories := map[string][]string{
		"Images":    {".jpg", ".jpeg", ".png", ".gif", ".bmp", ".webp"},
		"Documents": {".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx", ".txt"},
		"Videos":    {".mp4", ".avi", ".mkv", ".mov", ".wmv", ".flv"},
		"Audio":     {".mp3", ".wav", ".ogg", ".flac", ".aac"},
		"Archives":  {".zip", ".rar", ".7z", ".tar", ".gz"},
	}

	// Create or find category directories
	categoryDirs := make(map[string]string)
	for category := range categories {
		var dir models.File
		err := db.Table("teldrive.files").
			Where("name = ? AND type = 'dir'", category).
			First(&dir).Error
			
		if err != nil {
			// Directory doesn't exist, create it
			dir = models.File{
				Name: category,
				Type: "dir",
			}
			if !dryRun {
				if err := db.Table("teldrive.files").Create(&dir).Error; err != nil {
					lg.Errorw("Failed to create directory", "name", category, "error", err)
					continue
				}
			}
			lg.Infow("Created directory", "name", category, "id", dir.ID)
		} else {
			lg.Infow("Found existing directory", "name", category, "id", dir.ID)
		}
		
		categoryDirs[category] = dir.ID
	}

	// Get files in root directory (parent_id IS NULL)
	var files []models.File
	if err := db.Table("teldrive.files").
		Where("parent_id IS NULL AND type != 'dir'").
		Find(&files).Error; err != nil {
		lg.Fatalw("Failed to fetch files", "error", err)
	}
	
	lg.Infow("Found files in root directory", "count", len(files))
	
	// Categorize files
	for _, file := range files {
		ext := strings.ToLower(filepath.Ext(file.Name))
		
		for category, extensions := range categories {
			for _, validExt := range extensions {
				if ext == validExt {
					dirID := categoryDirs[category]
					lg.Infow("Categorizing file", 
						"file", file.Name, 
						"category", category, 
						"directory_id", dirID)
					
					if !dryRun {
						if err := db.Table("teldrive.files").
							Where("id = ?", file.ID).
							Update("parent_id", dirID).Error; err != nil {
							lg.Errorw("Failed to update parent ID", "file", file.Name, "error", err)
						}
					}
					break
				}
			}
		}
	}
	
	if dryRun {
		lg.Info("Dry run completed. Use --dry-run=false to actually move files.")
	} else {
		lg.Info("Auto-categorization completed.")
	}
}
