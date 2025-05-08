package uploader

import (
	"context"
	"io"

	"github.com/your-project/tg"
)

type Upload struct {
	Name     string
	Stream   io.Reader
	Size     int64
	Progress func(int64)
}

func NewUpload(name string, stream io.Reader, size int64, progress func(int64)) *Upload {
	return &Upload{
		Name:     name,
		Stream:   stream,
		Size:     size,
		Progress: progress,
	}
}

func (u *Uploader) Upload(ctx context.Context, upload *Upload) (*tg.InputFile, error) {
	// ... existing code ...
	
	// Create a progress reader
	progressReader := &progressReader{
		reader:   upload.Stream,
		progress: upload.Progress,
	}

	// Use the progress reader for upload
	// ... rest of upload code using progressReader instead of upload.Stream ...
}

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