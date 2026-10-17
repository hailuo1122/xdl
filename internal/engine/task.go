package engine

import (
	"encoding/json"
	"net/http"
	"time"
)

type TaskID string

type Kind string

const (
	KindHTTP    Kind = "http"
	KindHLS     Kind = "hls"
	KindDASH    Kind = "dash"
	KindTorrent Kind = "torrent"
	KindBatch   Kind = "batch"
	KindGrab    Kind = "grab"
)

type State string

const (
	StateQueued      State = "queued"
	StateResolving   State = "resolving"
	StateRunning     State = "running"
	StatePaused      State = "paused"
	StateWaitingUser State = "waiting_user"
	StateVerifying   State = "verifying"
	StateDone        State = "done"
	StateFailed      State = "failed"
	StateCanceled    State = "canceled"
)

type Validator struct {
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"lastModified,omitempty"`
	Size         int64  `json:"size,omitempty"`
	AcceptRanges bool   `json:"acceptRanges"`
}

type TaskError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

type Task struct {
	ID        TaskID          `json:"id"`
	Kind      Kind            `json:"kind"`
	URL       string          `json:"url"`
	FinalURL  string          `json:"finalUrl,omitempty"`
	Dir       string          `json:"dir"`
	Name      string          `json:"name"`
	FinalPath string          `json:"finalPath,omitempty"`
	PartPath  string          `json:"partPath,omitempty"`
	Size      int64           `json:"size"`
	Done      int64           `json:"done"`
	Speed     int64           `json:"speed"` // bytes/sec, ephemeral
	Validator Validator       `json:"validator"`
	State     State           `json:"state"`
	Priority  int             `json:"priority"`
	Category  string          `json:"category,omitempty"`
	Conns     int             `json:"conns"`
	Headers   http.Header     `json:"headers,omitempty"`
	KindMeta  json.RawMessage `json:"kindMeta,omitempty"`
	Err       *TaskError      `json:"err,omitempty"`
	CreatedAt time.Time       `json:"createdAt"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

type ChunkState string

const (
	ChunkPending ChunkState = "pending"
	ChunkRunning ChunkState = "running"
	ChunkDone    ChunkState = "done"
	ChunkFailed  ChunkState = "failed"
	ChunkStolen  ChunkState = "stolen"
)

type Chunk struct {
	Index    int        `json:"index"`
	Start    int64      `json:"start"`
	End      int64      `json:"end"` // inclusive
	Done     int64      `json:"done"`
	State    ChunkState `json:"state"`
	Owner    int        `json:"owner"` // worker id, -1 if none
	Attempts int        `json:"attempts"`
}

func (c Chunk) Remaining() int64 {
	total := c.End - c.Start + 1
	if c.Done >= total {
		return 0
	}
	return total - c.Done
}

func (c Chunk) WrittenEnd() int64 {
	return c.Start + c.Done - 1
}
