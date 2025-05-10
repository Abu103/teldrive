package cmd

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"regexp"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/go-co-op/gocron"
	"github.com/spf13/cobra"
	"github.com/tgdrive/teldrive/internal/api"
	"github.com/tgdrive/teldrive/internal/appcontext"
	"github.com/tgdrive/teldrive/internal/auth"
	"github.com/tgdrive/teldrive/internal/cache"
	"github.com/tgdrive/teldrive/internal/chizap"
	"github.com/tgdrive/teldrive/internal/config"
	"github.com/tgdrive/teldrive/internal/database"
	"github.com/tgdrive/teldrive/internal/events"
	"github.com/tgdrive/teldrive/internal/logging"
	"github.com/tgdrive/teldrive/internal/middleware"
	"github.com/tgdrive/teldrive/internal/tgc"
	"github.com/tgdrive/teldrive/internal/tgstorage"
	"github.com/tgdrive/teldrive/ui"

	"github.com/tgdrive/teldrive/pkg/cron"
	"github.com/tgdrive/teldrive/pkg/services"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gorm.io/gorm"
)

func NewRun() *cobra.Command {
	var cfg config.ServerCmdConfig
	loader := config.NewConfigLoader()
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Start Teldrive Server",
		Run: func(cmd *cobra.Command, args []string) {
			runApplication(cmd.Context(), &cfg)

		},
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if err := loader.Load(cmd, &cfg); err != nil {
				return err
			}
			if err := loader.Validate(); err != nil {
				return err
			}
			return nil
		},
	}
	loader.RegisterPlags(cmd.Flags(), "", cfg, false)
	return cmd
}

func findAvailablePort(startPort int) (int, error) {
	for port := startPort; port < startPort+100; port++ {
		addr := fmt.Sprintf(":%d", port)
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			continue
		}
		listener.Close()
		return port, nil
	}
	return 0, fmt.Errorf("no available ports found between %d and %d", startPort, startPort+100)
}

