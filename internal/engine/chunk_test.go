package engine

import (
	"testing"
)

func TestNewChunkPlanSizes(t *testing.T) {
	size := int64(100 << 20) // 100 MiB
	plan := NewChunkPlan(size, 8)
	chunks := plan.Snapshot()
	if len(chunks) == 0 {
		t.Fatal("expected chunks")
	}
	var covered int64
	for _, c := range chunks {
		if c.End < c.Start {
			t.Fatalf("bad range %d-%d", c.Start, c.End)
		}
		covered += c.End - c.Start + 1
	}
	if covered != size {
		t.Fatalf("covered %d want %d", covered, size)
	}
	if err := plan.ValidateNoOverlap(); err != nil {
		t.Fatal(err)
	}
}

func TestWorkStealNoOverlap(t *testing.T) {
	size := int64(64 << 20)
	plan := NewChunkPlan(size, 2)
	c0 := plan.ClaimNext(0)
	if c0 == nil {
		t.Fatal("expected chunk")
	}
	// Simulate slow progress on first chunk
	plan.UpdateProgress(c0.Index, 1<<20)

	stolen := plan.TrySteal(1, map[int]int64{c0.Index: 100 * 1024}) // ~100KB/s → ETA high
	if stolen == nil {
		t.Fatal("expected steal")
	}
	if err := plan.ValidateNoOverlap(); err != nil {
		t.Fatal(err)
	}
	snaps := plan.Snapshot()
	var total int64
	for _, c := range snaps {
		total += c.End - c.Start + 1
	}
	if total != size {
		t.Fatalf("total span %d != size %d", total, size)
	}
}

func TestSanitizeFilename(t *testing.T) {
	cases := map[string]string{
		"a<b>.txt": "a_b_.txt",
		"CON":      "_CON",
		"":         "download",
		"  hi  ":   "hi",
	}
	for in, want := range cases {
		got := SanitizeFilename(in)
		if got != want {
			t.Errorf("SanitizeFilename(%q)=%q want %q", in, got, want)
		}
	}
}
