package engine

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	mirrorProbeTimeout = 8 * time.Second
	mirrorRaceBytes    = 256 << 10 // 256 KiB race sample
	mirrorMaxSources   = 8
)

// sourceBank tracks alternate URLs for the same resource and picks by EWMA speed.
type sourceBank struct {
	mu      sync.Mutex
	sources []*mirrorSource
}

type mirrorSource struct {
	url     string
	ewmaBps float64
	fails   int
	dead    bool
}

func newSourceBank(primary string, mirrors []string) *sourceBank {
	seen := map[string]struct{}{}
	var urls []string
	add := func(u string) {
		u = strings.TrimSpace(u)
		if u == "" {
			return
		}
		key := strings.ToLower(u)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		urls = append(urls, u)
	}
	add(primary)
	for _, m := range mirrors {
		add(m)
		if len(urls) >= mirrorMaxSources {
			break
		}
	}
	b := &sourceBank{sources: make([]*mirrorSource, 0, len(urls))}
	for _, u := range urls {
		b.sources = append(b.sources, &mirrorSource{url: u})
	}
	return b
}

func (b *sourceBank) Len() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.sources)
}

func (b *sourceBank) URLs() []string {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.sources))
	for i, s := range b.sources {
		out[i] = s.url
	}
	return out
}

