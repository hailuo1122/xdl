package engine_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xdl-app/xdl/internal/engine"
	"github.com/xdl-app/xdl/internal/media/job"
)

func testServerBase(t *testing.T) string {
	t.Helper()
	base := os.Getenv("XDL_TESTSERVER")
	if base == "" {
		base = "http://127.0.0.1:18080"
	}
	resp, err := http.Get(base + "/health")
	if err != nil {
		t.Skip("testserver not running:", err)
	}
	_ = resp.Body.Close()
	return base
}

func TestManagerHLSTask(t *testing.T) {
	base := testServerBase(t)
	dir := t.TempDir()
	mgr, err := engine.NewManager(engine.ManagerConfig{
		DataDir:       filepath.Join(dir, "db"),
		DownloadDir:   dir,
		MaxConcurrent: 1,
		DefaultConns:  4,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	meta, err := job.EncodeMeta(job.Meta{Protocol: "hls", MediaURL: base + "/m3/media_hi.m3u8"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := mgr.Add(engine.AddParams{
		URL:      base + "/m3/media_hi.m3u8",
		Dir:      dir,
		Name:     "hi.ts",
		Kind:     engine.KindHLS,
		KindMeta: meta,
		Conns:    4,
	})
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		cur, ok := mgr.Get(task.ID)
		if !ok {
			t.Fatal("task missing")
		}
		switch cur.State {
		case engine.StateDone:
			if cur.Size <= 0 {
				t.Fatalf("empty result: %+v", cur)
			}
			if _, err := os.Stat(cur.FinalPath); err != nil {
				t.Fatal(err)
			}
			return
		case engine.StateFailed:
			t.Fatalf("failed: %+v", cur.Err)
		}
		time.Sleep(80 * time.Millisecond)
	}
	t.Fatal("timeout waiting for HLS task")
}

func TestManagerDASHRejectDRM(t *testing.T) {
	base := testServerBase(t)
	dir := t.TempDir()
	mgr, err := engine.NewManager(engine.ManagerConfig{
		DataDir: filepath.Join(dir, "db"), DownloadDir: dir, MaxConcurrent: 1, DefaultConns: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	meta, _ := job.EncodeMeta(job.Meta{Protocol: "dash", MediaURL: base + "/m3/drm.mpd"})
	task, err := mgr.Add(engine.AddParams{
		URL: base + "/m3/drm.mpd", Dir: dir, Name: "drm.mp4", Kind: engine.KindDASH, KindMeta: meta,
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		cur, _ := mgr.Get(task.ID)
		if cur.State == engine.StateFailed {
			msg := ""
			if cur.Err != nil {
				msg = cur.Err.Message + cur.Err.Hint
			}
			if !strings.Contains(msg, "ContentProtection") && !strings.Contains(msg, "DRM") {
				// Accept any failure with a Chinese/English DRM hint from dash.Parse.
				if !strings.Contains(msg, "不支持") && !strings.Contains(strings.ToLower(msg), "drm") &&
					!strings.Contains(msg, "Widevine") && !strings.Contains(msg, "ContentProtection") {
					t.Fatalf("unexpected failure: %q", msg)
				}
			}
			return
		}
		if cur.State == engine.StateDone {
			t.Fatal("DRM task should not succeed")
		}
		time.Sleep(80 * time.Millisecond)
	}
	t.Fatal("timeout")
}
