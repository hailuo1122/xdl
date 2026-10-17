package engine

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/xdl-app/xdl/internal/media/job"
)

// MediaDownloader adapts media/job into the task manager.
type MediaDownloader struct {
	inner *job.Downloader
}

func NewMediaDownloader() *MediaDownloader {
	return &MediaDownloader{inner: job.NewDownloader()}
}

func (d *MediaDownloader) Download(ctx context.Context, opt MediaRunOptions) (*MediaRunResult, error) {
	meta, _ := job.DecodeMeta(opt.KindMeta)
	res, err := d.inner.Download(ctx, job.Options{
		URL:        opt.URL,
		Dir:        opt.Dir,
		Name:       opt.Name,
		Headers:    opt.Headers,
		Meta:       meta,
		Conns:      opt.Conns,
		FFmpegPath: opt.FFmpegPath,
		GlobalLim:  opt.GlobalLim,
		FinalPath:  opt.FinalPath,
		OnProgress: opt.OnProgress,
		OnMeta: func(m job.Meta) error {
			if opt.OnMeta == nil {
				return nil
			}
			raw, err := job.EncodeMeta(m)
			if err != nil {
				return err
			}
			return opt.OnMeta(raw)
		},
	})
	if err != nil {
		return nil, err
	}
	return &MediaRunResult{
		Path:     res.Path,
		Size:     res.Size,
		FinalURL: res.FinalURL,
		Name:     res.Name,
	}, nil
}

type MediaRunOptions struct {
	URL        string
	Dir        string
	Name       string
	Headers    http.Header
	KindMeta   json.RawMessage
	Conns      int
	FFmpegPath string
	GlobalLim  *Limiter
	FinalPath  string
	OnProgress func(done, total, speed int64)
	OnMeta     func(json.RawMessage) error
}

type MediaRunResult struct {
	Path     string
	Size     int64
	FinalURL string
	Name     string
}
