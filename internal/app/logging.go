package app

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

var logOnce sync.Once

// InitLogging mirrors slog output to %APPDATA%\XDL\logs\xdl.log so Alpha users
// (and the browser-integration diagnostics) can inspect what happened.
func InitLogging() {
	logOnce.Do(func() {
		dir := filepath.Join(DataDir(), "logs")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return
		}
		f, err := os.OpenFile(filepath.Join(dir, "xdl.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		w := io.MultiWriter(os.Stderr, f)
		slog.SetDefault(slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo})))
	})
}
