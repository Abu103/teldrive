package tgc

import (
	"context"
	"fmt"
	"time"

	"github.com/gotd/td/tg"
)

// SendStatusMessage sends a status message to a channel with retries
func SendStatusMessage(ctx context.Context, client *tg.Client, channelID int64, text string) (int, error) {
	var msgID int
	var err error
	
	for i := 0; i < 3; i++ {
		if i > 0 {
			time.Sleep(time.Duration(i) * 2 * time.Second)
		}
		
		channel, err := GetChannelById(ctx, client, channelID)
		if err != nil {
			continue
		}
		
		result, err := client.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
			Peer:    &tg.InputPeerChannel{ChannelID: channel.ChannelID, AccessHash: channel.AccessHash},
			Message: text,
		})
		if err == nil {
			if updates, ok := result.(*tg.Updates); ok {
				for _, update := range updates.Updates {
					if msg, ok := update.(*tg.UpdateNewChannelMessage); ok {
						if m, ok := msg.Message.(*tg.Message); ok {
							msgID = m.ID
							return msgID, nil
						}
					}
				}
			}
			return msgID, nil
		}
	}
	return 0, fmt.Errorf("failed to send status message after retries: %w", err)
}

// UpdateStatusMessage updates a status message with retries
func UpdateStatusMessage(ctx context.Context, client *tg.Client, channelID int64, messageID int, text string) error {
	for i := 0; i < 3; i++ {
		if i > 0 {
			time.Sleep(time.Duration(i) * 2 * time.Second)
		}
		
		channel, err := GetChannelById(ctx, client, channelID)
		if err != nil {
			continue
		}
		
		_, err = client.MessagesEditMessage(ctx, &tg.MessagesEditMessageRequest{
			Peer:    &tg.InputPeerChannel{ChannelID: channel.ChannelID, AccessHash: channel.AccessHash},
			ID:      messageID,
			Message: text,
		})
		
		if err == nil {
			return nil
		}
	}
	return fmt.Errorf("failed to update status message after retries")
}

// DeleteStatusMessage deletes a status message with retries
func DeleteStatusMessage(ctx context.Context, client *tg.Client, channelID int64, messageID int) error {
	for i := 0; i < 3; i++ {
		if i > 0 {
			time.Sleep(time.Duration(i) * 2 * time.Second)
		}
		
		channel, err := GetChannelById(ctx, client, channelID)
		if err != nil {
			continue
		}
		
		_, err = client.ChannelsDeleteMessages(ctx, &tg.ChannelsDeleteMessagesRequest{
			Channel: channel,
			ID:      []int{messageID},
		})
		
		if err == nil {
			return nil
		}
	}
	return fmt.Errorf("failed to delete status message after retries")
}

// GetChannelById gets a channel by its ID
func GetChannelById(ctx context.Context, client *tg.Client, channelID int64) (*tg.InputChannel, error) {
	return &tg.InputChannel{
		ChannelID:  channelID,
		AccessHash: 0, // This will be filled by the client
	}, nil
} 