func runApplication(ctx context.Context, conf *config.ServerCmdConfig) {
	lvl, err := zapcore.ParseLevel(conf.Log.Level)
	if err != nil {
		lvl = zapcore.InfoLevel
	}
	logging.SetConfig(&logging.Config{
		Level:    lvl,
		FilePath: conf.Log.File,
	})

	lg := logging.DefaultLogger().Sugar()

	defer lg.Sync()

	port, err := findAvailablePort(conf.Server.Port)
	if err != nil {
		lg.Fatalw("failed to find available port", "err", err)
	}
	if port != conf.Server.Port {
		lg.Infof("Port %d is occupied, using port %d instead", conf.Server.Port, port)
		conf.Server.Port = port
	}

	scheduler := gocron.NewScheduler(time.UTC)

	cacher := cache.NewCache(ctx, &conf.Cache)

	db, err := database.NewDatabase(&conf.DB, lg)
	if err != nil {
		lg.Fatalw("failed to connect to database", "err", err)
	}

	// Initialize bot handlers
	// 1. Standard bot handler for the web interface
	botHandler := tgc.NewBotHandler(&conf.TG, conf.Bot.BotToken, conf.Bot.ChannelId, db)
	go func() {
		if err := botHandler.Start(ctx); err != nil {
			lg.Errorw("failed to start bot handler", "err", err)
		}
	}()
	
	// 2. Integrated bot for file uploads with parent ID support
	if conf.Bot.Enabled {
		// Log the actual channel ID format for debugging
		actualChannelID := conf.Bot.ChannelId
		if conf.Bot.ChannelId > 0 {
			// For positive channel IDs, we need to add -100 prefix for the bot
			actualChannelID = -1000000000000 - conf.Bot.ChannelId
			lg.Infow("Converting positive channel ID to bot format", 
				"original_id", conf.Bot.ChannelId,
				"converted_id", actualChannelID)
		}
		
		lg.Infow("Starting integrated Telegram bot", 
			"channel_id", actualChannelID,
			"parent_id", conf.Bot.ParentId,
			"bot_token_prefix", conf.Bot.BotToken[:10] + "...")
		
		// Create a log file for the integrated bot
		f, _ := os.OpenFile("integrated_bot.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if f != nil {
			defer f.Close()
			f.WriteString(fmt.Sprintf("[%s] STARTING INTEGRATED BOT with channel ID: %d, parent ID: %s\n", 
				time.Now().Format(time.RFC3339), actualChannelID, conf.Bot.ParentId))
		}
		
		integratedBot := tgc.NewIntegratedBotHandler(&conf.TG, conf.Bot.BotToken, actualChannelID, conf.Bot.ParentId, db)
		go func() {
			if err := integratedBot.Start(ctx); err != nil {
				lg.Errorw("failed to start integrated bot", "err", err)
				
				// Log error to file
				f, _ := os.OpenFile("integrated_bot.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
				if f != nil {
					defer f.Close()
					f.WriteString(fmt.Sprintf("[%s] ERROR STARTING INTEGRATED BOT: %v\n", 
						time.Now().Format(time.RFC3339), err))
				}
			}
		}()
	} else {
		lg.Info("Integrated Telegram bot is disabled")
	}

	if err != nil {
		lg.Fatalw("failed to create database", "err", err)
	}

	err = database.MigrateDB(db)

	if err != nil {
		lg.Fatalw("failed to migrate database", "err", err)
	}

	tgdb, err := tgstorage.NewDatabase(conf.TG.StorageFile)
	if err != nil {
		lg.Fatalw("failed to create tg db", "err", err)
	}

	err = tgstorage.MigrateDB(tgdb)
	if err != nil {
		lg.Fatalw("failed to migrate tg db", "err", err)
	}

	worker := tgc.NewBotWorker()

	logger := logging.DefaultLogger()

	eventRecorder := events.NewRecorder(ctx, db, logger)

	srv := setupServer(conf, db, cacher, logger, tgdb, worker, eventRecorder)

	cron.StartCronJobs(ctx, scheduler, db, conf)

	go func() {
		lg.Infof("Server started at http://localhost:%d", conf.Server.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			lg.Errorw("failed to start server", "err", err)
		}
	}()

	<-ctx.Done()

	lg.Info("Shutting down server...")

	eventRecorder.Shutdown()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), conf.Server.GracefulShutdown)

	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		lg.Errorw("server shutdown failed", "err", err)
	}

	lg.Info("Server stopped")
}

func setupServer(cfg *config.ServerCmdConfig, db *gorm.DB, cache cache.Cacher, lg *zap.Logger, tgdb *gorm.DB, worker *tgc.BotWorker, eventRecorder *events.Recorder) *http.Server {

	apiSrv := services.NewApiService(db, cfg, cache, tgdb, worker, eventRecorder)

	srv, err := api.NewServer(apiSrv, auth.NewSecurityHandler(db, cache, &cfg.JWT))

	if err != nil {
		lg.Fatal("failed to create server", zap.Error(err))
	}

	extendedSrv := services.NewExtendedMiddleware(srv, services.NewExtendedService(apiSrv))

	mux := chi.NewRouter()

	mux.Use(chimiddleware.Recoverer)
	mux.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH", "HEAD"},
		AllowedHeaders: []string{"Accept", "Authorization", "Content-Type"},
		MaxAge:         86400,
	}))
	mux.Use(chimiddleware.RealIP)
	mux.Use(middleware.InjectLogger(lg))
	mux.Use(chizap.ChizapWithConfig(lg, &chizap.Config{
		TimeFormat: time.RFC3339,
		UTC:        true,
		SkipPathRegexps: []*regexp.Regexp{
			regexp.MustCompile(`^/(assets|images|docs)/.*`),
		},
	}))
	mux.Use(appcontext.Middleware)
	mux.Mount("/api/", http.StripPrefix("/api", extendedSrv))
	mux.Handle("/*", middleware.SPAHandler(ui.StaticFS))

	return &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:           mux,
		ReadTimeout:       cfg.Server.ReadTimeout,
		WriteTimeout:      cfg.Server.WriteTimeout,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}
