package dash

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// Manifest is a static VOD MPD after DRM / dynamic checks.
type Manifest struct {
	BaseURL  string
	Variants []Variant
}

// Variant is one selectable video (+ optional audio) representation pair.
type Variant struct {
	ID         string
	Label      string
	Bandwidth  int
	Width      int
	Height     int
	Codecs     string
	MimeType   string
	InitURL    string
	MediaURLs  []string
	AudioInit  string
	AudioMedia []string
}

type mpdDoc struct {
	XMLName                   xml.Name `xml:"MPD"`
	Type                      string   `xml:"type,attr"`
	MediaPresentationDuration string   `xml:"mediaPresentationDuration,attr"`
	BaseURL                   string   `xml:"BaseURL"`
	Periods                   []period `xml:"Period"`
}

type period struct {
	Duration       string          `xml:"duration,attr"`
	BaseURL        string          `xml:"BaseURL"`
	AdaptationSets []adaptationSet `xml:"AdaptationSet"`
}

type adaptationSet struct {
	ContentType        string           `xml:"contentType,attr"`
	MimeType           string           `xml:"mimeType,attr"`
	Codecs             string           `xml:"codecs,attr"`
	Lang               string           `xml:"lang,attr"`
	BaseURL            string           `xml:"BaseURL"`
	ContentProtection  []struct{}       `xml:"ContentProtection"`
	SegmentTemplate    *segmentTemplate `xml:"SegmentTemplate"`
	SegmentList        *segmentList     `xml:"SegmentList"`
	Representations    []representation `xml:"Representation"`
}

type representation struct {
	ID              string           `xml:"id,attr"`
	Bandwidth       int              `xml:"bandwidth,attr"`
	Width           int              `xml:"width,attr"`
	Height          int              `xml:"height,attr"`
	Codecs          string           `xml:"codecs,attr"`
	MimeType        string           `xml:"mimeType,attr"`
	BaseURL         string           `xml:"BaseURL"`
	ContentProtect  []struct{}       `xml:"ContentProtection"`
	SegmentTemplate *segmentTemplate `xml:"SegmentTemplate"`
	SegmentList     *segmentList     `xml:"SegmentList"`
}

type segmentTemplate struct {
	Timescale       uint64           `xml:"timescale,attr"`
	Duration        uint64           `xml:"duration,attr"`
	StartNumber     uint64           `xml:"startNumber,attr"`
	Initialization  string           `xml:"initialization,attr"`
	Media           string           `xml:"media,attr"`
	SegmentTimeline *segmentTimeline `xml:"SegmentTimeline"`
}

type segmentTimeline struct {
	S []timelineS `xml:"S"`
}

type timelineS struct {
	T *uint64 `xml:"t,attr"`
	D uint64  `xml:"d,attr"`
	R *int64  `xml:"r,attr"`
}

type segmentList struct {
	Initialization *segURL   `xml:"Initialization"`
	SegmentURLs    []segURL  `xml:"SegmentURL"`
}

type segURL struct {
	SourceURL string `xml:"sourceURL,attr"`
	Media     string `xml:"media,attr"`
}

var durationRE = regexp.MustCompile(`(?i)PT(?:(\d+)H)?(?:(\d+)M)?(?:(\d+(?:\.\d+)?)S)?`)

