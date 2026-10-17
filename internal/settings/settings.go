// Package settings persists user-facing application preferences.
package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Settings is the durable configuration shared by the shell and the engine.
// Values are intentionally conservative by default (see Plan §9).
type Settings struct {
	DownloadDir      string `json:"downloadDir"`
	MaxConcurrent    int    `json:"maxConcurrent"`
	DefaultConns     int    `json:"defaultConns"`
	Turbo            bool   `json:"turbo"`
	GlobalLimitBps   int64  `json:"globalLimitBps"`
	LaunchAtLogin    bool   `json:"launchAtLogin"`
	StartMinimized   bool   `json:"startMinimized"`
	CloseToTray      bool   `json:"closeToTray"`
	Notifications    bool   `json:"notifications"`
	FirstRunDone     bool   `json:"firstRunDone"`
	Theme            string `json:"theme"`
	Language         string `json:"language"`
	FFmpegPath       string `json:"ffmpegPath"`
	BTLegalAccepted  bool   `json:"btLegalAccepted"`
	BTSeedAfterDone  bool   `json:"btSeedAfterDone"`
	BTUploadLimitBps int64  `json:"btUploadLimitBps"`
	BTMaxPeers       int    `json:"btMaxPeers"`
	// Auto-categorize downloads into named subfolders.
	AutoCategory bool           `json:"autoCategory"`
	Categories   []CategoryRule `json:"categories,omitempty"`
	// SchedulePause: when enabled, new work is held and running tasks paused
	// between PauseStartHour and PauseEndHour (local time, 0–23, end exclusive
	// unless equal → disabled window). Cross-midnight windows are supported.
	ScheduleEnabled    bool `json:"scheduleEnabled"`
	SchedulePauseStart int  `json:"schedulePauseStart"` // hour
	SchedulePauseEnd   int  `json:"schedulePauseEnd"`
	// UpdateManifestURL is the HTTPS JSON manifest for auto-update checks.
	UpdateManifestURL string `json:"updateManifestUrl,omitempty"`
	// Aria2 RPC lets existing aria2 browser extensions push tasks to XDL.
	// The listener is always 127.0.0.1. An empty secret accepts local callers
	// the same way a default aria2 install does.
	Aria2Enabled bool   `json:"aria2Enabled"`
	Aria2Port    int    `json:"aria2Port"`
	Aria2Secret  string `json:"aria2Secret"`
}

// Store is the subset of the engine store needed here.
type Store interface {
	GetSetting(key string) ([]byte, bool, error)
	PutSetting(key string, value []byte) error
}

const settingKey = "app"

// Defaults returns the initial settings rooted at dataDir.
func Defaults(dataDir string) Settings {
	return Settings{
		DownloadDir:        filepath.Join(dataDir, "Downloads"),
		MaxConcurrent:      3,
		DefaultConns:       8,
		Turbo:              false,
		CloseToTray:        true,
		Notifications:      true,
		Theme:              "light",
		Language:           "zh-CN",
		BTSeedAfterDone:    false,
		BTUploadLimitBps:   128 * 1024, // 128 KiB/s default upload cap
		BTMaxPeers:         50,
		AutoCategory:       true,
		Categories:         DefaultCategories(),
		ScheduleEnabled:    false,
		SchedulePauseStart: 0,
		SchedulePauseEnd:   0,
		Aria2Enabled:       true,
		Aria2Port:          6800,
	}
}

// Load reads settings, filling missing fields with defaults and repairing
// invalid values. A broken database value never blocks startup.
func Load(st Store, dataDir string) Settings {
	s := Defaults(dataDir)
	raw, ok, err := st.GetSetting(settingKey)
	if err == nil && ok {
		_ = json.Unmarshal(raw, &s)
	}
	Normalize(&s, dataDir)
	return s
}

// Save validates and persists settings.
func Save(st Store, s Settings, dataDir string) (Settings, error) {
	Normalize(&s, dataDir)
	if err := os.MkdirAll(s.DownloadDir, 0o755); err != nil {
		return s, fmt.Errorf("创建下载目录失败：%w", err)
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return s, err
	}
	if err := st.PutSetting(settingKey, raw); err != nil {
		return s, err
	}
	return s, nil
}

// Normalize clamps numeric ranges and repairs paths/strings in place.
func Normalize(s *Settings, dataDir string) {
	def := Defaults(dataDir)
	if strings.TrimSpace(s.DownloadDir) == "" {
		s.DownloadDir = def.DownloadDir
	} else if !filepath.IsAbs(s.DownloadDir) {
		s.DownloadDir = filepath.Join(dataDir, s.DownloadDir)
	}
	s.DownloadDir = filepath.Clean(s.DownloadDir)

	if s.MaxConcurrent < 1 || s.MaxConcurrent > 8 {
		s.MaxConcurrent = def.MaxConcurrent
	}
	if s.DefaultConns < 1 || s.DefaultConns > 32 {
		s.DefaultConns = def.DefaultConns
	}
	if s.GlobalLimitBps < 0 {
		s.GlobalLimitBps = 0
	}
	if s.GlobalLimitBps > 0 && s.GlobalLimitBps < 32*1024 {
		s.GlobalLimitBps = 32 * 1024
	}
	switch s.Theme {
	case "dark", "light", "system":
	default:
		s.Theme = "light"
	}
	switch s.Language {
	case "zh-CN", "en":
	default:
		s.Language = "zh-CN"
	}
	if s.BTMaxPeers < 10 || s.BTMaxPeers > 200 {
		s.BTMaxPeers = def.BTMaxPeers
	}
	if s.BTUploadLimitBps < 0 {
		s.BTUploadLimitBps = 0
	}
	if s.Categories == nil {
		s.Categories = DefaultCategories()
	}
	if s.SchedulePauseStart < 0 || s.SchedulePauseStart > 23 {
		s.SchedulePauseStart = 0
	}
	if s.SchedulePauseEnd < 0 || s.SchedulePauseEnd > 23 {
		s.SchedulePauseEnd = 0
	}
	s.UpdateManifestURL = strings.TrimSpace(s.UpdateManifestURL)
	s.Aria2Secret = strings.TrimSpace(s.Aria2Secret)
	if s.Aria2Port < 1 || s.Aria2Port > 65535 {
		s.Aria2Port = def.Aria2Port
	}
}

// InSchedulePause reports whether local now falls inside the configured pause window.
func (s Settings) InSchedulePause(now time.Time) bool {
	if !s.ScheduleEnabled {
		return false
	}
	start, end := s.SchedulePauseStart, s.SchedulePauseEnd
	if start == end {
		return false
	}
	h := now.Hour()
	if start < end {
		return h >= start && h < end
	}
	// Cross midnight, e.g. 22 → 7.
	return h >= start || h < end
}
