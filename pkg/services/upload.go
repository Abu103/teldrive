package services

import (
	"context"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"github.com/Abu103/teldrive/internal/api"
	"github.com/Abu103/teldrive/internal/auth"
	"github.com/Abu103/teldrive/internal/logging"
	"github.com/Abu103/teldrive/internal/tgc"
	"github.com/Abu103/teldrive/pkg/models"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

// Simple progress reader
type progressReader struct {
	reader   io.Reader
	progress func(int64)
	read     int64
}

func (r *progressReader) Read(p []byte) (n int, err error) {
	n, err = r.reader.Read(p)
	if n > 0 && r.progress != nil {
		r.read += int64(n)
		r.progress(r.read)
	}
	return
}

func (a *apiService) UploadsUpload(ctx context.Context, req *api.UploadsUploadReqWithContentType, params api.UploadsUploadParams) (*api.UploadPart, error) {
	logger := logging.FromContext(ctx)
	userId := auth.GetUser(ctx)
	fileStream := req.Content.Data
	fileSize := params.ContentLength

	// Create upload context with timeout
	uploadCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	// Get channel ID
	channelId := params.ChannelId.Value
	if channelId == 0 {
		var err error
		channelId, err = getDefaultChannel(a.db, a.cache, userId)
		if err != nil {
			return nil, fmt.Errorf("failed to get default channel: %w", err)
		}
	}

	// Create client
	client, err := tgc.AuthClient(uploadCtx, &a.cnf.TG, auth.GetJWTUser(ctx).TgSession)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	// Get API client
	apiClient := client.API()

	// Send initial status message
	statusMsg := fmt.Sprintf("⏳ Uploading %s...", params.FileName)
	statusMsgId, err := tgc.SendStatusMessage(uploadCtx, apiClient, channelId, statusMsg)
	if err != nil {
		logger.Warn("Failed to send status message", zap.Error(err))
	}

	// Track progress
	var uploaded int64
	pr := &progressReader{
		reader: fileStream,
		progress: func(bytes int64) {
			atomic.StoreInt64(&uploaded, bytes)
			if statusMsgId != 0 {
				progress := float64(bytes) / float64(fileSize) * 100
				progressMsg := fmt.Sprintf("⏳ Uploading %s... %.1f%%", params.FileName, progress)
				if err := tgc.UpdateStatusMessage(uploadCtx, apiClient, channelId, statusMsgId, progressMsg); err != nil {
					logger.Warn("Failed to update progress", zap.Error(err))
				}
			}
		},
	}

	// Upload file
	sender := message.NewSender(apiClient)
	channel, err := tgc.GetChannelById(uploadCtx, apiClient, channelId)
	if err != nil {
		return nil, fmt.Errorf("failed to get channel: %w", err)
	}

	// Send file
	res, err := sender.To(&tg.InputPeerChannel{
		ChannelID:  channel.ChannelID,
		AccessHash: channel.AccessHash,
	}).Upload(uploadCtx, message.UploadFromReader(params.FileName, pr, fileSize)).Send()

	if err != nil {
		if statusMsgId != 0 {
			_ = tgc.DeleteStatusMessage(uploadCtx, apiClient, channelId, statusMsgId)
		}
		return nil, fmt.Errorf("failed to upload file: %w", err)
	}

	// Get message ID
	var messageId int
	if updates, ok := res.(*tg.Updates); ok {
		for _, update := range updates.Updates {
			if msg, ok := update.(*tg.UpdateNewChannelMessage); ok {
				if m, ok := msg.Message.(*tg.Message); ok {
					messageId = m.ID
					break
				}
			}
		}
	}

	if messageId == 0 {
		return nil, fmt.Errorf("failed to get message ID")
	}

	// Save to database
	upload := &models.Upload{
		Name:      params.FileName,
		UploadId:  params.ID,
		PartId:    messageId,
		ChannelId: channelId,
		Size:      fileSize,
		PartNo:    int(params.PartNo),
		UserId:    userId,
	}

	if err := a.db.Create(upload).Error; err != nil {
		return nil, fmt.Errorf("failed to save upload: %w", err)
	}

	// Delete status message
	if statusMsgId != 0 {
		_ = tgc.DeleteStatusMessage(uploadCtx, apiClient, channelId, statusMsgId)
	}

	return &api.UploadPart{
		Name:      upload.Name,
		PartId:    upload.PartId,
		ChannelId: upload.ChannelId,
		PartNo:    upload.PartNo,
		Size:      upload.Size,
	}, nil
} 