package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/tgdrive/teldrive/internal/config"
	"github.com/tgdrive/teldrive/internal/database"
	"github.com/tgdrive/teldrive/internal/logging"
	"github.com/tgdrive/teldrive/internal/tgc"
	"go.uber.org/zap"
)

func main() {
	// Initialize logger
	logging.SetConfig(&logging.Config{
		Level: zap.DebugLevel,
	})
	lg := logging.DefaultLogger().Sugar()
	defer lg.Sync()

	// Configuration
	botToken := "7298222441:AAF7p3R3q6gIN0i1vtKPgeWY--7wA_654LM"
	channelID := int64(-1002523726746)

	// Create TG config
	tgConfig := &config.TGConfig{
		AppId:   22806755,
		AppHash: "c6c12dbbee8bac63e9091dbaf6ef3b1d",
	}

	// Create a context that will be canceled on SIGINT or SIGTERM
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Database connection string
	dsn := "postgresql://postgres.qrwadtuuhhzbhckeyhbl:Barabanki1%4012@aws-0-ap-south-1.pooler.supabase.com:6543/postgres"

	// Database configuration
	dbConfig := &config.DBConfig{
		DataSource:  dsn,
		PrepareStmt: false, // Disable prepared statements to avoid conflicts with PostgreSQL
		LogLevel:    "1",
	}

	// Connect to database
	lg.Info("Connecting to database...")
	db, err := database.NewDatabase(dbConfig, lg)
	if err != nil {
		lg.Fatalw("Failed to connect to database", "error", err)
	}

	// Create the standalone bot handler
	lg.Info("Creating standalone bot handler...")
	botHandler := tgc.NewStandaloneBotHandler(tgConfig, botToken, channelID, db)

	// Start the bot handler
	lg.Info("Starting bot handler...")
	err = botHandler.Start(ctx)
	if err != nil {
		lg.Fatalw("Failed to start bot handler", "error", err)
	}

	// Wait for termination signal
	lg.Info("Bot is now running. Press Ctrl+C to exit.")
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	lg.Info("Bot exited gracefully")
}