// Parse reads a static VOD MPD. Dynamic manifests and ContentProtection are rejected.
func Parse(r io.Reader, docURL string) (*Manifest, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var doc mpdDoc
	if err := xml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("MPD 解析失败：%w", err)
	}
	if strings.EqualFold(strings.TrimSpace(doc.Type), "dynamic") {
		return nil, fmt.Errorf("动态 MPD / 直播不受支持")
	}
	base := joinBase(docURL, doc.BaseURL)
	out := &Manifest{BaseURL: base}
	if len(doc.Periods) == 0 {
		return nil, fmt.Errorf("MPD 没有 Period")
	}
	// First period only (v1 scope).
	p := doc.Periods[0]
	periodBase := joinBase(base, p.BaseURL)
	var videos, audios []adaptationSet
	for _, as := range p.AdaptationSets {
		if len(as.ContentProtection) > 0 {
			return nil, fmt.Errorf("检测到 ContentProtection（Widevine/PlayReady 等 DRM），XDL 不支持绕过")
		}
		for _, rep := range as.Representations {
			if len(rep.ContentProtect) > 0 {
				return nil, fmt.Errorf("检测到 ContentProtection（Widevine/PlayReady 等 DRM），XDL 不支持绕过")
			}
		}
		ct := strings.ToLower(as.ContentType)
		mt := strings.ToLower(as.MimeType)
		switch {
		case strings.HasPrefix(ct, "video") || strings.HasPrefix(mt, "video"):
			videos = append(videos, as)
		case strings.HasPrefix(ct, "audio") || strings.HasPrefix(mt, "audio"):
			audios = append(audios, as)
		default:
			// Infer from representation mime / codecs.
			if len(as.Representations) > 0 {
				rm := strings.ToLower(as.Representations[0].MimeType)
				if strings.HasPrefix(rm, "video") {
					videos = append(videos, as)
				} else if strings.HasPrefix(rm, "audio") {
					audios = append(audios, as)
				}
			}
		}
	}
	if len(videos) == 0 {
		return nil, fmt.Errorf("MPD 没有视频 AdaptationSet")
	}
	var audioPick *expanded
	if len(audios) > 0 {
		as := audios[0]
		asBase := joinBase(periodBase, as.BaseURL)
		best := pickBestRep(as.Representations)
		ex, err := expandRep(as, best, asBase, doc.MediaPresentationDuration, p.Duration)
		if err != nil {
			return nil, err
		}
		audioPick = ex
	}
	vas := videos[0]
	vasBase := joinBase(periodBase, vas.BaseURL)
	for _, rep := range vas.Representations {
		ex, err := expandRep(vas, rep, vasBase, doc.MediaPresentationDuration, p.Duration)
		if err != nil {
			return nil, err
		}
		v := Variant{
			ID:        rep.ID,
			Bandwidth: rep.Bandwidth,
			Width:     rep.Width,
			Height:    rep.Height,
			Codecs:    firstNonEmpty(rep.Codecs, vas.Codecs),
			MimeType:  firstNonEmpty(rep.MimeType, vas.MimeType),
			InitURL:   ex.Init,
			MediaURLs: ex.Media,
		}
		v.Label = variantLabel(v)
		if audioPick != nil {
			v.AudioInit = audioPick.Init
			v.AudioMedia = audioPick.Media
		}
		out.Variants = append(out.Variants, v)
	}
	if len(out.Variants) == 0 {
		return nil, fmt.Errorf("MPD 没有可用清晰度")
	}
	return out, nil
}

type expanded struct {
	Init  string
	Media []string
}

func expandRep(as adaptationSet, rep representation, base, mpdDur, periodDur string) (*expanded, error) {
	repBase := joinBase(base, rep.BaseURL)
	tpl := rep.SegmentTemplate
	if tpl == nil {
		tpl = as.SegmentTemplate
	}
	list := rep.SegmentList
	if list == nil {
		list = as.SegmentList
	}
	if list != nil {
		return expandList(list, repBase)
	}
	if tpl != nil {
		return expandTemplate(tpl, rep, repBase, mpdDur, periodDur)
	}
	if rep.BaseURL != "" {
		abs, err := resolve(repBase, rep.BaseURL)
		if err != nil {
			return nil, err
		}
		return &expanded{Media: []string{abs}}, nil
	}
	return nil, fmt.Errorf("Representation %s 缺少 SegmentTemplate/List", rep.ID)
}

func expandList(list *segmentList, base string) (*expanded, error) {
	out := &expanded{}
	if list.Initialization != nil {
		src := firstNonEmpty(list.Initialization.SourceURL, list.Initialization.Media)
		if src != "" {
			u, err := resolve(base, src)
			if err != nil {
				return nil, err
			}
			out.Init = u
		}
	}
	for _, s := range list.SegmentURLs {
		src := firstNonEmpty(s.Media, s.SourceURL)
		if src == "" {
			continue
		}
		u, err := resolve(base, src)
		if err != nil {
			return nil, err
		}
		out.Media = append(out.Media, u)
	}
	if len(out.Media) == 0 {
		return nil, fmt.Errorf("SegmentList 为空")
	}
	return out, nil
}

