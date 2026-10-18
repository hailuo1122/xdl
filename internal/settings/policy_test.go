package settings

import "testing"

func TestExtPolicy(t *testing.T) {
	p := NormalizeExtPolicies([]ExtPolicy{
		{Ext: ".ISO", Action: "serial"},
		{Ext: "exe", Action: "nope"},
		{Ext: "zip", Action: "skip"},
		{Ext: "zip", Action: "high"},
	})
	if len(p) != 2 {
		t.Fatalf("%+v", p)
	}
	if ExtAction(p, "disk.iso") != "serial" {
		t.Fatal(ExtAction(p, "disk.iso"))
	}
	if ExtAction(p, "https://cdn.example/a.ZIP") != "skip" {
		t.Fatal("zip")
	}
	pri, skip := PriorityFor("skip")
	if !skip || pri != 0 {
		t.Fatalf("skip %d %v", pri, skip)
	}
	pri, skip = PriorityFor("high")
	if skip || pri != 2 {
		t.Fatalf("high %d", pri)
	}
}
