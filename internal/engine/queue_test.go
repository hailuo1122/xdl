package engine

import "testing"

func TestCanStartDomainAndSerial(t *testing.T) {
	m := &Manager{
		tasks: map[TaskID]*Task{
			"a": {ID: "a", URL: "https://cdn.example.com/a.bin", State: StateRunning, Name: "a.bin"},
			"b": {ID: "b", URL: "https://cdn.example.com/b.bin", State: StateQueued, Name: "b.bin"},
			"c": {ID: "c", URL: "https://other.example.org/c.iso", State: StateRunning, Name: "c.iso"},
			"d": {ID: "d", URL: "https://mirror.example.org/d.iso", State: StateQueued, Name: "d.iso"},
		},
		rules: QueueRules{
			DomainMax: 1,
			Ext:       []ExtRule{{Ext: "iso", Action: "serial"}},
		},
	}
	if m.canStartLocked(m.tasks["b"]) {
		t.Fatal("same host should be blocked")
	}
	if m.canStartLocked(m.tasks["d"]) {
		t.Fatal("serial iso should be blocked while another iso runs")
	}
	m.tasks["a"].State = StateDone
	if !m.canStartLocked(m.tasks["b"]) {
		t.Fatal("host free after done")
	}
	m.tasks["c"].State = StateDone
	if !m.canStartLocked(m.tasks["d"]) {
		t.Fatal("iso free after done")
	}
}
