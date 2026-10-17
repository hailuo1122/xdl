package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/xdl-app/xdl/internal/bittorrent"
)

type ManagerConfig struct {
	DataDir       string
	DownloadDir   string
	MaxConcurrent int
	DefaultConns  int
	Turbo         bool
	GlobalLimit   int64 // bytes/sec, 0=unlimited
	FFmpegPath    string
	BTMaxPeers    int
	BTUploadLimit int64
	BTSeed        bool
}

type Manager struct {
	cfg     ManagerConfig
	store   *Store
	bus     *EventBus
	global  *Limiter
	httpDL  *HTTPDownloader
	mediaDL *MediaDownloader
	btDL    *TorrentDownloader

	mu           sync.Mutex
	tasks        map[TaskID]*Task
	running      map[TaskID]*runSlot
	runGen       map[TaskID]uint64
	queue        []TaskID
	active       int
	hold         bool // schedule pause: queue accepts work but kick won't start
	chunkSnap    map[TaskID][]Chunk
	chunkSavedAt map[TaskID]time.Time
	pathHold     map[string]TaskID
}

type runSlot struct {
	cancel context.CancelFunc
	gen    uint64
	done   chan struct{}
}

func NewManager(cfg ManagerConfig) (*Manager, error) {
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 3
	}
	if cfg.DefaultConns <= 0 {
		cfg.DefaultConns = DefaultConns
	}
	if cfg.DownloadDir == "" {
		cfg.DownloadDir = filepath.Join(cfg.DataDir, "Downloads")
	}
	storePath := filepath.Join(cfg.DataDir, "xdl.db")
	store, err := OpenStore(storePath)
	if err != nil {
		return nil, err
	}
	btClient, err := newBTClient(cfg)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	m := &Manager{
		cfg:          cfg,
		store:        store,
		bus:          NewEventBus(),
		global:       NewLimiter(cfg.GlobalLimit),
		httpDL:       NewHTTPDownloader(),
		mediaDL:      NewMediaDownloader(),
		btDL:         NewTorrentDownloader(btClient),
		tasks:        make(map[TaskID]*Task),
		running:      make(map[TaskID]*runSlot),
		runGen:       make(map[TaskID]uint64),
		chunkSnap:    make(map[TaskID][]Chunk),
		chunkSavedAt: make(map[TaskID]time.Time),
		pathHold:     make(map[string]TaskID),
	}
	if err := m.load(); err != nil {
		_ = m.Close()
		return nil, err
	}
	return m, nil
}

func (m *Manager) Bus() *EventBus { return m.bus }
func (m *Manager) Store() *Store  { return m.store }

// RuntimeConfig is the mutable subset of ManagerConfig the shell can change
// while the manager is running.
type RuntimeConfig struct {
	DownloadDir   string
	MaxConcurrent int
	DefaultConns  int
	Turbo         bool
	GlobalLimit   int64
	FFmpegPath    string
	BTMaxPeers    int
	BTUploadLimit int64
	BTSeed        bool
}

// ApplyConfig updates scheduling defaults for new work and the global limiter.
func (m *Manager) ApplyConfig(c RuntimeConfig) {
	m.mu.Lock()
	if c.DownloadDir != "" {
		m.cfg.DownloadDir = c.DownloadDir
	}
	if c.MaxConcurrent > 0 {
		m.cfg.MaxConcurrent = c.MaxConcurrent
	}
	if c.DefaultConns > 0 {
		m.cfg.DefaultConns = c.DefaultConns
	}
	m.cfg.Turbo = c.Turbo
	m.cfg.GlobalLimit = c.GlobalLimit
	m.cfg.FFmpegPath = c.FFmpegPath
	m.cfg.BTMaxPeers = c.BTMaxPeers
	m.cfg.BTUploadLimit = c.BTUploadLimit
	m.cfg.BTSeed = c.BTSeed
	m.global.Set(c.GlobalLimit)
	bt := m.btDL
	btCfg := btConfigFrom(m.cfg)
	m.mu.Unlock()
	if bt != nil && bt.Client() != nil {
		bt.Client().Apply(btCfg)
	}
	m.kick()
}

