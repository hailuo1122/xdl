package engine

import (
	"net/url"
	"path"
	"strings"
)

// ExtRule is the engine copy of a per-extension queue policy.
type ExtRule struct {
	Ext    string
	Action string // normal, high, low, skip, serial
}

// QueueRules constrains which queued tasks may start.
type QueueRules struct {
	DomainMax int
	Ext       []ExtRule
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func extOf(name, rawURL string) string {
	leaf := name
	if leaf == "" {
		if u, err := url.Parse(rawURL); err == nil {
			leaf = u.Path
		}
	}
	leaf = path.Base(strings.ReplaceAll(leaf, "\\", "/"))
	i := strings.LastIndex(leaf, ".")
	if i < 0 || i == len(leaf)-1 {
		return ""
	}
	return strings.ToLower(leaf[i+1:])
}

func (m *Manager) canStartLocked(t *Task) bool {
	if t == nil {
		return false
	}
	host := hostOf(t.URL)
	if host == "" {
		host = hostOf(t.FinalURL)
	}
	if m.rules.DomainMax > 0 && host != "" && m.countHostLocked(host) >= m.rules.DomainMax {
		return false
	}
	ext := extOf(t.Name, t.URL)
	if ext != "" && extSerial(m.rules.Ext, ext) && m.countExtLocked(ext) >= 1 {
		return false
	}
	return true
}

func extSerial(rules []ExtRule, ext string) bool {
	for _, r := range rules {
		if r.Ext == ext && r.Action == "serial" {
			return true
		}
	}
	return false
}

func (m *Manager) countHostLocked(host string) int {
	n := 0
	for _, t := range m.tasks {
		if !busyState(t.State) {
			continue
		}
		if hostOf(t.URL) == host || hostOf(t.FinalURL) == host {
			n++
		}
	}
	return n
}

func (m *Manager) countExtLocked(ext string) int {
	n := 0
	for _, t := range m.tasks {
		if !busyState(t.State) {
			continue
		}
		if extOf(t.Name, t.URL) == ext {
			n++
		}
	}
	return n
}

func busyState(s State) bool {
	switch s {
	case StateRunning, StateResolving, StateVerifying:
		return true
	default:
		return false
	}
}
