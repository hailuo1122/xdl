package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestResumeReusesPartialData(t *testing.T) {
	const size = 8 << 20
	body := make([]byte, size)
	for i := range body {
		body[i] = byte((i*17 + 3) % 251)
	}
	wantSum := sha256.Sum256(body)

	var totalServed atomic.Int64
	var afterResume atomic.Int64
	var resumed atomic.Bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveResumeBody(w, r, body, 2<<20, &totalServed, &afterResume, &resumed)
	}))
	defer srv.Close()

	dataDir := t.TempDir()
	dlDir := filepath.Join(dataDir, "dl")
	m, err := NewManager(ManagerConfig{
		DataDir:       dataDir,
		DownloadDir:   dlDir,
		MaxConcurrent: 1,
		DefaultConns:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	task, err := m.Add(AddParams{
		URL:   srv.URL + "/resume.bin",
		Dir:   dlDir,
		Name:  "resume.bin",
		Conns: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	waitDone := waitTaskDoneAtLeast(t, m, task.ID, 2<<20, 30*time.Second)
	if waitDone < 2<<20 {
		t.Fatalf("progress before pause: %d", waitDone)
	}
	if err := m.Pause(task.ID); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, task.ID, StatePaused, 5*time.Second)

	resumed.Store(true)
	afterResume.Store(0)
	if err := m.Resume(task.ID); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, task.ID, StateDone, 60*time.Second)

	gotAfter := afterResume.Load()
	t.Logf("after-resume served=%d total=%d", gotAfter, totalServed.Load())
	if gotAfter > 6.5*(1<<20) {
		t.Fatalf("resume re-downloaded too much: %d > 6.5MiB", gotAfter)
	}

	final, ok := m.Get(task.ID)
	if !ok || final.FinalPath == "" {
		// FinalPath cleared PartPath; file is at FinalPath or Name in dir
		t.Fatal("missing task")
	}
	path := final.FinalPath
	if path == "" {
		path = filepath.Join(dlDir, "resume.bin")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(got)
	if sum != wantSum {
		t.Fatalf("sha mismatch got=%s want=%s", hex.EncodeToString(sum[:]), hex.EncodeToString(wantSum[:]))
	}
}

func TestResumeAfterRestart(t *testing.T) {
	const size = 8 << 20
	body := make([]byte, size)
	for i := range body {
		body[i] = byte((i*19 + 5) % 251)
	}
	wantSum := sha256.Sum256(body)

	var totalServed atomic.Int64
	var afterResume atomic.Int64
	var resumed atomic.Bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveResumeBody(w, r, body, 2<<20, &totalServed, &afterResume, &resumed)
	}))
	defer srv.Close()

	dataDir := t.TempDir()
	dlDir := filepath.Join(dataDir, "dl")
	m1, err := NewManager(ManagerConfig{
		DataDir:       dataDir,
		DownloadDir:   dlDir,
		MaxConcurrent: 1,
		DefaultConns:  1,
	})
	if err != nil {
		t.Fatal(err)
	}

	task, err := m1.Add(AddParams{
		URL:   srv.URL + "/restart.bin",
		Dir:   dlDir,
		Name:  "restart.bin",
		Conns: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	id := task.ID

	waitDone := waitTaskDoneAtLeast(t, m1, id, 2<<20, 30*time.Second)
	if waitDone < 2<<20 {
		t.Fatalf("progress before pause: %d", waitDone)
	}
	if err := m1.Pause(id); err != nil {
		t.Fatal(err)
	}
	waitState(t, m1, id, StatePaused, 5*time.Second)
	// Ensure chunk snapshot flushed.
	time.Sleep(200 * time.Millisecond)
	if err := m1.Close(); err != nil {
		t.Fatal(err)
	}

	resumed.Store(true)
	afterResume.Store(0)

	m2, err := NewManager(ManagerConfig{
		DataDir:       dataDir,
		DownloadDir:   dlDir,
		MaxConcurrent: 1,
		DefaultConns:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Close()

	got, ok := m2.Get(id)
	if !ok || got.State != StatePaused {
		t.Fatalf("expected paused task after restart, got %+v", got)
	}
	if err := m2.Resume(id); err != nil {
		t.Fatal(err)
	}
	waitState(t, m2, id, StateDone, 60*time.Second)

	gotAfter := afterResume.Load()
	t.Logf("after-restart-resume served=%d", gotAfter)
	if gotAfter > 6.5*(1<<20) {
		t.Fatalf("resume after restart re-downloaded too much: %d", gotAfter)
	}

	final, _ := m2.Get(id)
	path := final.FinalPath
	if path == "" {
		path = filepath.Join(dlDir, "restart.bin")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if sum != wantSum {
		t.Fatalf("sha mismatch")
	}
}

func serveResumeBody(w http.ResponseWriter, r *http.Request, body []byte, bps int64, total, after *atomic.Int64, resumed *atomic.Bool) {
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("ETag", `"resume-v1"`)
	start, end := 0, len(body)-1
	status := http.StatusOK
	if rng := r.Header.Get("Range"); strings.HasPrefix(rng, "bytes=") {
		parts := strings.SplitN(strings.TrimPrefix(rng, "bytes="), "-", 2)
		if parts[0] != "" {
			start, _ = strconv.Atoi(parts[0])
		}
		if len(parts) == 2 && parts[1] != "" {
			end, _ = strconv.Atoi(parts[1])
		}
		if start < 0 || end >= len(body) || start > end {
			http.Error(w, "bad range", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		status = http.StatusPartialContent
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(body)))
	}
	chunk := body[start : end+1]
	w.Header().Set("Content-Length", strconv.Itoa(len(chunk)))
	w.WriteHeader(status)

	var cw io.Writer = &countingWriter{w: w, n: total}
	if resumed.Load() {
		cw = &multiCountWriter{w: w, a: total, b: after}
	}
	_ = throttleWrite(r.Context(), cw, chunk, bps)
}

type multiCountWriter struct {
	w    http.ResponseWriter
	a, b *atomic.Int64
}

func (m *multiCountWriter) Write(p []byte) (int, error) {
	n, err := m.w.Write(p)
	m.a.Add(int64(n))
	m.b.Add(int64(n))
	return n, err
}

func waitTaskDoneAtLeast(t *testing.T, m *Manager, id TaskID, min int64, timeout time.Duration) int64 {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last int64
	for time.Now().Before(deadline) {
		task, ok := m.Get(id)
		if !ok {
			t.Fatal("task missing")
		}
		last = task.Done
		if task.Done >= min || task.State == StateDone || task.State == StateFailed {
			return task.Done
		}
		time.Sleep(50 * time.Millisecond)
	}
	return last
}

func waitState(t *testing.T, m *Manager, id TaskID, want State, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		task, ok := m.Get(id)
		if !ok {
			t.Fatal("task missing")
		}
		if task.State == want {
			return
		}
		if task.State == StateFailed {
			t.Fatalf("task failed: %+v", task.Err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	task, _ := m.Get(id)
	t.Fatalf("timeout waiting for %s, got %s done=%d", want, task.State, task.Done)
}
