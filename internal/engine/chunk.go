package engine

import (
	"fmt"
	"sync"
	"time"
)

const (
	MinChunkSize     = 4 << 20  // 4 MiB
	MaxChunkSize     = 64 << 20 // 64 MiB
	StealMinRemain   = 2 << 20  // 2 MiB
	StealMinETA      = 3 * time.Second
	CrashRewindBytes = 1 << 20 // 1 MiB rewind on recovery
)

// ChunkPlan holds the mutable chunk list for one task. All methods require external lock
// or use the internal mutex when called via Scheduler helpers.
type ChunkPlan struct {
	mu     sync.Mutex
	chunks []*Chunk
	nextID int
}

// LoadChunkPlan rebuilds a plan from persisted chunks. Running chunks become pending.
// nextID is derived from the max index; coverage of [0, size) is validated when size > 0.
func LoadChunkPlan(chunks []Chunk, size int64) (*ChunkPlan, error) {
	if len(chunks) == 0 {
		return nil, fmt.Errorf("empty chunk snapshot")
	}
	p := &ChunkPlan{chunks: make([]*Chunk, 0, len(chunks))}
	maxID := 0
	for i := range chunks {
		c := chunks[i]
		if c.Index >= maxID {
			maxID = c.Index + 1
		}
		if c.State == ChunkRunning || c.State == ChunkFailed {
			c.State = ChunkPending
			c.Owner = -1
		}
		cp := c
		p.chunks = append(p.chunks, &cp)
	}
	p.nextID = maxID
	if err := p.ValidateNoOverlap(); err != nil {
		return nil, err
	}
	if size > 0 {
		snaps := p.Snapshot()
		var covered int64
		for _, c := range snaps {
			if c.End < 0 {
				continue
			}
			covered += c.End - c.Start + 1
		}
		if covered != size {
			return nil, fmt.Errorf("chunk coverage %d != size %d", covered, size)
		}
	}
	return p, nil
}

func NewChunkPlan(size int64, conns int) *ChunkPlan {
	if conns < 1 {
		conns = 1
	}
	chunkSize := size / int64(conns*4)
	if chunkSize < MinChunkSize {
		chunkSize = MinChunkSize
	}
	if chunkSize > MaxChunkSize {
		chunkSize = MaxChunkSize
	}
	if size > 0 && chunkSize > size {
		chunkSize = size
	}

	p := &ChunkPlan{}
	if size <= 0 {
		// unknown size → single open-ended chunk
		p.chunks = []*Chunk{{Index: 0, Start: 0, End: -1, State: ChunkPending, Owner: -1}}
		p.nextID = 1
		return p
	}

	var start int64
	idx := 0
	for start < size {
		end := start + chunkSize - 1
		if end >= size-1 {
			end = size - 1
		}
		p.chunks = append(p.chunks, &Chunk{
			Index: idx,
			Start: start,
			End:   end,
			State: ChunkPending,
			Owner: -1,
		})
		idx++
		start = end + 1
		if start >= size {
			break
		}
	}
	p.nextID = idx
	return p
}

func (p *ChunkPlan) Snapshot() []Chunk {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Chunk, len(p.chunks))
	for i, c := range p.chunks {
		out[i] = *c
	}
	return out
}

func (p *ChunkPlan) TotalDone() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	var n int64
	for _, c := range p.chunks {
		n += c.Done
	}
	return n
}

// ClaimNext returns the next pending chunk for worker, or nil if none.
func (p *ChunkPlan) ClaimNext(workerID int) *Chunk {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.chunks {
		if c.State == ChunkPending {
			c.State = ChunkRunning
			c.Owner = workerID
			cp := *c
			return &cp
		}
	}
	return nil
}

// CurrentEnd returns the chunk's current [start, end] bounds (end inclusive).
// Safe for concurrent use while TrySteal may shrink End.
func (p *ChunkPlan) CurrentEnd(index int) (start, end int64, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.chunks {
		if c.Index == index {
			return c.Start, c.End, true
		}
	}
	return 0, 0, false
}

func (p *ChunkPlan) UpdateProgress(index int, done int64) {
	_, _ = p.UpdateProgressBounds(index, done)
}

// UpdateProgressBounds records downloaded bytes and returns the chunk's current
// inclusive end in one lock acquisition (hot path for Range workers).
func (p *ChunkPlan) UpdateProgressBounds(index int, done int64) (end int64, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.chunks {
		if c.Index == index {
			if c.End >= 0 {
				maxDone := c.End - c.Start + 1
				if done > maxDone {
					done = maxDone
				}
			}
			if done > c.Done {
				c.Done = done
			}
			return c.End, true
		}
	}
	return 0, false
}

func (p *ChunkPlan) MarkDone(index int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.chunks {
		if c.Index == index {
			if c.End >= 0 {
				c.Done = c.End - c.Start + 1
			}
			c.State = ChunkDone
			c.Owner = -1
			return
		}
	}
}