func expandTemplate(tpl *segmentTemplate, rep representation, base, mpdDur, periodDur string) (*expanded, error) {
	out := &expanded{}
	repl := func(tmpl string) string {
		s := tmpl
		s = strings.ReplaceAll(s, "$RepresentationID$", rep.ID)
		s = strings.ReplaceAll(s, "$Bandwidth$", strconv.Itoa(rep.Bandwidth))
		return s
	}
	if tpl.Initialization != "" {
		u, err := resolve(base, repl(tpl.Initialization))
		if err != nil {
			return nil, err
		}
		out.Init = u
	}
	start := uint64(1)
	if tpl.StartNumber != 0 {
		start = tpl.StartNumber
	}
	mediaTmpl := tpl.Media
	if mediaTmpl == "" {
		return nil, fmt.Errorf("SegmentTemplate 缺少 media")
	}
	if tpl.SegmentTimeline != nil && len(tpl.SegmentTimeline.S) > 0 {
		n := start
		for _, s := range tpl.SegmentTimeline.S {
			repCount := int64(0)
			if s.R != nil {
				repCount = *s.R
			}
			for i := int64(0); i <= repCount; i++ {
				u, err := resolve(base, replaceNumber(repl(mediaTmpl), n))
				if err != nil {
					return nil, err
				}
				out.Media = append(out.Media, u)
				n++
			}
		}
	} else {
		dur := tpl.Duration
		scale := tpl.Timescale
		if scale == 0 {
			scale = 1
		}
		total := parseDurationSeconds(firstNonEmpty(periodDur, mpdDur))
		if dur == 0 || total <= 0 {
			return nil, fmt.Errorf("无法展开 SegmentTemplate（缺少 duration / timeline）")
		}
		segDur := float64(dur) / float64(scale)
		count := int(total/segDur + 0.999)
		if count < 1 {
			count = 1
		}
		if count > 20000 {
			return nil, fmt.Errorf("分片数量过多（%d），可能不是 VOD", count)
		}
		for i := 0; i < count; i++ {
			n := start + uint64(i)
			u, err := resolve(base, replaceNumber(repl(mediaTmpl), n))
			if err != nil {
				return nil, err
			}
			out.Media = append(out.Media, u)
		}
	}
	if len(out.Media) == 0 {
		return nil, fmt.Errorf("展开后没有媒体分片")
	}
	return out, nil
}

func replaceNumber(tmpl string, n uint64) string {
	re := regexp.MustCompile(`\$Number(?:%0(\d+)d)?\$`)
	return re.ReplaceAllStringFunc(tmpl, func(m string) string {
		sub := re.FindStringSubmatch(m)
		if len(sub) == 2 && sub[1] != "" {
			w, _ := strconv.Atoi(sub[1])
			return fmt.Sprintf("%0*d", w, n)
		}
		return strconv.FormatUint(n, 10)
	})
}

func parseDurationSeconds(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	m := durationRE.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	h, _ := strconv.ParseFloat(m[1], 64)
	min, _ := strconv.ParseFloat(m[2], 64)
	sec, _ := strconv.ParseFloat(m[3], 64)
	return h*3600 + min*60 + sec
}

func pickBestRep(reps []representation) representation {
	if len(reps) == 0 {
		return representation{}
	}
	best := reps[0]
	for _, r := range reps[1:] {
		if r.Bandwidth > best.Bandwidth {
			best = r
		}
	}
	return best
}

func variantLabel(v Variant) string {
	if v.Height > 0 {
		return fmt.Sprintf("%dp", v.Height)
	}
	if v.Width > 0 && v.Height > 0 {
		return fmt.Sprintf("%dx%d", v.Width, v.Height)
	}
	if v.Bandwidth > 0 {
		return fmt.Sprintf("%dk", v.Bandwidth/1000)
	}
	if v.ID != "" {
		return v.ID
	}
	return "default"
}

func joinBase(base, rel string) string {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return base
	}
	u, err := resolve(base, rel)
	if err != nil {
		return base
	}
	// Keep directory form for further joins when BaseURL is a path prefix.
	if strings.HasSuffix(rel, "/") && !strings.HasSuffix(u, "/") {
		u += "/"
	}
	return u
}

func resolve(base, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return base, nil
	}
	u, err := url.Parse(ref)
	if err != nil {
		return "", err
	}
	if u.IsAbs() {
		return u.String(), nil
	}
	b, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	return b.ResolveReference(u).String(), nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// SuggestName builds an output filename for a DASH variant.
func SuggestName(mpdURL, label string) string {
	u, err := url.Parse(mpdURL)
	stem := "video"
	if err == nil {
		base := path.Base(u.Path)
		base = strings.TrimSuffix(base, path.Ext(base))
		if base != "" && base != "." {
			stem = base
		}
	}
	if label != "" {
		stem = stem + " [" + label + "]"
	}
	return stem + ".mp4"
}
