package settings

import "strings"

// DefaultExtPolicies keeps huge disk images serial and installers behind other work.
func DefaultExtPolicies() []ExtPolicy {
	return []ExtPolicy{
		{Ext: "iso", Action: "serial"},
		{Ext: "img", Action: "serial"},
		{Ext: "exe", Action: "low"},
		{Ext: "msi", Action: "low"},
	}
}

// NormalizeExtPolicies drops blanks and unknown actions.
func NormalizeExtPolicies(in []ExtPolicy) []ExtPolicy {
	if in == nil {
		return nil
	}
	out := make([]ExtPolicy, 0, len(in))
	seen := map[string]struct{}{}
	for _, p := range in {
		ext := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(p.Ext), "."))
		if ext == "" || strings.ContainsAny(ext, "/\\") {
			continue
		}
		if _, ok := seen[ext]; ok {
			continue
		}
		action := strings.ToLower(strings.TrimSpace(p.Action))
		switch action {
		case "normal", "high", "low", "skip", "serial":
		default:
			continue
		}
		seen[ext] = struct{}{}
		out = append(out, ExtPolicy{Ext: ext, Action: action})
		if len(out) >= 64 {
			break
		}
	}
	return out
}

// ExtAction returns the policy action for a filename or URL path.
func ExtAction(policies []ExtPolicy, nameOrURL string) string {
	ext := extFromName(nameOrURL)
	if ext == "" {
		ext = extFromURL(nameOrURL)
	}
	for _, p := range policies {
		if p.Ext == ext {
			return p.Action
		}
	}
	return "normal"
}

// PriorityFor maps an action onto the task priority scale (higher runs first).
func PriorityFor(action string) (priority int, skip bool) {
	switch action {
	case "high":
		return 2, false
	case "low":
		return -1, false
	case "skip":
		return 0, true
	default:
		return 0, false
	}
}
