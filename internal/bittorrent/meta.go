package bittorrent

import (
	"encoding/json"
	"strings"
)

// Meta is persisted in engine.Task.KindMeta for torrent tasks.
type Meta struct {
	InfoHash    string  `json:"infoHash"`
	Magnet      string  `json:"magnet,omitempty"`
	TorrentPath string  `json:"torrentPath,omitempty"`
	Name        string  `json:"name,omitempty"`
	Selected    []int   `json:"selected,omitempty"` // empty = all files
	SeedAfter   bool    `json:"seedAfter"`
	DataDir     string  `json:"dataDir,omitempty"`
	Peers       int     `json:"peers,omitempty"`
	Seeds       int     `json:"seeds,omitempty"`
	Uploaded    int64   `json:"uploaded,omitempty"`
	Downloaded  int64   `json:"downloaded,omitempty"`
	Ratio       float64 `json:"ratio,omitempty"`
}

func EncodeMeta(m Meta) (json.RawMessage, error) {
	return json.Marshal(m)
}

func DecodeMeta(raw json.RawMessage) Meta {
	var m Meta
	if len(raw) == 0 {
		return m
	}
	_ = json.Unmarshal(raw, &m)
	return m
}

// FileInfo describes one file inside a torrent for the selection UI.
type FileInfo struct {
	Index  int    `json:"index"`
	Path   string `json:"path"`
	Length int64  `json:"length"`
}

// ProbeResult is returned by magnet/.torrent metadata resolution.
type ProbeResult struct {
	InfoHash  string     `json:"infoHash"`
	Name      string     `json:"name"`
	TotalSize int64      `json:"totalSize"`
	Files     []FileInfo `json:"files"`
	Magnet    string     `json:"magnet,omitempty"`
	Source    string     `json:"source,omitempty"` // magnet | file
}

// IsMagnet reports whether s looks like a magnet URI.
func IsMagnet(s string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(s)), "magnet:")
}

// IsTorrentPath reports whether s looks like a local .torrent path.
func IsTorrentPath(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	low := strings.ToLower(s)
	return strings.HasSuffix(low, ".torrent")
}