// BT returns the shared BitTorrent client, or nil if unavailable.
func (m *Manager) BT() *TorrentDownloader {
	return m.btDL
}

// DownloadDir returns the configured default output directory.
func (m *Manager) DownloadDir() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg.DownloadDir
}

// Stats aggregates live task and throughput counters for the tray/status bar.
func (m *Manager) Stats() (tasks, active int, speed int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tasks {
		tasks++
		switch t.State {
		case StateRunning, StateResolving:
			active++
		}
		if t.Speed > 0 {
			speed += t.Speed
		}
	}
	return
}

// PauseAll pauses every queued or running task. It returns how many changed.
func (m *Manager) PauseAll() int {
	m.mu.Lock()
	var ids []TaskID
	for id, t := range m.tasks {
		if t.State == StateQueued || t.State == StateRunning || t.State == StateResolving {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()
	n := 0
	for _, id := range ids {
		if err := m.Pause(id); err == nil {
			n++
		}
	}
	return n
}

// ResumeAll resumes paused tasks. Failed tasks stay failed for explicit retry.
func (m *Manager) ResumeAll() int {
	m.mu.Lock()
	var ids []TaskID
	for id, t := range m.tasks {
		if t.State == StatePaused {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()
	n := 0
	for _, id := range ids {
		if err := m.Resume(id); err == nil {
			n++
		}
	}
	return n
}

// Chunks returns the most recent chunk snapshot for a task.
func (m *Manager) Chunks(id TaskID) []Chunk {
	m.mu.Lock()
	defer m.mu.Unlock()
	src := m.chunkSnap[id]
	out := make([]Chunk, len(src))
	copy(out, src)
	return out
}

func (m *Manager) Close() error {
	m.mu.Lock()
	for _, slot := range m.running {
		slot.cancel()
	}
	m.mu.Unlock()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		n := len(m.running)
		m.mu.Unlock()
		if n == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	var first error
	if m.btDL != nil && m.btDL.Client() != nil {
		if err := m.btDL.Client().Close(); err != nil {
			first = err
		}
	}
	if err := m.store.Close(); err != nil && first == nil {
		first = err
	}
	return first
}

func (m *Manager) load() error {
	list, err := m.store.ListTasks()
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range list {
		m.tasks[t.ID] = t
		chunks, _ := m.store.GetChunks(t.ID)
		if t.State == StateRunning || t.State == StateResolving {
			// Crash recovery: rewind incomplete chunks, then mark paused.
			if len(chunks) > 0 {
				if plan, lerr := LoadChunkPlan(chunks, t.Size); lerr == nil {
					plan.RewindForRecovery()
					snaps := plan.Snapshot()
					m.chunkSnap[t.ID] = snaps
					_ = m.store.PutChunks(t.ID, snaps)
				} else {
					m.chunkSnap[t.ID] = chunks
				}
			}
			t.State = StatePaused
			_ = m.store.PutTask(t)
		} else if len(chunks) > 0 {
			m.chunkSnap[t.ID] = chunks
		}
		if t.FinalPath != "" && t.State != StateDone && t.State != StateCanceled {
			m.pathHold[t.FinalPath] = t.ID
		}
	}
	return nil
}

type AddParams struct {
	URL      string
	Dir      string
	Name     string
	Headers  http.Header
	Conns    int
	Kind     Kind
	KindMeta json.RawMessage
	Category string
}

func (m *Manager) Add(params AddParams) (*Task, error) {
	if params.Kind == "" {
		params.Kind = KindHTTP
	}
	dir := params.Dir
	if dir == "" {
		dir = m.cfg.DownloadDir
	}
	conns := params.Conns
	if conns <= 0 {
		conns = m.cfg.DefaultConns
	}
	id := newTaskID()
	now := time.Now()
	t := &Task{
		ID:        id,
		Kind:      params.Kind,
		URL:       params.URL,
		Dir:       dir,
		Name:      params.Name,
		State:     StateQueued,
		Priority:  0,
		Category:  params.Category,
		Conns:     conns,
		Headers:   params.Headers,
		KindMeta:  params.KindMeta,
		CreatedAt: now,
		UpdatedAt: now,
	}
	m.mu.Lock()
	m.tasks[id] = t
	m.queue = append(m.queue, id)
	m.mu.Unlock()
	if err := m.store.PutTask(t); err != nil {
		return nil, err
	}
	m.bus.Publish(Event{Type: EventTaskUpdated, Data: t})
	m.kick()
	return t, nil
}

func (m *Manager) List() []*Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Task, 0, len(m.tasks))
	for _, t := range m.tasks {
		cp := *t
		out = append(out, &cp)
	}
	return out
}

func (m *Manager) Get(id TaskID) (*Task, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok {
		return nil, false
	}
	cp := *t
	return &cp, true
}

func (m *Manager) Pause(id TaskID) error {
	m.mu.Lock()
	t, ok := m.tasks[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("not found")
	}
	m.queue = removeTaskID(m.queue, id)
	slot, running := m.running[id]
	if t.State == StatePaused && !running {
		m.mu.Unlock()
		return nil
	}
	if running {
		// Invalidate in-flight run so its cleanup cannot overwrite a later Resume.
		m.runGen[id]++
		delete(m.running, id)
		m.active--
		if m.active < 0 {
			m.active = 0
		}
	}
	t.State = StatePaused
	t.UpdatedAt = time.Now()
	t.Speed = 0
	cp := *t
	m.mu.Unlock()

	if running {
		slot.cancel()
		select {
		case <-slot.done:
		case <-time.After(2 * time.Second):
		}
	}
	_ = m.store.PutTask(&cp)
	m.bus.Publish(Event{Type: EventTaskUpdated, Data: &cp})
	return nil
}

func (m *Manager) Resume(id TaskID) error {
	m.mu.Lock()
	t, ok := m.tasks[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("not found")
	}
	switch t.State {
	case StateDone, StateCanceled:
		m.mu.Unlock()
		return fmt.Errorf("cannot resume %s", t.State)
	case StateQueued, StateRunning, StateResolving:
		m.mu.Unlock()
		return nil
	case StatePaused, StateFailed:
		t.State = StateQueued
		t.Err = nil
		t.UpdatedAt = time.Now()
		if !taskIDIn(m.queue, id) {
			m.queue = append(m.queue, id)
		}
		cp := *t
		m.mu.Unlock()
		_ = m.store.PutTask(&cp)
		m.bus.Publish(Event{Type: EventTaskUpdated, Data: &cp})
		m.kick()
		return nil
	default:
		m.mu.Unlock()
		return fmt.Errorf("cannot resume %s", t.State)
	}
}

func (m *Manager) Remove(id TaskID) error {
	_ = m.Pause(id)
	m.mu.Lock()
	t := m.tasks[id]
	var infoHash string
	if t != nil && t.Kind == KindTorrent {
		infoHash = torrentInfoHash(t.KindMeta)
	}
	delete(m.tasks, id)
	delete(m.chunkSnap, id)
	delete(m.chunkSavedAt, id)
	delete(m.runGen, id)
	m.queue = removeTaskID(m.queue, id)
	m.releasePathLocked(id)
	bt := m.btDL
	m.mu.Unlock()
	if infoHash != "" && bt != nil && bt.Client() != nil {
		bt.Client().Drop(infoHash)
	}
	return m.store.DeleteTask(id)
}

// SetHold freezes starting new downloads (used by schedule pause windows).
func (m *Manager) SetHold(hold bool) {
	m.mu.Lock()
	m.hold = hold
	m.mu.Unlock()
	if !hold {
		m.kick()
	}
}

func (m *Manager) Holding() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hold
}

func (m *Manager) kick() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.hold {
		return
	}
	for m.active < m.cfg.MaxConcurrent && len(m.queue) > 0 {
		id := m.queue[0]
		m.queue = m.queue[1:]
		t, ok := m.tasks[id]
		if !ok || t.State != StateQueued {
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		m.runGen[id]++
		gen := m.runGen[id]
		done := make(chan struct{})
		m.running[id] = &runSlot{cancel: cancel, gen: gen, done: done}
		m.active++
		t.State = StateRunning
		t.UpdatedAt = time.Now()
		go m.runTask(ctx, id, gen, done)
	}
}

func (m *Manager) runTask(ctx context.Context, id TaskID, gen uint64, done chan struct{}) {
	defer close(done)
	defer func() {
		m.mu.Lock()
		if slot, ok := m.running[id]; ok && slot.gen == gen {
			delete(m.running, id)
			m.active--
			if m.active < 0 {
				m.active = 0
			}
		}
		m.mu.Unlock()
		m.kick()
	}()

	m.mu.Lock()
	t := m.tasks[id]
	m.mu.Unlock()
	if t == nil {
		return
	}

	if !m.setStateIfGen(id, gen, StateResolving, nil) {
		return
	}

	if t.Kind == KindHLS || t.Kind == KindDASH {
		m.runMediaTask(ctx, id, gen, t)
		return
	}
	if t.Kind == KindTorrent {
		m.runTorrentTask(ctx, id, gen, t)
		return
	}

	opt := DownloadOptions{
		URL:       t.URL,
		Dir:       t.Dir,
		Name:      t.Name,
		Headers:   t.Headers,
		Conns:     t.Conns,
		Turbo:     m.cfg.Turbo,
		GlobalLim: m.global,
		FinalPath: t.FinalPath,
		PartPath:  t.PartPath,
		OnProgress: func(done, total, speed int64) {
			m.mu.Lock()
			if m.runGen[id] != gen {
				m.mu.Unlock()
				return
			}
			if cur, ok := m.tasks[id]; ok {
				if cur.State == StateRunning || cur.State == StateResolving {
					cur.Done = done
					cur.Size = total
					cur.Speed = speed
					cur.State = StateRunning
				}
			}
			m.mu.Unlock()
			m.bus.Publish(Event{Type: EventTaskProgress, Data: map[string]interface{}{
				"id": id, "done": done, "total": total, "speed": speed,
			}})
		},
		OnProbe: func(probe *ProbeResult, o *DownloadOptions) error {
			m.mu.Lock()
			cur := m.tasks[id]
			if cur == nil || m.runGen[id] != gen {
				m.mu.Unlock()
				return context.Canceled
			}
			name := cur.Name
			if name == "" {
				name = probe.Filename
			}
			name = SanitizeFilename(name)
			cur.Name = name
			o.Name = name
			cur.FinalURL = probe.FinalURL
			cur.Size = probe.Size

			if cur.FinalPath == "" {
				fp, err := m.claimPathLocked(id, cur.Dir, name)
				if err != nil {
					m.mu.Unlock()
					return err
				}
				cur.FinalPath = fp
				cur.PartPath = PartPath(fp)
			} else {
				m.pathHold[cur.FinalPath] = id
			}
			o.FinalPath = cur.FinalPath
			o.PartPath = cur.PartPath

			newVal := Validator{
				ETag:         probe.ETag,
				LastModified: probe.LastModified,
				Size:         probe.Size,
				AcceptRanges: probe.AcceptRanges,
			}

			canReuse := false
			if st, err := os.Stat(LongPath(cur.PartPath)); err == nil && probe.Size > 0 && st.Size() == probe.Size {
				if validatorsMatch(cur.Validator, probe) {
					canReuse = true
				}
			}
			if !canReuse {
				_ = os.Remove(LongPath(cur.PartPath))
				delete(m.chunkSnap, id)
				_ = m.store.DeleteChunks(id)
				o.ResumeChunks = nil
				o.ReusePart = false
			} else {
				var snaps []Chunk
				if s := m.chunkSnap[id]; len(s) > 0 {
					snaps = s
				} else if stored, err := m.store.GetChunks(id); err == nil && len(stored) > 0 {
					snaps = stored
					m.chunkSnap[id] = stored
				}
				if len(snaps) > 0 {
					o.ResumeChunks = snaps
					o.ReusePart = true
				} else {
					o.ResumeChunks = nil
					o.ReusePart = false
				}
			}
			cur.Validator = newVal
			tcp := *cur
			m.mu.Unlock()
			_ = m.store.PutTask(&tcp)
			return nil
		},
		OnChunksUpdate: func(chunks []Chunk) {
			m.mu.Lock()
			// Pause bumps runGen before workers finish so the final snapshot
			// would be dropped if we required an exact gen match — and resume
			// would then re-download from byte 0. Accept gen or gen+1 (paused).
			curGen := m.runGen[id]
			if curGen != gen && curGen != gen+1 {
				m.mu.Unlock()
				return
			}
			m.chunkSnap[id] = chunks
			m.mu.Unlock()
			m.persistChunks(id, false)
		},
	}

	res, err := m.httpDL.Download(ctx, opt)

	if ctx.Err() != nil {
		m.persistChunks(id, true)
		m.setStateIfGen(id, gen, StatePaused, nil)
		return
	}
	if err != nil {
		m.persistChunks(id, true)
		te, ok := err.(*TaskError)
		if !ok {
			te = NewTaskError(ErrCodeInternal, err.Error(), "查看日志或重试")
		}
		m.setStateIfGen(id, gen, StateFailed, te)
		return
	}
	m.mu.Lock()
	if m.runGen[id] == gen {
		if cur, ok := m.tasks[id]; ok {
			cur.Name = res.Name
			cur.FinalURL = res.FinalURL
			cur.FinalPath = res.Path
			cur.PartPath = ""
			cur.Size = res.Size
			cur.Done = res.Size
			cur.Speed = 0
		}
		delete(m.chunkSnap, id)
		delete(m.chunkSavedAt, id)
		m.releasePathLocked(id)
	}
	m.mu.Unlock()
	_ = m.store.DeleteChunks(id)
	m.setStateIfGen(id, gen, StateDone, nil)
}

func (m *Manager) runMediaTask(ctx context.Context, id TaskID, gen uint64, t *Task) {
	progress := func(done, total, speed int64) {
		m.mu.Lock()
		if m.runGen[id] != gen {
			m.mu.Unlock()
			return
		}
		if cur, ok := m.tasks[id]; ok {
			if cur.State == StateRunning || cur.State == StateResolving {
				cur.Done = done
				cur.Size = total
				cur.Speed = speed
				cur.State = StateRunning
			}
		}
		m.mu.Unlock()
		m.bus.Publish(Event{Type: EventTaskProgress, Data: map[string]interface{}{
			"id": id, "done": done, "total": total, "speed": speed,
		}})
	}

	// Claim output path early so the UI can show the destination.
	m.mu.Lock()
	cur := m.tasks[id]
	if cur == nil || m.runGen[id] != gen {
		m.mu.Unlock()
		return
	}
	name := cur.Name
	if name == "" {
		name = "video.mp4"
	} else if filepath.Ext(name) == "" {
		name += ".mp4"
	}
	name = SanitizeFilename(name)
	cur.Name = name
	if cur.FinalPath == "" {
		fp, err := m.claimPathLocked(id, cur.Dir, name)
		if err != nil {
			m.mu.Unlock()
			m.setStateIfGen(id, gen, StateFailed, NewTaskError(ErrCodeInternal, err.Error(), "更换目录后重试"))
			return
		}
		cur.FinalPath = fp
	} else {
		m.pathHold[cur.FinalPath] = id
	}
	finalPath := cur.FinalPath
	kindMeta := cur.KindMeta
	headers := cur.Headers
	dir := cur.Dir
	conns := cur.Conns
	url := cur.URL
	ffmpeg := m.cfg.FFmpegPath
	tcp := *cur
	m.mu.Unlock()
	_ = m.store.PutTask(&tcp)

	res, err := m.mediaDL.Download(ctx, MediaRunOptions{
		URL:        url,
		Dir:        dir,
		Name:       name,
		Headers:    headers,
		KindMeta:   kindMeta,
		Conns:      conns,
		FFmpegPath: ffmpeg,
		GlobalLim:  m.global,
		FinalPath:  finalPath,
		OnProgress: progress,
		OnMeta: func(raw json.RawMessage) error {
			m.mu.Lock()
			if m.runGen[id] != gen {
				m.mu.Unlock()
				return context.Canceled
			}
			if cur, ok := m.tasks[id]; ok {
				cur.KindMeta = raw
				cp := *cur
				m.mu.Unlock()
				_ = m.store.PutTask(&cp)
				return nil
			}
			m.mu.Unlock()
			return nil
		},
	})

	if ctx.Err() != nil {
		m.setStateIfGen(id, gen, StatePaused, nil)
		return
	}
	if err != nil {
		te, ok := err.(*TaskError)
		if !ok {
			te = NewTaskError(ErrCodeInternal, err.Error(), "检查播放列表或 Cookie 后重试")
		}
		m.setStateIfGen(id, gen, StateFailed, te)
		return
	}
	m.mu.Lock()
	if m.runGen[id] == gen {
		if cur, ok := m.tasks[id]; ok {
			cur.Name = res.Name
			cur.FinalURL = res.FinalURL
			cur.FinalPath = res.Path
			cur.PartPath = ""
			cur.Size = res.Size
			cur.Done = res.Size
			cur.Speed = 0
		}
		m.releasePathLocked(id)
	}
	m.mu.Unlock()
	m.setStateIfGen(id, gen, StateDone, nil)
}

func (m *Manager) runTorrentTask(ctx context.Context, id TaskID, gen uint64, t *Task) {
	progress := func(done, total, speed int64) {
		m.mu.Lock()
		if m.runGen[id] != gen {
			m.mu.Unlock()
			return
		}
		if cur, ok := m.tasks[id]; ok {
			if cur.State == StateRunning || cur.State == StateResolving {
				cur.Done = done
				cur.Size = total
				cur.Speed = speed
				cur.State = StateRunning
			}
		}
		m.mu.Unlock()
		m.bus.Publish(Event{Type: EventTaskProgress, Data: map[string]interface{}{
			"id": id, "done": done, "total": total, "speed": speed,
		}})
	}

	m.mu.Lock()
	cur := m.tasks[id]
	if cur == nil || m.runGen[id] != gen {
		m.mu.Unlock()
		return
	}
	name := cur.Name
	dir := cur.Dir
	url := cur.URL
	kindMeta := cur.KindMeta
	if cur.FinalPath == "" && name != "" {
		cur.FinalPath = filepath.Join(dir, name)
		m.pathHold[cur.FinalPath] = id
	} else if cur.FinalPath != "" {
		m.pathHold[cur.FinalPath] = id
	}
	tcp := *cur
	m.mu.Unlock()
	_ = m.store.PutTask(&tcp)

	if m.btDL == nil {
		m.setStateIfGen(id, gen, StateFailed, NewTaskError(ErrCodeInternal, "BT 客户端未启动", "重启应用后重试"))
		return
	}

	res, err := m.btDL.Download(ctx, TorrentRunOptions{
		URL:        url,
		Dir:        dir,
		Name:       name,
		KindMeta:   kindMeta,
		OnProgress: progress,
		OnMeta: func(raw json.RawMessage) error {
			m.mu.Lock()
			if m.runGen[id] != gen {
				m.mu.Unlock()
				return context.Canceled
			}
			if cur, ok := m.tasks[id]; ok {
				cur.KindMeta = raw
				meta := bittorrent.DecodeMeta(raw)
				if cur.Name == "" && meta.Name != "" {
					cur.Name = meta.Name
				}
				if meta.Peers > 0 {
					cur.Conns = meta.Peers
				}
				cp := *cur
				m.mu.Unlock()
				_ = m.store.PutTask(&cp)
				m.bus.Publish(Event{Type: EventTaskUpdated, Data: &cp})
				return nil
			}
			m.mu.Unlock()
			return nil
		},
	})

	if ctx.Err() != nil {
		m.setStateIfGen(id, gen, StatePaused, nil)
		return
	}
	if err != nil {
		te, ok := err.(*TaskError)
		if !ok {
			te = NewTaskError(ErrCodeNetwork, err.Error(), "检查磁力链接、端口或网络后重试")
		}
		m.setStateIfGen(id, gen, StateFailed, te)
		return
	}
	m.mu.Lock()
	if m.runGen[id] == gen {
		if cur, ok := m.tasks[id]; ok {
			cur.Name = res.Name
			cur.FinalPath = res.Path
			cur.PartPath = ""
			cur.Size = res.Size
			cur.Done = res.Size
			cur.Speed = 0
		}
		m.releasePathLocked(id)
	}
	m.mu.Unlock()
	m.setStateIfGen(id, gen, StateDone, nil)
}

func newBTClient(cfg ManagerConfig) (*bittorrent.Client, error) {
	return bittorrent.NewClient(btConfigFrom(cfg))
}

func btConfigFrom(cfg ManagerConfig) bittorrent.Config {
	return bittorrent.Config{
		DataDir:        filepath.Join(cfg.DataDir, "bt"),
		MaxPeers:       cfg.BTMaxPeers,
		UploadLimitBps: cfg.BTUploadLimit,
		Seed:           cfg.BTSeed,
	}
}

func torrentInfoHash(raw json.RawMessage) string {
	return bittorrent.DecodeMeta(raw).InfoHash
}


func (m *Manager) persistChunks(id TaskID, force bool) {
	m.mu.Lock()
	chunks, ok := m.chunkSnap[id]
	if !ok || len(chunks) == 0 {
		m.mu.Unlock()
		return
	}
	if !force {
		if last, exists := m.chunkSavedAt[id]; exists && time.Since(last) < time.Second {
			m.mu.Unlock()
			return
		}
	}
	m.chunkSavedAt[id] = time.Now()
	cp := append([]Chunk(nil), chunks...)
	m.mu.Unlock()
	_ = m.store.PutChunks(id, cp)
}

func (m *Manager) setStateIfGen(id TaskID, gen uint64, state State, err *TaskError) bool {
	m.mu.Lock()
	if m.runGen[id] != gen {
		m.mu.Unlock()
		return false
	}
	t, ok := m.tasks[id]
	if !ok {
		m.mu.Unlock()
		return false
	}
	if state == StatePaused && t.State != StateRunning && t.State != StateResolving && t.State != StatePaused {
		m.mu.Unlock()
		return false
	}
	t.State = state
	t.Err = err
	t.UpdatedAt = time.Now()
	if state != StateRunning {
		t.Speed = 0
	}
	cp := *t
	m.mu.Unlock()
	_ = m.store.PutTask(&cp)
	m.bus.Publish(Event{Type: EventTaskUpdated, Data: &cp})
	return true
}

// claimPathLocked reserves a final path so concurrent tasks do not share one .xdlpart.
// Caller must hold m.mu.
func (m *Manager) claimPathLocked(id TaskID, dir, name string) (string, error) {
	name = SanitizeFilename(name)
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 0; i < 10000; i++ {
		leaf := name
		if i > 0 {
			leaf = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		candidate := filepath.Join(dir, leaf)
		if owner, ok := m.pathHold[candidate]; ok && owner != id {
			continue
		}
		if _, err := os.Stat(candidate); err == nil {
			continue
		}
		part := candidate + partSuffix
		if _, err := os.Stat(part); err == nil {
			if owner, ok := m.pathHold[candidate]; !ok || owner != id {
				continue
			}
		}
		m.pathHold[candidate] = id
		return candidate, nil
	}
	return "", fmt.Errorf("could not find unique name for %s", name)
}

func (m *Manager) releasePathLocked(id TaskID) {
	for p, owner := range m.pathHold {
		if owner == id {
			delete(m.pathHold, p)
		}
	}
}

func validatorsMatch(stored Validator, probe *ProbeResult) bool {
	if stored.Size == 0 {
		return false
	}
	if stored.Size != probe.Size {
		return false
	}
	if probe.ETag == "" && probe.LastModified == "" {
		return true
	}
	if stored.ETag != "" && probe.ETag != "" && stored.ETag != probe.ETag {
		return false
	}
	if stored.LastModified != "" && probe.LastModified != "" && stored.LastModified != probe.LastModified {
		return false
	}
	if stored.ETag != "" && probe.ETag == "" {
		return false
	}
	if stored.LastModified != "" && probe.LastModified == "" && stored.ETag == "" {
		return false
	}
	return true
}

func removeTaskID(q []TaskID, id TaskID) []TaskID {
	out := q[:0]
	for _, x := range q {
		if x != id {
			out = append(out, x)
		}
	}
	// If all removed, return new empty slice to avoid aliasing issues.
	if len(out) == 0 {
		return nil
	}
	return out
}

func taskIDIn(q []TaskID, id TaskID) bool {
	for _, x := range q {
		if x == id {
			return true
		}
	}
	return false
}

func newTaskID() TaskID {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return TaskID(hex.EncodeToString(b[:]))
}
