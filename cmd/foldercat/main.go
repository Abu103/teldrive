package main

import (
	"flag"
	"fmt"
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
	var verbose bool
	var targetFolder string
	var parentFolder string
	
	flag.BoolVar(&dryRun, "dry-run", true, "Dry run (don't actually move files)")
	flag.BoolVar(&verbose, "verbose", false, "Show detailed output")
	flag.StringVar(&targetFolder, "folder", "", "Target folder ID to scan (leave empty for root directory)")
	flag.StringVar(&parentFolder, "parent", "", "Parent folder ID to move files to")
	flag.Parse()
	
	// Print banner
	fmt.Println("\n==================================================")
	fmt.Println("  TELDRIVE FOLDER CATEGORIZER")
	fmt.Println("  Organizes files into directories based on type")
	fmt.Println("==================================================")

	// Initialize logger - set to ErrorLevel to reduce noise
	logging.SetConfig(&logging.Config{
		Level: zap.ErrorLevel,
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
	// You can customize these categories and file extensions as needed
	categories := map[string][]string{
		// Media files
		"1080p":      {".mp4", ".mkv", ".avi"},       // HD videos
		"4K":         {".mp4", ".mkv"},                // 4K videos
		"Audio":      {".mp3", ".wav", ".flac", ".ogg"}, // Audio files
		
		// Documents
		"PDFs":       {".pdf"},                          // PDF documents
		"Office":     {".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx"}, // Office documents
		"Text":       {".txt", ".md", ".csv"},         // Plain text files
		
		// Images
		"Photos":     {".jpg", ".jpeg", ".png"},        // Photos
		"Graphics":   {".gif", ".bmp", ".webp", ".svg"}, // Graphics
		
		// Other
		"Archives":   {".zip", ".rar", ".7z", ".tar", ".gz"}, // Compressed files
		"Code":       {".py", ".js", ".html", ".css", ".go", ".java"}, // Code files
	}

	// If a parent folder is specified, we'll move all files to that folder
	if parentFolder != "" {
		// Check if the parent folder exists
		var parentDir models.File
		err := db.Table("teldrive.files").
			Where("id = ? AND type = 'dir'", parentFolder).
			First(&parentDir).Error
			
		if err != nil {
			fmt.Printf("ERROR: Parent folder with ID '%s' not found\n", parentFolder)
			lg.Fatalw("Parent folder not found", "parent_id", parentFolder, "error", err)
		}
		
		fmt.Printf("Target parent folder: %s (ID: %s)\n", parentDir.Name, parentDir.ID)
		
		// Get files from the specified folder or root
		var files []models.File
		query := db.Table("teldrive.files").Where("type != 'dir'")
		
		if targetFolder != "" {
			query = query.Where("parent_id = ?", targetFolder)
			fmt.Printf("Scanning files in folder with ID: %s\n", targetFolder)
		} else {
			query = query.Where("parent_id IS NULL")
			fmt.Println("Scanning files in root directory")
		}
		
		if err := query.Find(&files).Error; err != nil {
			lg.Fatalw("Failed to fetch files", "error", err)
		}
		
		fmt.Printf("\nFound %d files to move\n", len(files))
		
		// Move all files to the specified parent folder
		fmt.Println("\nMoving files to target folder")
		fmt.Println("============================")
		
		moved := 0
		for _, file := range files {
			fmt.Printf("File: %s\n", file.Name)
			
			if verbose {
				fmt.Printf("  File ID: %s\n", file.ID)
				fmt.Printf("  Current parent: %s\n", file.ParentId)
				fmt.Printf("  New parent: %s\n\n", parentFolder)
			}
			
			if !dryRun {
				if err := db.Table("teldrive.files").
					Where("id = ?", file.ID).
					Update("parent_id", parentFolder).Error; err != nil {
					lg.Errorw("Failed to update parent ID", "file", file.Name, "error", err)
					fmt.Printf("  ERROR: Failed to update parent ID for %s: %v\n", file.Name, err)
				} else {
					moved++
				}
			} else {
				moved++
			}
		}
		
		fmt.Println("============================")
		fmt.Printf("Total files moved: %d\n\n", moved)
		
		if dryRun {
			fmt.Println("This was a DRY RUN - no files were actually moved.")
			fmt.Println("Run with --dry-run=false to actually move the files.")
		} else {
			fmt.Println("Files moved successfully!")
		}
		
		return
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

	// Get files from the specified folder or root
	var files []models.File
	query := db.Table("teldrive.files").Where("type != 'dir'")
	
	if targetFolder != "" {
		query = query.Where("parent_id = ?", targetFolder)
		fmt.Printf("\nFound files in folder with ID: %s\n", targetFolder)
	} else {
		query = query.Where("parent_id IS NULL")
		fmt.Println("\nFound files in root directory")
	}
	
	if err := query.Find(&files).Error; err != nil {
		lg.Fatalw("Failed to fetch files", "error", err)
	}
	
	fmt.Printf("Found %d files to categorize\n", len(files))
	
	// Categorize files
	fmt.Println("\nAuto-Categorizing Files")
	fmt.Println("=======================")
	
	categorized := 0
	for _, file := range files {
		ext := strings.ToLower(filepath.Ext(file.Name))
		
		for category, extensions := range categories {
			for _, validExt := range extensions {
				if ext == validExt {
					dirID := categoryDirs[category]
					
					// Always show basic info for categorized files
					fmt.Printf("File: %s → %s\n", file.Name, category)
					
					// Show more details in verbose mode
					if verbose {
						fmt.Printf("  File ID: %s\n", file.ID)
						fmt.Printf("  Category: %s\n", category)
						fmt.Printf("  Directory ID: %s\n", dirID)
						fmt.Printf("  Extension: %s\n\n", ext)
					}
					
					lg.Infow("Categorizing file", 
						"file", file.Name, 
						"category", category, 
						"directory_id", dirID)
					
					if !dryRun {
						if err := db.Table("teldrive.files").
							Where("id = ?", file.ID).
							Update("parent_id", dirID).Error; err != nil {
							lg.Errorw("Failed to update parent ID", "file", file.Name, "error", err)
							fmt.Printf("  ERROR: Failed to update parent ID for %s: %v\n", file.Name, err)
						}
					}
					categorized++
					break
				}
			}
		}
	}
	
	fmt.Println("======================")
	fmt.Printf("Total files categorized: %d\n\n", categorized)
	
	if dryRun {
		fmt.Println("This was a DRY RUN - no files were actually moved.")
		fmt.Println("Run with --dry-run=false to actually move the files.")
		lg.Info("Dry run completed. Use --dry-run=false to actually move files.")
	} else {
		fmt.Println("Auto-categorization completed successfully!")
		lg.Info("Auto-categorization completed.")
	}
}
