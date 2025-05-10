package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

func main() {
	// Configuration - use your bot token
	botToken := "8097661408:AAHPpOOXMTHuXsbUuQUAZ6QmUFwT4eHelUE"
	appID := 22806755
	appHash := "c6c12dbbee8bac63e9091dbaf6ef3b1d"

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Create a memory storage for the session
	storage := new(session.StorageMemory)

	// Initialize the client
	fmt.Println("Initializing Telegram client...")
	client := telegram.NewClient(appID, appHash, telegram.Options{
		SessionStorage: storage,
	})

	// Run the client
	fmt.Println("Starting bot...")
	err := client.Run(ctx, func(ctx context.Context) error {
		// Check auth status
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return fmt.Errorf("failed to get auth status: %w", err)
		}

		fmt.Printf("Auth status: %v\n", status.Authorized)

		if !status.Authorized {
			fmt.Println("Authorizing bot...")
			// Create a dedicated context for authorization
			authCtx, authCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer authCancel()
			
			// Bot authorization
			_, err := client.Auth().Bot(authCtx, botToken)
			if err != nil {
				return fmt.Errorf("failed to authorize: %w", err)
			}
			fmt.Println("Bot authorized successfully!")
		}

		// Create API client
		api := client.API()

		// Get bot info
		self, err := client.Self(ctx)
		if err != nil {
			return fmt.Errorf("failed to get self: %w", err)
		}
		fmt.Printf("Bot info: ID=%d, Username=@%s\n", self.ID, self.Username)

		// Get dialogs (chats/channels)
		fmt.Println("Getting dialogs (channels and chats)...")
		
		// Get dialogs
		dialogs, err := api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
			Limit: 100,
		})
		if err != nil {
			return fmt.Errorf("failed to get dialogs: %w", err)
		}

		// Process dialogs
		fmt.Println("Listing all channels/chats where bot is a member:")
		fmt.Println("---------------------------------------------")
		
		// Extract dialogs data
		dialogsData, ok := dialogs.(*tg.MessagesDialogs)
		if !ok {
			dialogsSlice, ok := dialogs.(*tg.MessagesDialogsSlice)
			if !ok {
				return fmt.Errorf("unexpected dialogs type: %T", dialogs)
			}
			dialogsData = &tg.MessagesDialogs{
				Dialogs: dialogsSlice.Dialogs,
				Messages: dialogsSlice.Messages,
				Chats: dialogsSlice.Chats,
				Users: dialogsSlice.Users,
			}
		}
		
		// Process chats
		for _, chat := range dialogsData.Chats {
			switch c := chat.(type) {
			case *tg.Channel:
				fmt.Printf("CHANNEL: %s (ID: %d, Username: @%s)\n", c.Title, c.ID, c.Username)
				fmt.Printf("  - Full ID: -100%d\n", c.ID)
				fmt.Printf("  - Creator: %v\n", c.Creator)
				fmt.Printf("  - Broadcast: %v\n", c.Broadcast)
				fmt.Printf("  - Verified: %v\n", c.Verified)
				fmt.Printf("  - Megagroup: %v\n", c.Megagroup)
				fmt.Printf("  - Restricted: %v\n", c.Restricted)
				fmt.Printf("  - AccessHash: %d\n", c.AccessHash)
				fmt.Println()
			case *tg.Chat:
				fmt.Printf("CHAT: %s (ID: %d)\n", c.Title, c.ID)
				fmt.Printf("  - Admin: %v\n", c.Creator)
				fmt.Printf("  - Deactivated: %v\n", c.Deactivated)
				fmt.Println()
			}
		}
		
		fmt.Println("---------------------------------------------")
		fmt.Println("IMPORTANT: Use the 'Full ID' value in your config.toml")
		
		return nil
	})

	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("ID check completed successfully")
}