// Pick returns the currently preferred URL (highest EWMA, fewest fails).
func (b *sourceBank) Pick() string {
	if b == nil {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	var best *mirrorSource
	for _, s := range b.sources {
		if s.dead {
			continue
		}
		if best == nil {
			best = s
			continue
		}
		if s.ewmaBps > best.ewmaBps*1.05 {
			best = s
			continue
		}
		if s.ewmaBps >= best.ewmaBps*0.95 && s.fails < best.fails {
			best = s
		}
	}
	if best == nil {
		// All marked dead — revive the least-failed for another try.
		for _, s := range b.sources {
			if best == nil || s.fails < best.fails {
				best = s
			}
		}
		if best != nil {
			best.dead = false
		}
	}
	if best == nil {
		return ""
	}
	return best.url
}

func (b *sourceBank) ReportSuccess(url string, bps int64) {
	if b == nil || url == "" || bps <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, s := range b.sources {
		if s.url != url {
			continue
		}
		s.fails = 0
		s.dead = false
		if s.ewmaBps <= 0 {
			s.ewmaBps = float64(bps)
		} else {
			s.ewmaBps = s.ewmaBps*0.7 + float64(bps)*0.3
		}
		return
	}
}

func (b *sourceBank) ReportFailure(url string) {
	if b == nil || url == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	alive := 0
	for _, s := range b.sources {
		if s.url == url {
			s.fails++
			s.ewmaBps *= 0.5
			if s.fails >= 3 {
				s.dead = true
			}
		}
		if !s.dead {
			alive++
		}
	}
	// Never leave the bank empty of candidates.
	if alive == 0 {
		for _, s := range b.sources {
			s.dead = false
		}
	}
}

type mirrorProbe struct {
	url   string
	probe *ProbeResult
	bps   int64
	err   error
}

// resolveMirrors probes primary + mirrors, keeps size-compatible sources, and
// ranks them with a short byte race. Returns the winning probe (best source)
// and a bank for workers to rotate across.
func resolveMirrors(
	ctx context.Context,
	client *http.Client,
	primary string,
	mirrors []string,
	headers http.Header,
) (*ProbeResult, *sourceBank, error) {
	bank := newSourceBank(primary, mirrors)
	urls := bank.URLs()
	if len(urls) <= 1 {
		pr, err := Probe(ctx, client, primary, headers)
		return pr, bank, err
	}

	type result struct {
		idx int
		mp  mirrorProbe
	}
	ch := make(chan result, len(urls))
	for i, u := range urls {
		i, u := i, u
		go func() {
			pctx, cancel := context.WithTimeout(ctx, mirrorProbeTimeout)
			defer cancel()
			pr, err := Probe(pctx, client, u, headers)
			ch <- result{idx: i, mp: mirrorProbe{url: u, probe: pr, err: err}}
		}()
	}

	probes := make([]mirrorProbe, len(urls))
	var primaryProbe *ProbeResult
	var primaryErr error
	for range urls {
		r := <-ch
		probes[r.idx] = r.mp
		if r.mp.url == primary || (primary == urls[0] && r.idx == 0) {
			primaryProbe = r.mp.probe
			primaryErr = r.mp.err
		}
	}
	if primaryProbe == nil && len(probes) > 0 {
		primaryProbe = probes[0].probe
		primaryErr = probes[0].err
	}

	// Reference validator: prefer a successful primary, else first success.
	var ref *ProbeResult
	if primaryProbe != nil && primaryErr == nil {
		ref = primaryProbe
	} else {
		for _, mp := range probes {
			if mp.err == nil && mp.probe != nil {
				ref = mp.probe
				primaryErr = nil
				break
			}
		}
	}
	if ref == nil {
		if primaryErr != nil {
			return nil, bank, primaryErr
		}
		return nil, bank, NewTaskError(ErrCodeProbeFailed, "镜像探测全部失败", "检查链接后重试")
	}

	compatible := make([]mirrorProbe, 0, len(probes))
	for _, mp := range probes {
		if mp.err != nil || mp.probe == nil {
			continue
		}
		if !mirrorCompatible(ref, mp.probe) {
			continue
		}
		compatible = append(compatible, mp)
	}
	if len(compatible) == 0 {
		compatible = []mirrorProbe{{url: ref.FinalURL, probe: ref}}
	}

	// Byte race among compatible sources to seed EWMA and pick the winner.
	raceCtx, cancel := context.WithTimeout(ctx, mirrorProbeTimeout)
	defer cancel()
	var raceWg sync.WaitGroup
	var raceMu sync.Mutex
	for i := range compatible {
		i := i
		raceWg.Add(1)
		go func() {
			defer raceWg.Done()
			bps := raceSource(raceCtx, client, compatible[i].probe.FinalURL, headers)
			raceMu.Lock()
			compatible[i].bps = bps
			raceMu.Unlock()
		}()
	}
	raceWg.Wait()

	sort.SliceStable(compatible, func(i, j int) bool {
		return compatible[i].bps > compatible[j].bps
	})

	bank = &sourceBank{sources: make([]*mirrorSource, 0, len(compatible))}
	for _, mp := range compatible {
		bank.sources = append(bank.sources, &mirrorSource{
			url:     mp.probe.FinalURL,
			ewmaBps: float64(mp.bps),
		})
	}

	winner := compatible[0].probe
	// Keep original URL field semantics: FinalURL is the raced winner.
	return winner, bank, nil
}

func mirrorCompatible(ref, other *ProbeResult) bool {
	if ref == nil || other == nil {
		return false
	}
	if ref.Size > 0 && other.Size > 0 && ref.Size != other.Size {
		return false
	}
	// Soft ETag check: if both present and disagree, reject.
	if ref.ETag != "" && other.ETag != "" && ref.ETag != other.ETag {
		return false
	}
	if !other.AcceptRanges && ref.AcceptRanges && ref.Size >= MinChunkSize {
		// Mirror cannot do multi-conn Range — still usable as single-stream fallback,
		// but skip for the parallel bank.
		return false
	}
	return true
}

func raceSource(ctx context.Context, client *http.Client, rawURL string, headers http.Header) int64 {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0
	}
	copyHeaders(req.Header, headers)
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Range", "bytes=0-"+itoa(mirrorRaceBytes-1))
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", defaultUA)
	}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return 0
	}
	buf := make([]byte, 32*1024)
	var n int64
	for n < mirrorRaceBytes {
		if err := ctx.Err(); err != nil {
			break
		}
		nr, readErr := resp.Body.Read(buf)
		n += int64(nr)
		if readErr != nil {
			break
		}
	}
	elapsed := time.Since(start).Seconds()
	if elapsed <= 0 || n <= 0 {
		return 0
	}
	return int64(float64(n) / elapsed)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
