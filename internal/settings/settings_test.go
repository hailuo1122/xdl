package settings

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type memStore struct {
	m map[string][]byte
}

func (m *memStore) GetSetting(key string) ([]byte, bool, error) {
	v, ok := m.m[key]
	return v, ok, nil
}

func (m *memStore) PutSetting(key string, value []byte) error {
	if m.m == nil {
		m.m = make(map[string][]byte)
	}
	m.m[key] = append([]byte(nil), value...)
	return nil
}

func TestDefaults(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cfg")
	s := Defaults(dir)
	if s.MaxConcurrent != 3 || s.DefaultConns != 8 {
		t.Fatalf("unexpected defaults: %+v", s)
	}
	if !strings.HasSuffix(s.DownloadDir, "Downloads") {
		t.Fatalf("download dir = %q", s.DownloadDir)
	}
	if !s.CloseToTray || !s.Notifications {
		t.Fatalf("unexpected toggles: %+v", s)
	}
}

func TestNormalizeClamps(t *testing.T) {
	dir := t.TempDir()
	s := Settings{
		DownloadDir:    "relative/downloads",
		MaxConcurrent:  99,
		DefaultConns:   -3,
		GlobalLimitBps: 12,
		Theme:          "neon",
		Language:       "fr",
	}
	Normalize(&s, dir)
	if !filepath.IsAbs(s.DownloadDir) || s.DownloadDir != filepath.Join(dir, "relative", "downloads") {
		t.Fatalf("download dir = %q", s.DownloadDir)
	}
	if s.MaxConcurrent != 3 || s.DefaultConns != 8 {
		t.Fatalf("clamp failed: %+v", s)
	}
	if s.GlobalLimitBps != 32*1024 {
		t.Fatalf("limit = %d", s.GlobalLimitBps)
	}
	if s.Theme != "light" || s.Language != "zh-CN" {
		t.Fatalf("theme/lang = %q/%q", s.Theme, s.Language)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st := &memStore{}
	s := Defaults(dir)
	s.DownloadDir = filepath.Join(dir, "dl")
	s.MaxConcurrent = 5
	s.Turbo = true
	s.DefaultConns = 16
	s.GlobalLimitBps = 5 << 20
	s.FirstRunDone = true
	s.Theme = "light"
	out, err := Save(st, s, dir)
	if err != nil {
		t.Fatal(err)
	}
	got := Load(st, dir)
	if !reflect.DeepEqual(got, out) {
		t.Fatalf("roundtrip mismatch:\n got %+v\nwant %+v", got, out)
	}
}

func TestLoadCorruptFallsBack(t *testing.T) {
	dir := t.TempDir()
	st := &memStore{m: map[string][]byte{settingKey: []byte("{not json")}}
	got := Load(st, dir)
	want := Defaults(dir)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("corrupt load = %+v, want %+v", got, want)
	}
}

func TestSaveCreatesDownloadDir(t *testing.T) {
	dir := t.TempDir()
	st := &memStore{}
	s := Defaults(dir)
	s.DownloadDir = filepath.Join(dir, "nested", "out")
	if _, err := Save(st, s, dir); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(filepath.Join(dir, "nested", "out")); err != nil || !st.IsDir() {
		t.Fatalf("download dir missing: %v", err)
	}
}
