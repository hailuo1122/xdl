package grab

import (
	"bufio"
	"io"
	"strings"
)

// ParseURLList reads newline / whitespace separated URLs from text.
// Lines starting with # are comments. Returns normalized unique http(s) URLs.
func ParseURLList(text string) []string {
	return ParseURLReader(strings.NewReader(text))
}

// ParseURLReader streams URLs from r.
func ParseURLReader(r io.Reader) []string {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var raw []string
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Allow space-separated URLs on one line.
		fields := strings.Fields(line)
		raw = append(raw, fields...)
	}
	return Dedup(raw)
}
