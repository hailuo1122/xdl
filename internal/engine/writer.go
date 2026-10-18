package engine

import (
	"context"
	"os"
	"sync"
)

const (
	// writerQueueDepth caps in-flight coalesced write buffers (each up to 1 MiB).
	writerQueueDepth = 16
)

// diskWriter serializes WriteAt onto one goroutine so network workers can keep
// reading while prior buffers drain to disk. Buffers are taken from writeBufPool
// and returned after the write completes.
type diskWriter struct {
	f     *os.File
	queue chan writeJob

	mu     sync.Mutex
	closed bool
	err    error

	wg       sync.WaitGroup
	inflight sync.WaitGroup
}

type writeJob struct {
	off  int64
	bufp *[]byte
	n    int
	errc chan error // nil = async; error recorded on diskWriter
}

func newDiskWriter(f *os.File) *diskWriter {
	w := &diskWriter{
		f:     f,
		queue: make(chan writeJob, writerQueueDepth),
	}
	w.wg.Add(1)
	go w.loop()
	return w
}

func (w *diskWriter) loop() {
	defer w.wg.Done()
	for job := range w.queue {
		w.doWrite(job)
	}
}

func (w *diskWriter) doWrite(job writeJob) {
	defer w.inflight.Done()
	var err error
	if job.n > 0 && job.bufp != nil {
		_, err = w.f.WriteAt((*job.bufp)[:job.n], job.off)
	}
	if job.bufp != nil {
		putWriteBuf(job.bufp)
	}
	if err != nil {
		w.setErr(err)
	}
	if job.errc != nil {
		job.errc <- err
	}
}

func (w *diskWriter) setErr(err error) {
	if err == nil {
		return
	}
	w.mu.Lock()
	if w.err == nil {
		w.err = err
	}
	w.mu.Unlock()
}

func (w *diskWriter) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

// Submit queues a pooled write buffer for offset off.
// Ownership of bufp transfers to the writer (returned to the pool after WriteAt).
// Blocks when the queue is full (backpressure) or until ctx is cancelled.
func (w *diskWriter) Submit(ctx context.Context, bufp *[]byte, n int, off int64) error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		if bufp != nil {
			putWriteBuf(bufp)
		}
		return os.ErrClosed
	}
	if w.err != nil {
		err := w.err
		w.mu.Unlock()
		if bufp != nil {
			putWriteBuf(bufp)
		}
		return err
	}
	w.inflight.Add(1)
	w.mu.Unlock()

	job := writeJob{off: off, bufp: bufp, n: n}
	select {
	case <-ctx.Done():
		w.inflight.Done()
		if bufp != nil {
			putWriteBuf(bufp)
		}
		return ctx.Err()
	case w.queue <- job:
		return nil
	}
}


// Flush waits until all submitted writes have finished.
func (w *diskWriter) Flush() error {
	w.inflight.Wait()
	return w.Err()
}

// Close rejects new submits, drains the queue, and stops the loop.
func (w *diskWriter) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return w.Err()
	}
	w.closed = true
	close(w.queue)
	w.mu.Unlock()
	w.wg.Wait()
	w.inflight.Wait()
	return w.Err()
}
