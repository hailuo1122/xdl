package engine

import (
	"sync"
	"time"
)

// SuggestConns picks an initial connection count from probe results.
// requested is the user/task ceiling (already clamped to turbo max by the caller).
func SuggestConns(pr *ProbeResult, requested int, turbo bool) int {
	max := MaxConnsNormal
	if turbo {
		max = MaxConnsTurbo
	}
	if requested <= 0 {
		requested = DefaultConns
	}
	if requested > max {
		requested = max
	}
	if pr == nil || pr.SingleStream || !pr.AcceptRanges || pr.Size <= 0 || pr.Size < MinChunkSize {
		return 1
	}

	var suggest int
	switch {
	case pr.Size < 4<<20:
		suggest = 1
	case pr.Size < 16<<20:
		suggest = 2
	case pr.Size < 64<<20:
		suggest = 4
	case pr.Size < 256<<20:
		suggest = 8
	case pr.Size < 1<<30:
		suggest = 12
	default:
		suggest = 16
	}
	if turbo && pr.Size >= 256<<20 {
		suggest *= 2
	}

	// First-byte sample nudges the size tier; it must not gut an explicit
	// multi-conn width on a fast LAN (localhost / gigabit), only rein in
	// turbo-scale parallelism that adds overhead without filling the pipe.
	if pr.SampleBps > 0 {
		switch {
		case pr.SampleBps >= 80<<20: // ≥80 MiB/s single stream
			if suggest > 8 {
				suggest = 8
			}
		case pr.SampleBps >= 20<<20:
			if suggest > 16 {
				suggest = 16
			}
		case pr.SampleBps < 1<<20: // <1 MiB/s — open up toward ceiling
			if suggest < requested && suggest < 8 {
				suggest = minInt(requested, 8)
			}
		}
	}

	if suggest > requested {
		suggest = requested
	}
	if suggest > max {
		suggest = max
	}
	if suggest < 1 {
		suggest = 1
	}
	return suggest
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// connAdapter may spawn more workers when the aggregate speed plateaus below
// what the remaining work could absorb, up to a fixed ceiling.
type connAdapter struct {
	mu        sync.Mutex
	max       int
	started   int
	lastDone  int64
	lastAt    time.Time
	lastSpeed int64
	stalls    int
	launch    func() // starts one additional worker; may be nil once capped
}

func newConnAdapter(initial, max int, launch func()) *connAdapter {
	if max < initial {
		max = initial
	}
	return &connAdapter{
		max:     max,
		started: initial,
		lastAt:  time.Now(),
		launch:  launch,
	}
}

// Tick observes progress. Returns how many workers were added (0 or 1).
func (a *connAdapter) Tick(done, total int64) int {
	if a == nil {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.launch == nil || a.started >= a.max {
		return 0
	}
	now := time.Now()
	elapsed := now.Sub(a.lastAt).Seconds()
	if elapsed < 1.5 {
		return 0
	}
	delta := done - a.lastDone
	var spd int64
	if elapsed > 0 {
		spd = int64(float64(delta) / elapsed)
	}
	prev := a.lastSpeed
	a.lastDone = done
	a.lastAt = now
	a.lastSpeed = spd

	remain := total - done
	if total <= 0 || remain < MinChunkSize*2 {
		return 0
	}
	// Plateau: speed not growing ≥15% while plenty of work remains.
	if prev > 0 && spd > 0 && spd < prev+prev/7 {
		a.stalls++
	} else {
		a.stalls = 0
	}
	if a.stalls < 2 {
		return 0
	}
	a.stalls = 0
	a.started++
	launch := a.launch
	if a.started >= a.max {
		a.launch = nil
	}
	a.mu.Unlock()
	launch()
	a.mu.Lock()
	return 1
}
