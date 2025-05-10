package main

import (
	"flag"
	"fmt"
	"strings"
	"time"
	"os"
	"encoding/json"

	"github.com/tgdrive/teldrive/internal/config"
	"github.com/tgdrive/teldrive/internal/database"
	"github.com/tgdrive/teldrive/internal/logging"
	"github.com/tgdrive/teldrive/pkg/models"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// SimpleFile is a simplified file structure for output
type SimpleFile struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Type     string     `json:"type"`
	Size     *int64     `json:"size"`
	ParentID *string    `json:"parent_id"`
	Parent   string     `json:"parent"`
	Created  time.Time  `json:"created_at"`
}

func main() {
	// Command-line flags
	var dryRun bool
	var verbose bool
	var listOnly bool
	var moveToRoot bool
	var targetFolderID string
	var searchPattern string
	var limit int
	var exportJson bool
	var showFolders bool
	
	flag.BoolVar(&dryRun, "dry-run", true, "Dry run (don't actually move files)")
	flag.BoolVar(&verbose, "verbose", false, "Show detailed output")
	flag.BoolVar(&listOnly, "list", false, "Only list files, don't move them")
	flag.BoolVar(&moveToRoot, "to-root", false, "Move files to root directory (NULL parent_id)")
	flag.StringVar(&targetFolderID, "to-folder", "", "Target folder ID to move files to")
	flag.StringVar(&searchPattern, "search", "", "Search for files with names containing this pattern")
	flag.IntVar(&limit, "limit", 100, "Maximum number of files to list/process")
	flag.BoolVar(&exportJson, "json", false, "Export results as JSON")
	flag.BoolVar(&showFolders, "folders", false, "Show available folders instead of files")
	flag.Parse()
	
	// Print banner
	fmt.Println("\n==================================================")
	fmt.Println("  TELDRIVE FILE RESCUE UTILITY")
	fmt.Println("  Find and restore your files")
	fmt.Println("==================================================")

	// If showing help, exit
	if len(os.Args) == 1 {
		fmt.Println("\nUsage:")
		fmt.Println("  --list                 List files without moving them")
		fmt.Println("  --folders              List available folders instead of files")
		fmt.Println("  --to-root              Move files to root directory")
		fmt.Println("  --to-folder <id>       Move files to specified folder ID")
		fmt.Println("  --search <pattern>     Search for files with names containing pattern")
		fmt.Println("  --limit <n>            Limit results to n files (default: 100)")
		fmt.Println("  --verbose              Show detailed file information")
		fmt.Println("  --json                 Output results as JSON")
		fmt.Println("  --dry-run=false        Actually perform the move operation (default: dry run)")
		fmt.Println("\nExamples:")
		fmt.Println("  filerescue --list --verbose                   List all files with details")
		fmt.Println("  filerescue --folders                          List all available folders")
		fmt.Println("  filerescue --to-root --dry-run=false          Move all files to root")
		fmt.Println("  filerescue --to-folder <id> --dry-run=false   Move all files to folder <id>")
		return
	}

	// Initialize logger
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
	fmt.Println("Connecting to database...")
	db, err := database.NewDatabase(dbConfig, lg)
	if err != nil {
		fmt.Printf("ERROR: Failed to connect to database: %v\n", err)
		return
	}

	// Test database connection
	var result int
	if err := db.Raw("SELECT 1").Scan(&result).Error; err != nil {
		fmt.Printf("ERROR: Database connection test failed: %v\n", err)
		return
	}
	fmt.Println("Database connection successful!")

	// Validate command-line arguments
	if moveToRoot && targetFolderID != "" {
		fmt.Println("ERROR: Cannot specify both --to-root and --to-folder")
		return
	}

	if !showFolders && !listOnly && !moveToRoot && targetFolderID == "" {
		fmt.Println("ERROR: Must specify either --list, --folders, --to-root, or --to-folder")
		return
	}

	// If showing folders, list all available folders and exit
	if showFolders {
		fmt.Println("\nAvailable folders:")
		fmt.Println("============================")
		
		var folders []models.File
		if err := db.Table("teldrive.files").Where("type = 'dir'").Find(&folders).Error; err != nil {
			fmt.Printf("ERROR: Failed to fetch folders: %v\n", err)
			return
		}
		
		if len(folders) == 0 {
			fmt.Println("No folders found.")
			return
		}
		
		for i, folder := range folders {
			fmt.Printf("%d. %s\n", i+1, folder.Name)
			fmt.Printf("   ID: %s\n", folder.ID)
			
			if folder.ParentId != nil && *folder.ParentId != "" {
				var parent models.File
				err := db.Table("teldrive.files").Where("id = ?", *folder.ParentId).First(&parent).Error
				if err == nil {
					fmt.Printf("   Parent: %s (ID: %s)\n", parent.Name, parent.ID)
				}
			} else {
				fmt.Printf("   Parent: ROOT\n")
			}
			
			fmt.Println()
		}
		
		fmt.Println("============================")
		return
	}
	
	// If a target folder is specified, verify it exists
	if targetFolderID != "" {
		var folder models.File
		err := db.Table("teldrive.files").
			Where("id = ? AND type = 'dir'", targetFolderID).
			First(&folder).Error
			
		if err != nil {
			fmt.Printf("ERROR: Target folder with ID '%s' not found\n", targetFolderID)
			return
		}
		
		fmt.Printf("Target folder: %s (ID: %s)\n", folder.Name, folder.ID)
	}

	// Build the query
	query := db.Table("teldrive.files").Where("type != 'dir'")
	
	// Add search pattern if specified
	if searchPattern != "" {
		query = query.Where("name ILIKE ?", "%"+searchPattern+"%")
		fmt.Printf("Searching for files containing: %s\n", searchPattern)
	}
	
	// Apply limit
	query = query.Limit(limit)
	
	// Get files
	var files []models.File
	if err := query.Find(&files).Error; err != nil {
		fmt.Printf("ERROR: Failed to fetch files: %v\n", err)
		return
	}
	
	fmt.Printf("\nFound %d files\n", len(files))
	
	// Create a map to store parent folder names
	parentMap := make(map[string]string)
	
	// Collect all parent IDs
	var parentIDs []string
	for _, file := range files {
		if file.ParentId != nil && *file.ParentId != "" {
			parentIDs = append(parentIDs, *file.ParentId)
		}
	}
	
	// Get all parent folders in a single query
	if len(parentIDs) > 0 {
		var parents []models.File
		if err := db.Table("teldrive.files").Where("id IN ?", parentIDs).Find(&parents).Error; err == nil {
			for _, parent := range parents {
				parentMap[parent.ID] = parent.Name
			}
		}
	}
	
	// Create simplified file objects for output
	simpleFiles := make([]SimpleFile, 0, len(files))
	for _, file := range files {
		parentName := "ROOT"
		if file.ParentId != nil && *file.ParentId != "" {
			if name, ok := parentMap[*file.ParentId]; ok {
				parentName = name
			} else {
				parentName = fmt.Sprintf("Unknown (%s)", *file.ParentId)
			}
		}
		
		simpleFiles = append(simpleFiles, SimpleFile{
			ID:       file.ID,
			Name:     file.Name,
			Type:     file.Type,
			Size:     file.Size,
			ParentID: file.ParentId,
			Parent:   parentName,
			Created:  file.CreatedAt,
		})
	}
	
	// Output as JSON if requested
	if exportJson {
		jsonData, err := json.MarshalIndent(simpleFiles, "", "  ")
		if err != nil {
			fmt.Printf("ERROR: Failed to marshal JSON: %v\n", err)
			return
		}
		
		fmt.Println(string(jsonData))
		return
	}
	
	// List all files with their current location
	fmt.Println("\nFiles found:")
	fmt.Println("============================")
	
	for i, file := range simpleFiles {
		fmt.Printf("%d. %s\n", i+1, file.Name)
		fmt.Printf("   Location: %s\n", file.Parent)
		
		if verbose {
			fmt.Printf("   File ID: %s\n", file.ID)
			fmt.Printf("   Type: %s\n", file.Type)
			fmt.Printf("   Size: %d bytes\n", file.Size)
			fmt.Printf("   Created: %s\n", file.Created.Format(time.RFC3339))
		}
		
		fmt.Println()
	}
	
	fmt.Println("============================")
	
	// If we're only listing files, we're done
	if listOnly {
		fmt.Println("Files listed successfully!")
		return
	}
	
	// Confirm before proceeding
	if !dryRun {
		fmt.Print("Are you sure you want to move these files? (y/n): ")
		var confirm string
		fmt.Scanln(&confirm)
		
		if strings.ToLower(confirm) != "y" {
			fmt.Println("Operation cancelled.")
			return
		}
	}
	
	// Move files if requested
	if !listOnly {
		// Confirm before proceeding with non-dry run
		if !dryRun {
			fmt.Printf("\nAre you sure you want to move %d files? (y/n): ", len(files))
			var confirm string
			fmt.Scanln(&confirm)
			
			if strings.ToLower(confirm) != "y" {
				fmt.Println("Operation cancelled.")
				return
			}
		}
		
		// Move files
		fmt.Println("\nMoving files")
		fmt.Println("============================")
		
		moved := 0
		for _, file := range simpleFiles {
			if moveToRoot {
				fmt.Printf("Moving file: %s to ROOT\n", file.Name)
			} else {
				fmt.Printf("Moving file: %s to folder ID: %s\n", file.Name, targetFolderID)
			}
			
			if !dryRun {
				// Use direct SQL update for better reliability
				var result *gorm.DB
				if moveToRoot {
					result = db.Exec("UPDATE teldrive.files SET parent_id = NULL WHERE id = ?", file.ID)
				} else {
					result = db.Exec("UPDATE teldrive.files SET parent_id = ? WHERE id = ?", targetFolderID, file.ID)
				}
				
				if result.Error != nil {
					fmt.Printf("  ERROR: Failed to update parent ID for %s: %v\n", file.Name, result.Error)
				} else if result.RowsAffected > 0 {
					moved++
				} else {
					fmt.Printf("  WARNING: No rows affected when updating %s\n", file.Name)
				}
			} else {
				moved++
			}
		}
		
		fmt.Println("============================")
		fmt.Printf("Total files to be moved: %d\n\n", moved)
		
		if dryRun {
			fmt.Println("This was a DRY RUN - no files were actually moved.")
			fmt.Println("Run with --dry-run=false to actually move the files.")
		} else {
			fmt.Println("Files moved successfully!")
		}
	} else {
		fmt.Println("Files listed successfully!")
	}
}
