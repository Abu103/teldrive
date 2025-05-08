package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Abu103/teldrive/internal/api"
	"github.com/Abu103/teldrive/internal/auth"
	"github.com/Abu103/teldrive/internal/crypt"
	"github.com/Abu103/teldrive/internal/logging"
	"github.com/Abu103/teldrive/internal/pool"
	"github.com/Abu103/teldrive/internal/tgc"
	"go.uber.org/zap"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/Abu103/teldrive/pkg/mapper"
	"github.com/Abu103/teldrive/pkg/models"
)

var (
	saltLength      = 32
	ErrUploadFailed = errors.New("upload failed")
)

func (a *apiService) UploadsDelete(ctx context.Context, params api.UploadsDeleteParams) error {
	if err := a.db.Where("upload_id = ?", params.ID).Delete(&models.Upload{}).Error; err != nil {
		return &api.ErrorStatusCode{StatusCode: 500, Response: api.Error{Message: err.Error(), Code: 500}}
	}
	return nil
}

func (a *apiService) UploadsPartsById(ctx context.Context, params api.UploadsPartsByIdParams) ([]api.UploadPart, error) {
	parts := []models.Upload{}
	if err := a.db.Model(&models.Upload{}).Order("part_no").Where("upload_id = ?", params.ID).
		Where("created_at < ?", time.Now().UTC().Add(a.cnf.TG.Uploads.Retention)).
		Find(&parts).Error; err != nil {
		return nil, &apiError{err: err}
	}
	return mapper.ToUploadOut(parts), nil
}

func (a *apiService) UploadsStats(ctx context.Context, params api.UploadsStatsParams) ([]api.UploadStats, error) {
	userId := auth.GetUser(ctx)
	var stats []api.UploadStats
	err := a.db.Raw(`
    SELECT 
    dates.upload_date::date AS upload_date,
    COALESCE(SUM(files.size), 0)::bigint AS total_uploaded
    FROM 
        generate_series(
            (CURRENT_TIMESTAMP AT TIME ZONE 'UTC')::date - INTERVAL '1 day' * @days,
            (CURRENT_TIMESTAMP AT TIME ZONE 'UTC')::date,
            '1 day'
        ) AS dates(upload_date)
    LEFT JOIN 
    teldrive.files AS files
    ON 
        dates.upload_date = DATE_TRUNC('day', files.created_at)::date
        AND files.type = 'file'
        AND files.user_id = @userId
    GROUP BY 
        dates.upload_date
    ORDER BY 
        dates.upload_date
  `, sql.Named("days", params.Days-1), sql.Named("userId", userId)).Scan(&stats).Error

	if err != nil {
		return nil, &apiError{err: err}

	}
	return stats, nil
}

