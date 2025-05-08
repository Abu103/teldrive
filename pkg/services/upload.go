package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

// Create a progress tracker with better state management
type progressTracker struct {
	uploaded     int64
	total        int64
	lastProgress float64
	lastUpdate   time.Time
	minInterval  time.Duration
	minChange    float64
	mu           sync.Mutex
}

func newProgressTracker(total int64) *progressTracker {
	return &progressTracker{
		total:        total,
		minInterval:  5 * time.Second,  // Minimum time between updates
		minChange:    1.0,              // Minimum progress change percentage
		lastUpdate:   time.Now(),
	}
}

func (pt *progressTracker) update(bytes int64) (bool, float64) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	
	pt.uploaded = bytes
	progress := float64(bytes) / float64(pt.total) * 100
	
	// Check if enough time has passed and progress has changed significantly
	now := time.Now()
	if now.Sub(pt.lastUpdate) >= pt.minInterval && 
	   progress-pt.lastProgress >= pt.minChange {
		pt.lastProgress = progress
		pt.lastUpdate = now
		return true, progress
	}
	return false, progress
}

func (a *apiService) UploadsUpload(ctx context.Context, req *api.UploadsUploadReqWithContentType, params api.UploadsUploadParams) (*api.UploadPart, error) {
	var (
		channelId   int64
		err         error
		client      *telegram.Client
		apiClient   *tg.Client
		token       string
		index       int
		channelUser string
		out         api.UploadPart
		uploaded    int64
	)

	logger := logging.FromContext(ctx)

	if params.Encrypted.Value && a.cnf.TG.Uploads.EncryptionKey == "" {
		return nil, &apiError{err: errors.New("encryption is not enabled"), code: 400}
	}

	userId := auth.GetUser(ctx)
	fileStream := req.Content.Data
	fileSize := params.ContentLength

	// Create a context with timeout for the entire upload process
	uploadCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	if params.ChannelId.Value == 0 {
		channelId, err = getDefaultChannel(a.db, a.cache, userId)
		if err != nil {
			return nil, err
		}
	} else {
		channelId = params.ChannelId.Value
	}

	logger.Info("Starting upload process",
		zap.String("fileName", params.FileName),
		zap.String("partName", params.PartName),
		zap.Int64("fileSize", fileSize),
		zap.Int("partNo", params.PartNo))

	tokens, err := getBotsToken(a.db, a.cache, userId, channelId)
	if err != nil {
		return nil, err
	}

	if len(tokens) == 0 {
		client, err = tgc.AuthClient(uploadCtx, &a.cnf.TG, auth.GetJWTUser(ctx).TgSession)
		if err != nil {
			return nil, err
		}
		channelUser = strconv.FormatInt(userId, 10)
	} else {
		a.worker.Set(tokens, channelId)
		token, index = a.worker.Next(channelId)
		client, err = tgc.BotClient(uploadCtx, a.tgdb, &a.cnf.TG, token)
		if err != nil {
			return nil, err
		}
		channelUser = strings.Split(token, ":")[0]
	}

	// Create a new client pool with a smaller size for uploads
	uploadPool := pool.NewPool(client, int64(1), middlewares...)
	defer uploadPool.Close()

	// Get the API client once at the start
	apiClient = client.API()

	channel, err := tgc.GetChannelById(uploadCtx, apiClient, channelId)
	if err != nil {
		logger.Error("Failed to get channel", zap.Error(err))
		return nil, err
	}

	// Send initial status message
	statusMsg := fmt.Sprintf("⏳ Uploading part %d of %s...", params.PartNo, params.FileName)
	statusMsgId, err := tgc.SendStatusMessage(uploadCtx, apiClient, channelId, statusMsg)
	if err != nil {
		logger.Warn("Failed to send status message", zap.Error(err))
	} else {
		logger.Info("Sent initial status message", zap.Int("messageId", statusMsgId))
	}

	var salt string
	if params.Encrypted.Value {
		logger.Info("Encrypting file")
		salt, _ = generateRandomSalt()
		cipher, err := crypt.NewCipher(a.cnf.TG.Uploads.EncryptionKey, salt)
		if err != nil {
			return nil, err
		}
		fileSize = crypt.EncryptedSize(fileSize)
		fileStream, err = cipher.EncryptData(fileStream)
		if err != nil {
			return nil, err
		}
	}

	// Create a progress tracker with more frequent updates
	tracker := &progressTracker{
		total:        fileSize,
		minInterval:  2 * time.Second,  // Update every 2 seconds
		minChange:    0.5,              // Update on 0.5% change
		lastUpdate:   time.Now(),
	}

	// Create a progress reader with better error handling
	pr := &progressReader{
		reader:   fileStream,
		progress: func(bytes int64) {
			if shouldUpdate, progress := tracker.update(bytes); shouldUpdate {
				atomic.StoreInt64(&uploaded, bytes)
				if statusMsgId != 0 {
					progressMsg := fmt.Sprintf("⏳ Uploading part %d of %s... %.1f%%", 
						params.PartNo, params.FileName, progress)
					if err := tgc.UpdateStatusMessage(uploadCtx, apiClient, channelId, statusMsgId, progressMsg); err != nil {
						if !strings.Contains(err.Error(), "MESSAGE_NOT_MODIFIED") {
							logger.Warn("Failed to update progress message", zap.Error(err))
						}
					}
				}
			}
		},
	}

	// Add retry logic for the upload with better error handling
	var upload tg.InputFileClass
	var uploadErr error
	maxRetries := 3
	for retry := 0; retry < maxRetries; retry++ {
		if retry > 0 {
			logger.Info("Retrying upload", 
				zap.String("fileName", params.FileName),
				zap.String("partName", params.PartName),
				zap.Int("partNo", params.PartNo),
				zap.Int("retry", retry))
			
			// Reset the progress reader for retry
			pr = &progressReader{
				reader:   fileStream,
				progress: func(bytes int64) {
					atomic.StoreInt64(&uploaded, bytes)
				},
			}
		}

		// Get a dedicated client for this upload with reconnection
		var uploadClient *tg.Client
		for retries := 0; retries < 3; retries++ {
			uploadClient = uploadPool.Default(uploadCtx)
			if uploadClient != nil {
				break
			}
			logger.Warn("Failed to get upload client, retrying...", zap.Int("retry", retries))
			time.Sleep(time.Duration(retries+1) * 2 * time.Second)
		}
		if uploadClient == nil {
			return nil, fmt.Errorf("failed to get upload client after retries")
		}

		// Use a smaller part size for better reliability
		partSize := 131072 // 128KB

		logger.Info("Starting upload with client", 
			zap.String("bot", channelUser),
			zap.Int("botNo", index),
			zap.Int("threads", a.cnf.TG.Uploads.Threads),
			zap.Int("partSize", partSize))

		u := uploader.NewUploader(uploadClient).
			WithThreads(a.cnf.TG.Uploads.Threads).
			WithPartSize(partSize)

		upload, uploadErr = u.Upload(uploadCtx, uploader.NewUpload(params.PartName, pr, fileSize))
		if uploadErr == nil {
			break
		}

		if strings.Contains(uploadErr.Error(), "engine was closed") ||
		   strings.Contains(uploadErr.Error(), "connection dead") {
			logger.Warn("Connection issue detected, attempting to reconnect...")
			time.Sleep(5 * time.Second)
			continue
		}

		logger.Error("Upload attempt failed", 
			zap.String("fileName", params.FileName),
			zap.String("partName", params.PartName),
			zap.Int("partNo", params.PartNo),
			zap.Int("retry", retry),
			zap.Error(uploadErr))

		if retry < maxRetries-1 {
			time.Sleep(time.Duration(retry+1) * 5 * time.Second)
		}
	}

	if uploadErr != nil {
		logger.Error("All upload attempts failed", 
			zap.String("fileName", params.FileName),
			zap.String("partName", params.PartName),
			zap.Int("partNo", params.PartNo),
			zap.Error(uploadErr))
		
		if statusMsgId != 0 {
			if delErr := tgc.DeleteStatusMessage(uploadCtx, apiClient, channelId, statusMsgId); delErr != nil {
				logger.Warn("Failed to delete status message after error", zap.Error(delErr))
			}
		}
		return nil, uploadErr
	}

	// Update final progress
	atomic.StoreInt64(&uploaded, fileSize)
	progress := float64(uploaded) / float64(fileSize) * 100
	progressMsg := fmt.Sprintf("⏳ Uploading part %d of %s... %.1f%%", 
		params.PartNo, params.FileName, progress)
	if err := tgc.UpdateStatusMessage(uploadCtx, apiClient, channelId, statusMsgId, progressMsg); err != nil {
		logger.Warn("Failed to update final progress message", zap.Error(err))
	}

	logger.Info("File uploaded successfully, sending to channel")

	// Send to channel with retry logic
	document := message.UploadedDocument(upload).Filename(params.PartName).ForceFile(true)
	sender := message.NewSender(apiClient)
	target := sender.To(&tg.InputPeerChannel{
		ChannelID:  channel.ChannelID,
		AccessHash: channel.AccessHash,
	})

	var res tg.UpdatesClass
	var sendErr error
	for retry := 0; retry < maxRetries; retry++ {
		if retry > 0 {
			logger.Info("Retrying send to channel", 
				zap.String("fileName", params.FileName),
				zap.String("partName", params.PartName),
				zap.Int("partNo", params.PartNo),
				zap.Int("retry", retry))
		}

		res, sendErr = target.Media(uploadCtx, document)
		if sendErr == nil {
			break
		}

		logger.Error("Send to channel attempt failed", 
			zap.String("fileName", params.FileName),
			zap.String("partName", params.PartName),
			zap.Int("partNo", params.PartNo),
			zap.Int("retry", retry),
			zap.Error(sendErr))

		if retry < maxRetries-1 {
			time.Sleep(time.Duration(retry+1) * 5 * time.Second)
		}
	}

	if sendErr != nil {
		logger.Error("All send to channel attempts failed", 
			zap.String("fileName", params.FileName),
			zap.String("partName", params.PartName),
			zap.Int("partNo", params.PartNo),
			zap.Error(sendErr))
		
		if statusMsgId != 0 {
			if delErr := tgc.DeleteStatusMessage(uploadCtx, apiClient, channelId, statusMsgId); delErr != nil {
				logger.Warn("Failed to delete status message after error", zap.Error(delErr))
			}
		}
		return nil, sendErr
	}

	logger.Info("Media sent to channel successfully")

	// Process the response
	updates, ok := res.(*tg.Updates)
	if !ok {
		return nil, fmt.Errorf("unexpected response type: %T", res)
	}

	var message *tg.Message
	for _, update := range updates.Updates {
		channelMsg, ok := update.(*tg.UpdateNewChannelMessage)
		if ok {
			message = channelMsg.Message.(*tg.Message)
			break
		}
	}

	if message.ID == 0 {
		return nil, fmt.Errorf("upload failed")
	}

	// Save upload information
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
		return nil, err
	}

	// Verify the upload
	v, err := apiClient.ChannelsGetMessages(uploadCtx, &tg.ChannelsGetMessagesRequest{
		Channel: channel,
		ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: message.ID}},
	})

	if err != nil || v == nil {
		return nil, ErrUploadFailed
	}

	switch msgs := v.(type) {
	case *tg.MessagesChannelMessages:
		if len(msgs.Messages) == 0 {
			return nil, ErrUploadFailed
		}
		doc, ok := msgDocument(msgs.Messages[0])
		if !ok {
			return nil, ErrUploadFailed
		}
		if doc.Size != fileSize {
			apiClient.ChannelsDeleteMessages(uploadCtx, &tg.ChannelsDeleteMessagesRequest{
				Channel: channel,
				ID:      []int{message.ID},
			})
			return nil, ErrUploadFailed
		}
	default:
		return nil, ErrUploadFailed
	}

	// Delete status message on success
	if statusMsgId != 0 {
		if err := tgc.DeleteStatusMessage(uploadCtx, apiClient, channelId, statusMsgId); err != nil {
			logger.Warn("Failed to delete status message after success", zap.Error(err))
		}
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
	
	logger.Info("Upload completed successfully",
		zap.String("fileName", params.FileName),
		zap.String("partName", params.PartName),
		zap.Int("partNo", params.PartNo),
		zap.Int64("size", fileSize))
		
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
