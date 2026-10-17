package engine

import (
	"context"
	"sync"

	"golang.org/x/time/rate"
)

// Limiter is a token-bucket speed limiter. 0 means unlimited.
type Limiter struct {
	mu       sync.Mutex
	limiter  *rate.Limiter
	bps      int64
	burstMin int
}

func NewLimiter(bytesPerSec int64) *Limiter {
	l := &Limiter{burstMin: 128 * 1024}
	l.Set(bytesPerSec)
	return l
}

func (l *Limiter) Set(bytesPerSec int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.bps = bytesPerSec
	if bytesPerSec <= 0 {
		l.limiter = nil
		return
	}
	burst := int(bytesPerSec)
	if burst < l.burstMin {
		burst = l.burstMin
	}
	l.limiter = rate.NewLimiter(rate.Limit(bytesPerSec), burst)
}

func (l *Limiter) WaitN(ctx context.Context, n int) error {
	l.mu.Lock()
	lim := l.limiter
	l.mu.Unlock()
	if lim == nil || n <= 0 {
		return nil
	}
	return lim.WaitN(ctx, n)
}

func (l *Limiter) BytesPerSec() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.bps
}