func (a *apiService) UploadsUpload(ctx context.Context, req *api.UploadsUploadReqWithContentType, params api.UploadsUploadParams) (*api.UploadPart, error) {
	var (
		channelId   int64
		err         error
		client      *telegram.Client
		token       string
		index       int
		channelUser string
		out         api.UploadPart
	)

	if params.Encrypted.Value && a.cnf.TG.Uploads.EncryptionKey == "" {
		return nil, &apiError{err: errors.New("encryption is not enabled"), code: 400}
	}

	userId := auth.GetUser(ctx)

	fileStream := req.Content.Data

	fileSize := params.ContentLength

	if params.ChannelId.Value == 0 {
		channelId, err = getDefaultChannel(a.db, a.cache, userId)
		if err != nil {
			return nil, err
		}
	} else {
		channelId = params.ChannelId.Value
	}

	tokens, err := getBotsToken(a.db, a.cache, userId, channelId)

	if err != nil {
		return nil, err
	}

	if len(tokens) == 0 {
		client, err = tgc.AuthClient(ctx, &a.cnf.TG, auth.GetJWTUser(ctx).TgSession)
		if err != nil {
			return nil, err
		}
		channelUser = strconv.FormatInt(userId, 10)
	} else {
		a.worker.Set(tokens, channelId)
		token, index = a.worker.Next(channelId)
		client, err = tgc.BotClient(ctx, a.tgdb, &a.cnf.TG, token)

		if err != nil {
			return nil, err
		}

		channelUser = strings.Split(token, ":")[0]
	}

	middlewares := tgc.NewMiddleware(&a.cnf.TG, tgc.WithFloodWait(),
		tgc.WithRecovery(ctx),
		tgc.WithRetry(a.cnf.TG.Uploads.MaxRetries),
		tgc.WithRateLimit())

	uploadPool := pool.NewPool(client, int64(a.cnf.TG.PoolSize), middlewares...)

	defer uploadPool.Close()

	logger := logging.FromContext(ctx)

	logger.Info("Starting upload process",
		zap.String("fileName", params.FileName),
		zap.String("partName", params.PartName),
		zap.String("bot", channelUser),
		zap.Int("botNo", index),
		zap.Int("chunkNo", params.PartNo),
		zap.Int64("fileSize", fileSize),
		zap.Int64("partSize", int64(2 * 1024 * 1024)))

	err = tgc.RunWithAuth(ctx, client, token, func(ctx context.Context) error {

		// Get the API client once at the start
		apiClient := client.API()

		channel, err := tgc.GetChannelById(ctx, apiClient, channelId)

		if err != nil {
			logger.Error("Failed to get channel", zap.Error(err))
			return err
		}

		logger.Info("Got channel successfully", zap.Int64("channelId", channelId))

		// Send initial status message
		statusMsg := fmt.Sprintf("⏳ Uploading part %d of %s...", params.PartNo, params.FileName)
		statusMsgId, err := tgc.SendStatusMessage(ctx, apiClient, channelId, statusMsg)
		if err != nil {
			logger.Warn("Failed to send status message", zap.Error(err))
			// Don't return error, continue with upload
		} else {
			logger.Info("Sent initial status message", zap.Int("messageId", statusMsgId))
		}

		var salt string

		if params.Encrypted.Value {
			logger.Info("Encrypting file")
			salt, _ = generateRandomSalt()
			cipher, err := crypt.NewCipher(a.cnf.TG.Uploads.EncryptionKey, salt)
			if err != nil {
				logger.Error("Failed to create cipher", zap.Error(err))
				return err
			}
			fileSize = crypt.EncryptedSize(fileSize)
			fileStream, err = cipher.EncryptData(fileStream)
			if err != nil {
				logger.Error("Failed to encrypt data", zap.Error(err))
				return err
			}
			logger.Info("File encrypted successfully")
		}

		client := uploadPool.Default(ctx)

		// Use a fixed part size of 512KB (524288 bytes)
		// This is the minimum size required by Telegram's API and is guaranteed to work
		partSize := 524288 // 512KB

		logger.Info("Creating uploader", 
			zap.Int("threads", a.cnf.TG.Uploads.Threads),
			zap.Int("partSize", partSize),
			zap.Int64("fileSize", fileSize))

		u := uploader.NewUploader(client).
			WithThreads(a.cnf.TG.Uploads.Threads).
			WithPartSize(partSize)

		// Create a progress tracker
		var uploaded int64
		total := fileSize

		// Create a context for progress updates
		progressCtx, progressCancel := context.WithCancel(ctx)
		defer progressCancel()

		// Update progress periodically
		go func() {
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()

			for {
				select {
				case <-progressCtx.Done():
					return
				case <-ticker.C:
					if statusMsgId != 0 {
						progress := float64(uploaded) / float64(total) * 100
						progressMsg := fmt.Sprintf("⏳ Uploading part %d of %s... %.1f%%", 
							params.PartNo, params.FileName, progress)
						if _, err := tgc.SendStatusMessage(ctx, apiClient, channelId, progressMsg); err != nil {
							logger.Warn("Failed to update progress message", zap.Error(err))
						} else {
							logger.Debug("Updated progress", zap.Float64("progress", progress))
						}
					}
				}
			}
		}()

		logger.Info("Starting file upload")
		upload, err := u.Upload(ctx, uploader.NewUpload(params.PartName, fileStream, fileSize))

		// Delete status message after upload completes or fails
		if statusMsgId != 0 {
			defer func() {
				if err := tgc.DeleteStatusMessage(ctx, client, channelId, statusMsgId); err != nil {
					logger.Warn("Failed to delete status message", zap.Error(err))
				} else {
					logger.Info("Deleted status message")
				}
			}()
		}

		if err != nil {
			logger.Error("Upload failed", 
				zap.String("fileName", params.FileName),
				zap.String("partName", params.PartName),
				zap.Int("chunkNo", params.PartNo),
				zap.Error(err))
			return err
		}

		logger.Info("File uploaded successfully, sending to channel")

		document := message.UploadedDocument(upload).Filename(params.PartName).ForceFile(true)

		sender := message.NewSender(client)

		target := sender.To(&tg.InputPeerChannel{ChannelID: channel.ChannelID,
			AccessHash: channel.AccessHash})

		res, err := target.Media(ctx, document)

		if err != nil {
			logger.Error("Failed to send media to channel", zap.Error(err))
			return err
		}

		logger.Info("Media sent to channel successfully")

		updates := res.(*tg.Updates)

		var message *tg.Message

		for _, update := range updates.Updates {
			channelMsg, ok := update.(*tg.UpdateNewChannelMessage)
			if ok {
				message = channelMsg.Message.(*tg.Message)
				break
			}
		}

		if message.ID == 0 {
			return fmt.Errorf("upload failed")
		}

		partUpload := &models.Upload{
			Name:      params.PartName,
			UploadId:  params.ID,
			PartId:    message.ID,
			ChannelId: channelId,
			Size:      fileSize,
			PartNo:    int(params.PartNo),
			UserId:    userId,
			Encrypted: params.Encrypted.Value,
			Salt:      salt,
		}

		if err := a.db.Create(partUpload).Error; err != nil {
			return err
		}

		v, err := client.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{Channel: channel, ID: []tg.InputMessageClass{&tg.InputMessageID{ID: message.ID}}})

		if err != nil || v == nil {
			return ErrUploadFailed
		}

		switch msgs := v.(type) {
		case *tg.MessagesChannelMessages:
			if len(msgs.Messages) == 0 {
				return ErrUploadFailed
			}
			doc, ok := msgDocument(msgs.Messages[0])
			if !ok {
				return ErrUploadFailed
			}
			if doc.Size != fileSize {
				client.ChannelsDeleteMessages(ctx, &tg.ChannelsDeleteMessagesRequest{Channel: channel, ID: []int{message.ID}})
				return ErrUploadFailed
			}
		default:
			return ErrUploadFailed
		}

		out = api.UploadPart{
			Name:      partUpload.Name,
			PartId:    partUpload.PartId,
			ChannelId: partUpload.ChannelId,
			PartNo:    partUpload.PartNo,
			Size:      partUpload.Size,
			Encrypted: partUpload.Encrypted,
		}
		out.SetSalt(api.NewOptString(partUpload.Salt))
		return nil

	})

	if err != nil {
		logger.Error("upload failed", zap.String("fileName", params.FileName),
			zap.String("partName", params.PartName),
			zap.Int("chunkNo", params.PartNo))
		return nil, err
	}
	logger.Debug("upload finished", zap.String("fileName", params.FileName),
		zap.String("partName", params.PartName),
		zap.Int("chunkNo", params.PartNo))
	return &out, nil

}

func msgDocument(m tg.MessageClass) (*tg.Document, bool) {
	res, ok := m.AsNotEmpty()
	if !ok {
		return nil, false
	}
	msg, ok := res.(*tg.Message)
	if !ok {
		return nil, false
	}

	media, ok := msg.Media.(*tg.MessageMediaDocument)
	if !ok {
		return nil, false
	}
	return media.Document.AsNotEmpty()
}

func generateRandomSalt() (string, error) {
	randomBytes := make([]byte, saltLength)
	_, err := rand.Read(randomBytes)
	if err != nil {
		return "", err
	}

	hasher := sha256.New()
	hasher.Write(randomBytes)
	hashedSalt := base64.URLEncoding.EncodeToString(hasher.Sum(nil))

	return hashedSalt, nil
}
