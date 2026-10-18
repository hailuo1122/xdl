package engine

import (
	"path/filepath"
	"testing"
	"time"
)

// Remove 必须先删掉持久化记录，再广播 task.removed，
// 否则前端乐观移除后会被一条迟到的 task.updated 又把卡片塞回来。
func TestRemovePublishesEventAndDropsTask(t *testing.T) {
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

	ch := m.Bus().Subscribe(16)
	defer m.Bus().Unsubscribe(ch)

	task, err := m.Add(AddParams{URL: "http://127.0.0.1:1/nope.bin", Name: "nope.bin", Conns: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Store().GetTask(task.ID); err != nil {
		t.Fatalf("task should be persisted before remove: %v", err)
	}

	if err := m.Remove(task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Store().GetTask(task.ID); err == nil {
		t.Fatal("task still in store after Remove")
	}

	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Type != EventTaskRemoved {
				continue
			}
			data, ok := ev.Data.(map[string]string)
			if !ok || data["id"] != string(task.ID) {
				t.Fatalf("unexpected removed payload: %#v", ev.Data)
			}
			return
		case <-deadline:
			t.Fatal("no task.removed event after Remove")
		}
	}
}
