package job

import "encoding/json"

// Meta is stored in engine.Task.KindMeta for HLS/DASH tasks.
type Meta struct {
	Protocol   string   `json:"protocol"` // hls | dash
	VariantID  string   `json:"variantId,omitempty"`
	MediaURL   string   `json:"mediaUrl"`
	Label      string   `json:"label,omitempty"`
	Bandwidth  int      `json:"bandwidth,omitempty"`
	Width      int      `json:"width,omitempty"`
	Height     int      `json:"height,omitempty"`
	Container  string   `json:"container"` // ts | fmp4
	InitURL    string   `json:"initUrl,omitempty"`
	AudioInit  string   `json:"audioInit,omitempty"`
	AudioMedia []string `json:"audioMedia,omitempty"`
	SegURLs    []string `json:"segUrls,omitempty"` // dash absolute URLs; hls filled after resolve
	WorkDir    string   `json:"workDir,omitempty"`
	SegDone    []bool   `json:"segDone,omitempty"`
	InitDone   bool     `json:"initDone,omitempty"`
	AudioDone  []bool   `json:"audioDone,omitempty"`
}

func EncodeMeta(m Meta) (json.RawMessage, error) {
	return json.Marshal(m)
}

func DecodeMeta(raw json.RawMessage) (Meta, error) {
	var m Meta
	if len(raw) == 0 {
		return m, nil
	}
	err := json.Unmarshal(raw, &m)
	return m, err
}
