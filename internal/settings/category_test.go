package settings

import (
	"path/filepath"
	"testing"
	"time"
)

func TestMatchCategory(t *testing.T) {
	rules := DefaultCategories()
	r, ok := MatchCategory(rules, "https://cdn.example.com/a/movie.mp4", "")
	if !ok || r.Name != "视频" {
		t.Fatalf("got %+v ok=%v", r, ok)
	}
	r, ok = MatchCategory(rules, "https://files.example.com/x", "report.PDF")
	if !ok || r.Name != "文档" {
		t.Fatalf("got %+v ok=%v", r, ok)
	}
}

func TestResolveDir(t *testing.T) {
	base := filepath.Join("D:", "dl")
	dir, cat := ResolveDir(base, DefaultCategories(), true, "https://a.com/f.zip", "")
	if cat != "压缩包" {
		t.Fatalf("cat=%q", cat)
	}
	if dir != filepath.Join(base, "Archives") {
		t.Fatalf("dir=%q", dir)
	}
	dir, cat = ResolveDir(base, DefaultCategories(), false, "https://a.com/f.zip", "")
	if dir != base || cat != "" {
		t.Fatalf("auto off: %q %q", dir, cat)
	}
}

func TestInSchedulePause(t *testing.T) {
	s := Settings{ScheduleEnabled: true, SchedulePauseStart: 22, SchedulePauseEnd: 7}
	// 23:00 → pause
	now := time.Date(2026, 1, 1, 23, 0, 0, 0, time.Local)
	if !s.InSchedulePause(now) {
		t.Fatal("expected pause at 23")
	}
	now = time.Date(2026, 1, 1, 10, 0, 0, 0, time.Local)
	if s.InSchedulePause(now) {
		t.Fatal("expected active at 10")
	}
	s.SchedulePauseStart, s.SchedulePauseEnd = 9, 12
	now = time.Date(2026, 1, 1, 10, 0, 0, 0, time.Local)
	if !s.InSchedulePause(now) {
		t.Fatal("expected pause 9-12")
	}
}
