package engine

import "testing"

func TestSuggestConnsSizeTiers(t *testing.T) {
	cases := []struct {
		size int64
		want int
	}{
		{1 << 20, 1},
		{8 << 20, 2},
		{32 << 20, 4},
		{128 << 20, 8},
		{512 << 20, 12},
		{2 << 30, 16},
	}
	for _, tc := range cases {
		pr := &ProbeResult{Size: tc.size, AcceptRanges: true}
		got := SuggestConns(pr, 32, false)
		if got != tc.want {
			t.Fatalf("size=%d got %d want %d", tc.size, got, tc.want)
		}
	}
}

func TestSuggestConnsRespectsCeilingAndSingleStream(t *testing.T) {
	pr := &ProbeResult{Size: 2 << 30, AcceptRanges: true}
	if got := SuggestConns(pr, 4, false); got != 4 {
		t.Fatalf("ceiling: got %d", got)
	}
	pr.SingleStream = true
	if got := SuggestConns(pr, 16, true); got != 1 {
		t.Fatalf("single: got %d", got)
	}
}

func TestSuggestConnsSampleBpsFastLAN(t *testing.T) {
	pr := &ProbeResult{Size: 2 << 30, AcceptRanges: true, SampleBps: 100 << 20}
	got := SuggestConns(pr, 32, true)
	if got > 8 {
		t.Fatalf("fast LAN should rein in turbo width, got %d", got)
	}
	// Normal 64 MiB @ 8 conns must stay at 8 so we still open full width.
	pr64 := &ProbeResult{Size: 64 << 20, AcceptRanges: true, SampleBps: 100 << 20}
	if got := SuggestConns(pr64, 8, false); got != 8 {
		t.Fatalf("64MiB requested 8: got %d", got)
	}
}
