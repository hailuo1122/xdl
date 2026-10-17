package dash

import (
	"strings"
	"testing"
)

func TestParseStatic(t *testing.T) {
	const mpd = `<?xml version="1.0"?>
<MPD type="static" mediaPresentationDuration="PT8S" minBufferTime="PT1S">
  <Period>
    <AdaptationSet contentType="video" mimeType="video/mp4">
      <Representation id="720" bandwidth="2400000" width="1280" height="720">
        <SegmentList>
          <Initialization sourceURL="init.mp4"/>
          <SegmentURL media="1.m4s"/>
          <SegmentURL media="2.m4s"/>
        </SegmentList>
      </Representation>
    </AdaptationSet>
  </Period>
</MPD>`
	man, err := Parse(strings.NewReader(mpd), "http://example.com/v/manifest.mpd")
	if err != nil {
		t.Fatal(err)
	}
	if len(man.Variants) != 1 {
		t.Fatalf("variants=%d", len(man.Variants))
	}
	v := man.Variants[0]
	if v.Height != 720 || len(v.MediaURLs) != 2 {
		t.Fatalf("%+v", v)
	}
	if !strings.HasSuffix(v.InitURL, "/v/init.mp4") {
		t.Fatalf("init=%s", v.InitURL)
	}
}

func TestRejectDRM(t *testing.T) {
	const mpd = `<?xml version="1.0"?>
<MPD type="static" mediaPresentationDuration="PT8S">
  <Period>
    <AdaptationSet contentType="video">
      <ContentProtection schemeIdUri="urn:uuid:edef8ba9-79d6-4ace-a3c8-27dcd51d21ed"/>
      <Representation id="1" bandwidth="1"><BaseURL>a.mp4</BaseURL></Representation>
    </AdaptationSet>
  </Period>
</MPD>`
	_, err := Parse(strings.NewReader(mpd), "http://example.com/drm.mpd")
	if err == nil || !strings.Contains(err.Error(), "ContentProtection") {
		t.Fatalf("expected DRM rejection, got %v", err)
	}
}

func TestRejectDynamic(t *testing.T) {
	const mpd = `<?xml version="1.0"?>
<MPD type="dynamic" mediaPresentationDuration="PT8S">
  <Period>
    <AdaptationSet contentType="video">
      <Representation id="1" bandwidth="1"><BaseURL>a.mp4</BaseURL></Representation>
    </AdaptationSet>
  </Period>
</MPD>`
	_, err := Parse(strings.NewReader(mpd), "http://example.com/live.mpd")
	if err == nil {
		t.Fatal("expected dynamic rejection")
	}
}
