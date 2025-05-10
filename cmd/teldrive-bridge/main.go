package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/tgdrive/teldrive/internal/config"
	"github.com/tgdrive/teldrive/internal/database"
	"github.com/tgdrive/teldrive/internal/logging"
	"github.com/tgdrive/teldrive/pkg/models"
	"go.uber.org/zap"
)

type BotStatus struct {
	Running   bool   `json:"running"`
	ParentID  string `json:"parentId"`
	StartTime string `json:"startTime"`
	PID       int    `json:"pid"`
}

type DirectoryInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ParentID string `json:"parentId"`
	Type     string `json:"type"`
}

var (
	botStatus = BotStatus{
		Running:   false,
		ParentID:  "",
		StartTime: "",
		PID:       0,
	}
	botCmd *exec.Cmd
)

func main() {
	// Command-line flags
	var port int
	flag.IntVar(&port, "port", 8090, "Port for the bridge API server")
	flag.Parse()

	// Initialize logger
	logging.SetConfig(&logging.Config{
		Level: zap.DebugLevel,
	})
	lg := logging.DefaultLogger().Sugar()
	defer lg.Sync()

	lg.Infow("Starting TelDrive Bridge", "port", port)

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

	// Set up Gin router
	router := gin.Default()

	// Configure CORS
	router.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"http://localhost:5174", "http://localhost:3000"},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	// API routes
	api := router.Group("/api")
	{
		// Get bot status
		api.GET("/bot/status", func(c *gin.Context) {
			c.JSON(http.StatusOK, botStatus)
		})

		// Start bot with parent ID
		api.POST("/bot/start", func(c *gin.Context) {
			var request struct {
				ParentID string `json:"parentId"`
				BotType  string `json:"botType"` // "fixed" or "simple"
			}

			if err := c.ShouldBindJSON(&request); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
				return
			}

			// Check if bot is already running
			if botStatus.Running {
				c.JSON(http.StatusConflict, gin.H{"error": "Bot is already running", "status": botStatus})
				return
			}

			// Determine which bot executable to use
			botExecutable := "fixedbot.exe"
			if request.BotType == "simple" {
				botExecutable = "simplebot.exe"
			}

			// Get the current directory
			currentDir, err := os.Getwd()
			if err != nil {
				lg.Errorw("Failed to get current directory", "error", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start bot"})
				return
			}

			// Construct the bot command
			botPath := filepath.Join(currentDir, botExecutable)
			botArgs := []string{}
			if request.ParentID != "" {
				botArgs = append(botArgs, fmt.Sprintf("-parent=%s", request.ParentID))
			}

			// Start the bot process
			lg.Infow("Starting bot", "executable", botExecutable, "args", botArgs)
			botCmd = exec.Command(botPath, botArgs...)
			
			// Set up pipes for stdout and stderr
			stdout, err := botCmd.StdoutPipe()
			if err != nil {
				lg.Errorw("Failed to create stdout pipe", "error", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start bot"})
				return
			}
			
			stderr, err := botCmd.StderrPipe()
			if err != nil {
				lg.Errorw("Failed to create stderr pipe", "error", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start bot"})
				return
			}
			
			// Start the bot
			if err := botCmd.Start(); err != nil {
				lg.Errorw("Failed to start bot", "error", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start bot"})
				return
			}
			
			// Update bot status
			botStatus.Running = true
			botStatus.ParentID = request.ParentID
			botStatus.StartTime = time.Now().Format(time.RFC3339)
			botStatus.PID = botCmd.Process.Pid
			
			// Start goroutines to handle bot output
			go func() {
				scanner := bufio.NewScanner(stdout)
				for scanner.Scan() {
					lg.Infow("Bot stdout", "message", scanner.Text())
				}
			}()
			
			go func() {
				scanner := bufio.NewScanner(stderr)
				for scanner.Scan() {
					lg.Errorw("Bot stderr", "message", scanner.Text())
				}
			}()
			
			// Wait for the bot to finish in a goroutine
			go func() {
				err := botCmd.Wait()
				botStatus.Running = false
				botStatus.PID = 0
				if err != nil {
					lg.Errorw("Bot exited with error", "error", err)
				} else {
					lg.Infow("Bot exited successfully")
				}
			}()
			
			c.JSON(http.StatusOK, gin.H{
				"message": fmt.Sprintf("%s started successfully", botExecutable),
				"status":  botStatus,
			})
		})

		// Stop bot
		api.POST("/bot/stop", func(c *gin.Context) {
			if !botStatus.Running || botCmd == nil || botCmd.Process == nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "No bot is running"})
				return
			}

			// Send interrupt signal to the bot process
			if err := botCmd.Process.Signal(syscall.SIGINT); err != nil {
				lg.Errorw("Failed to send interrupt signal to bot", "error", err)
				
				// Try to kill the process if interrupt fails
				if err := botCmd.Process.Kill(); err != nil {
					lg.Errorw("Failed to kill bot process", "error", err)
					c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to stop bot"})
					return
				}
			}

			// Update bot status
			botStatus.Running = false
			botStatus.PID = 0

			c.JSON(http.StatusOK, gin.H{
				"message": "Bot stopped successfully",
				"status":  botStatus,
			})
		})

		// Get directories
		api.GET("/directories", func(c *gin.Context) {
			var directories []DirectoryInfo
			
			// Query folders from the database
			if err := db.Table("teldrive.files").
				Where("type = ?", "folder").
				Select("id, name, parent_id, type").
				Find(&directories).Error; err != nil {
				lg.Errorw("Failed to query directories", "error", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch directories"})
				return
			}
			
			c.JSON(http.StatusOK, directories)
		})

		// Get directory info
		api.GET("/directories/:id", func(c *gin.Context) {
			id := c.Param("id")
			
			var directory DirectoryInfo
			if err := db.Table("teldrive.files").
				Where("id = ? AND type = ?", id, "folder").
				Select("id, name, parent_id, type").
				First(&directory).Error; err != nil {
				lg.Errorw("Failed to query directory", "error", err, "id", id)
				c.JSON(http.StatusNotFound, gin.H{"error": "Directory not found"})
				return
			}
			
			c.JSON(http.StatusOK, directory)
		})

		// Update parent ID for files
		api.POST("/update-parent", func(c *gin.Context) {
			var request struct {
				ParentID string `json:"parentId"`
				FileIDs  []string `json:"fileIds"`
			}

			if err := c.ShouldBindJSON(&request); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
				return
			}

			// Check if the parent directory exists
			var parentDir models.File
			if request.ParentID != "" {
				if err := db.Table("teldrive.files").Where("id = ? AND type = ?", request.ParentID, "folder").First(&parentDir).Error; err != nil {
					c.JSON(http.StatusNotFound, gin.H{"error": "Parent directory not found"})
					return
				}
			}

			// Update the parent ID for the specified files
			if err := db.Table("teldrive.files").Where("id IN ?", request.FileIDs).Update("parent_id", request.ParentID).Error; err != nil {
				lg.Errorw("Failed to update parent ID", "error", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update parent ID"})
				return
			}

			c.JSON(http.StatusOK, gin.H{
				"message": fmt.Sprintf("Updated parent ID for %d files", len(request.FileIDs)),
				"parentId": request.ParentID,
			})
		})
	}

	// Set up a channel to listen for interrupt signals
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)

	// Start the server in a goroutine
	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: router,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			lg.Fatalw("Failed to start server", "error", err)
		}
	}()

	lg.Infow("TelDrive Bridge API server started", "port", port)
	lg.Infow("Press Ctrl+C to stop")

	// Wait for interrupt signal
	<-signalChan
	lg.Info("Received interrupt signal, shutting down...")

	// Stop the bot if it's running
	if botStatus.Running && botCmd != nil && botCmd.Process != nil {
		lg.Info("Stopping bot...")
		if err := botCmd.Process.Signal(syscall.SIGINT); err != nil {
			lg.Errorw("Failed to send interrupt signal to bot", "error", err)
			if err := botCmd.Process.Kill(); err != nil {
				lg.Errorw("Failed to kill bot process", "error", err)
			}
		}
	}

	// Create a deadline for graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Shut down the server
	if err := srv.Shutdown(ctx); err != nil {
		lg.Fatalw("Server forced to shutdown", "error", err)
	}

	lg.Info("Server exited")
}
