package grab

import "testing"

func TestParseURLList(t *testing.T) {
	text := `# comment
https://a.com/1.zip
https://a.com/1.zip?utm_source=x
https://b.com/2.mp4 https://c.com/3.pdf
ftp://skip
`
	got := ParseURLList(text)
	if len(got) != 3 {
		t.Fatalf("got %v", got)
	}
}
