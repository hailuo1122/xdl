package engine

import (
	"fmt"
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

func TestPausedQueuedTaskStaysPaused(t *testing.T) {
	const size = 2 << 20
	body := make([]byte, size)
	for i := range body {
		body[i] = byte(i)
	}

	// Slow server so task A stays running while we pause B.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("ETag", `"q"`)
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
			status = http.StatusPartialContent
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(body)))
		}
		chunk := body[start : end+1]
		w.Header().Set("Content-Length", strconv.Itoa(len(chunk)))
		w.WriteHeader(status)
		_ = throttleWrite(r.Context(), w, chunk, 256<<10) // 256 KiB/s
	}))
	defer srv.Close()

	dataDir := t.TempDir()
	m, err := NewManager(ManagerConfig{
		DataDir:       dataDir,
		DownloadDir:   filepath.Join(dataDir, "dl"),
		MaxConcurrent: 1,
		DefaultConns:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	a, err := m.Add(AddParams{URL: srv.URL + "/a.bin", Name: "a.bin", Conns: 1})
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.Add(AddParams{URL: srv.URL + "/b.bin", Name: "b.bin", Conns: 1})
	if err != nil {
		t.Fatal(err)
	}

	waitState(t, m, a.ID, StateRunning, 5*time.Second)
	// B should be queued behind A.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		tb, _ := m.Get(b.ID)
		if tb.State == StateQueued {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	tb, _ := m.Get(b.ID)
	if tb.State != StateQueued {
		t.Fatalf("B want queued, got %s", tb.State)
	}

	if err := m.Pause(b.ID); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, b.ID, StatePaused, 2*time.Second)

	waitState(t, m, a.ID, StateDone, 60*time.Second)
	time.Sleep(300 * time.Millisecond)

	tb, _ = m.Get(b.ID)
	if tb.State != StatePaused {
		t.Fatalf("paused queued task B auto-started: state=%s", tb.State)
	}
}

func TestPauseResumeRace(t *testing.T) {
	const size = 4 << 20 // >= MinChunkSize so multi-path is used
	body := make([]byte, size)
	for i := range body {
		body[i] = byte(i % 200)
	}
	var dataRequests atomic.Int64
	var downloadCycles atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("ETag", `"race"`)
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
			status = http.StatusPartialContent
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(body)))
			if end > start {
				dataRequests.Add(1)
				if start == 0 {
					downloadCycles.Add(1)
				}
			}
		}
		chunk := body[start : end+1]
		w.Header().Set("Content-Length", strconv.Itoa(len(chunk)))
		w.WriteHeader(status)
		_ = throttleWrite(r.Context(), w, chunk, 4<<20)
	}))
	defer srv.Close()

	dataDir := t.TempDir()
	m, err := NewManager(ManagerConfig{
		DataDir:       dataDir,
		DownloadDir:   filepath.Join(dataDir, "dl"),
		MaxConcurrent: 1,
		DefaultConns:  2,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	task, err := m.Add(AddParams{URL: srv.URL + "/race.bin", Name: "race.bin", Conns: 2})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, task.ID, StateRunning, 5*time.Second)

	_ = m.Pause(task.ID)
	_ = m.Resume(task.ID)

	waitState(t, m, task.ID, StateDone, 60*time.Second)
	final, _ := m.Get(task.ID)
	if final.State != StateDone || final.Done != size {
		t.Fatalf("want done size=%d, got state=%s done=%d", size, final.State, final.Done)
	}
	if n := dataRequests.Load(); n < 1 {
		t.Fatalf("expected data transfers, got %d", n)
	}
	// Pause+Resume should not leave the task stuck paused or spawn unbounded runs.
	if c := downloadCycles.Load(); c > 4 {
		t.Fatalf("too many download cycles from race: %d", c)
	}
}

func TestConcurrentAddSameName(t *testing.T) {
	const size = 1 << 20
	body := make([]byte, size)
	for i := range body {
		body[i] = byte(i % 251)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)
		_ = throttleWrite(r.Context(), w, body, 2<<20)
	}))
	defer srv.Close()

	dir := t.TempDir()
	m, err := NewManager(ManagerConfig{
		DataDir:       t.TempDir(),
		DownloadDir:   dir,
		MaxConcurrent: 2,
		DefaultConns:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	a, err := m.Add(AddParams{URL: srv.URL + "/same.bin", Dir: dir, Name: "same.bin", Conns: 1})
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.Add(AddParams{URL: srv.URL + "/same.bin", Dir: dir, Name: "same.bin", Conns: 1})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, a.ID, StateDone, 30*time.Second)
	waitState(t, m, b.ID, StateDone, 30*time.Second)

	ta, _ := m.Get(a.ID)
	tb, _ := m.Get(b.ID)
	if ta.FinalPath == "" || tb.FinalPath == "" || ta.FinalPath == tb.FinalPath {
		t.Fatalf("paths collided: %q %q", ta.FinalPath, tb.FinalPath)
	}
	for _, p := range []string{ta.FinalPath, tb.FinalPath} {
		got, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(body) || !bytesEqual(got, body) {
			t.Fatalf("content mismatch %s", p)
		}
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