func (p *ChunkPlan) MarkFailed(index int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.chunks {
		if c.Index == index {
			c.Attempts++
			c.State = ChunkPending
			c.Owner = -1
			return
		}
	}
}

func (p *ChunkPlan) AllDone() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.chunks) == 0 {
		return false
	}
	for _, c := range p.chunks {
		if c.State != ChunkDone {
			return false
		}
	}
	return true
}

func (p *ChunkPlan) HasPendingOrRunning() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.chunks {
		if c.State == ChunkPending || c.State == ChunkRunning {
			return true
		}
	}
	return false
}

// TrySteal splits the slowest running chunk's tail for an idle worker.
// speedBps is the victim chunk's recent speed; if 0, ETA check is skipped when remain > StealMinRemain*2.
func (p *ChunkPlan) TrySteal(workerID int, speedBpsOf map[int]int64) *Chunk {
	p.mu.Lock()
	defer p.mu.Unlock()

	var victim *Chunk
	var victimRemain int64
	for _, c := range p.chunks {
		if c.State != ChunkRunning || c.End < 0 {
			continue
		}
		remain := c.Remaining()
		if remain < StealMinRemain {
			continue
		}
		spd := speedBpsOf[c.Index]
		if spd > 0 {
			eta := time.Duration(float64(remain) / float64(spd) * float64(time.Second))
			if eta < StealMinETA {
				continue
			}
		} else if remain < StealMinRemain*2 {
			continue
		}
		if victim == nil || remain > victimRemain {
			victim = c
			victimRemain = remain
		}
	}
	if victim == nil {
		return nil
	}

	// Split: keep head with victim, give tail to thief.
	// writtenEnd = Start+Done-1; new boundary after written + half remaining (at least StealMinRemain/2 to thief)
	writtenEnd := victim.Start + victim.Done - 1
	if victim.Done == 0 {
		writtenEnd = victim.Start - 1
	}
	remainStart := writtenEnd + 1
	mid := remainStart + (victim.End-remainStart)/2
	if victim.End-mid < StealMinRemain/2 {
		mid = victim.End - StealMinRemain/2
	}
	if mid <= writtenEnd || mid >= victim.End {
		return nil
	}

	stolen := &Chunk{
		Index: p.nextID,
		Start: mid + 1,
		End:   victim.End,
		Done:  0,
		State: ChunkRunning,
		Owner: workerID,
	}
	p.nextID++
	victim.End = mid
	// Invariants: ranges non-overlapping
	p.chunks = append(p.chunks, stolen)
	cp := *stolen
	return &cp
}

// RewindForRecovery subtracts up to 1MiB from each incomplete chunk's Done.
func (p *ChunkPlan) RewindForRecovery() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.chunks {
		if c.State == ChunkDone {
			continue
		}
		if c.Done > CrashRewindBytes {
			c.Done -= CrashRewindBytes
		} else {
			c.Done = 0
		}
		if c.State == ChunkRunning {
			c.State = ChunkPending
			c.Owner = -1
		}
	}
}

// ValidateComplete checks the plan covers [0, size) and every chunk is fully done.
func (p *ChunkPlan) ValidateComplete(size int64) error {
	if err := p.ValidateNoOverlap(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.chunks) == 0 {
		return fmt.Errorf("no chunks")
	}
	var covered int64
	for _, c := range p.chunks {
		if c.State == ChunkStolen {
			continue
		}
		if c.State != ChunkDone {
			return fmt.Errorf("chunk %d state %s", c.Index, c.State)
		}
		if c.End < 0 {
			return fmt.Errorf("chunk %d open-ended", c.Index)
		}
		length := c.End - c.Start + 1
		if c.Done != length {
			return fmt.Errorf("chunk %d done %d != %d", c.Index, c.Done, length)
		}
		covered += length
	}
	if size > 0 && covered != size {
		return fmt.Errorf("coverage %d != size %d", covered, size)
	}
	return nil
}

// ValidateNoOverlap checks chunk range invariants (for tests).
func (p *ChunkPlan) ValidateNoOverlap() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	type span struct{ s, e int64 }
	var spans []span
	for _, c := range p.chunks {
		if c.State == ChunkStolen {
			continue
		}
		if c.End >= 0 && c.End < c.Start {
			return fmt.Errorf("chunk %d end < start", c.Index)
		}
		if c.Done < 0 {
			return fmt.Errorf("chunk %d negative done", c.Index)
		}
		if c.End >= 0 {
			total := c.End - c.Start + 1
			if c.Done > total {
				return fmt.Errorf("chunk %d done > size", c.Index)
			}
		}
		spans = append(spans, span{c.Start, c.End})
	}
	for i := 0; i < len(spans); i++ {
		for j := i + 1; j < len(spans); j++ {
			a, b := spans[i], spans[j]
			if a.e < 0 || b.e < 0 {
				continue
			}
			if a.s <= b.e && b.s <= a.e {
				return fmt.Errorf("overlap between [%d,%d] and [%d,%d]", a.s, a.e, b.s, b.e)
			}
		}
	}
	return nil
}
