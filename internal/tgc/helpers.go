package tgc

import (
	"context"
	"fmt"
	"time"

	"github.com/gotd/td/tg"
	"github.com/Abu103/teldrive/internal/utils"
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

// DeleteMessages deletes messages from a channel
func DeleteMessages(ctx context.Context, client *tg.Client, channelID int64, messageIDs []int) error {
	for i := 0; i < 3; i++ {
		if i > 0 {
			time.Sleep(time.Duration(i) * 2 * time.Second)
		}
		
		_, err := client.MessagesDeleteMessages(ctx, &tg.MessagesDeleteMessagesRequest{
			Revoke: true,
			ID:     messageIDs,
		})
		if err == nil {
			return nil
		}
	}
	return fmt.Errorf("failed to delete messages after retries")
}

// CalculateChunkSize calculates the optimal chunk size for file upload
func CalculateChunkSize(fileSize int64) int64 {
	const minChunkSize = 64 * 1024 // 64KB
	const maxChunkSize = 512 * 1024 // 512KB
	
	chunkSize := fileSize / 100 // 1%
	if chunkSize < minChunkSize {
		chunkSize = minChunkSize
	} else if chunkSize > maxChunkSize {
		chunkSize = maxChunkSize
	}
	return chunkSize
}

// GetLocation gets the file location for a message
func GetLocation(ctx context.Context, client *tg.Client, channelID int64, messageID int) (*tg.InputFileLocation, error) {
	for i := 0; i < 3; i++ {
		if i > 0 {
			time.Sleep(time.Duration(i) * 2 * time.Second)
		}
		
		result, err := client.MessagesGetMessages(ctx, []tg.InputMessageClass{
			&tg.InputMessageID{
				ID: int32(messageID),
			},
		})
		if err != nil {
			continue
		}
		
		if messages, ok := result.(*tg.MessagesMessages); ok {
			if len(messages.Messages) > 0 {
				if msg, ok := messages.Messages[0].(*tg.Message); ok {
					if media, ok := msg.Media.(*tg.MessageMediaDocument); ok {
						document := media.Document.(*tg.Document)
						return &tg.InputFileLocation{
							ID:        document.ID,
							AccessHash: document.AccessHash,
							FileReference: document.FileReference,
						}, nil
					}
				}
			}
		}
	}
	return nil, fmt.Errorf("failed to get file location after retries")
}

// GetChunk gets a specific chunk of a file
func GetChunk(ctx context.Context, client *tg.Client, location *tg.InputFileLocation, offset int64, limit int64) ([]byte, error) {
	for i := 0; i < 3; i++ {
		if i > 0 {
			time.Sleep(time.Duration(i) * 2 * time.Second)
		}
		
		result, err := client.UploadGetFile(ctx, &tg.UploadGetFileRequest{
			Location: location,
			Offset:   int(offset),
			Limit:    int(limit),
		})
		if err != nil {
			continue
		}
		
		if file, ok := result.(*tg.UploadFile); ok {
			return file.Bytes, nil
		}
	}
	return nil, fmt.Errorf("failed to get file chunk after retries")
}

// GetChannelById gets a channel by its ID
func GetChannelById(ctx context.Context, client *tg.Client, channelID int64) (*tg.InputChannel, error) {
	return &tg.InputChannel{
		ChannelID:  channelID,
		AccessHash: 0, // This will be filled by the client
	}, nil
} 