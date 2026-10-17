package app

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xdl-app/xdl/internal/engine"
)

// aria2 JSON-RPC (HTTP POST /jsonrpc) so browser extensions that "send to aria2"
// can hand URLs to XDL. The socket is loopback-only.

type ariaListener struct {
	mu  sync.Mutex
	srv *http.Server
	ln  net.Listener
}

func (rt *Runtime) restartAria2() {
	rt.stopAria2()
	cfg := rt.Settings()
	if !cfg.Aria2Enabled {
		return
	}
	addr := fmt.Sprintf("127.0.0.1:%d", cfg.Aria2Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		slog.Warn("aria2 rpc", "addr", addr, "err", err)
		return
	}
	srv := &http.Server{
		Handler:           rt.ariaHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	rt.ariaMu.Lock()
	rt.aria = &ariaListener{srv: srv, ln: ln}
	rt.ariaMu.Unlock()
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Warn("aria2 rpc serve", "err", err)
		}
	}()
	slog.Info("aria2 rpc listening", "addr", "http://"+addr+"/jsonrpc")
}

func (rt *Runtime) stopAria2() {
	rt.ariaMu.Lock()
	cur := rt.aria
	rt.aria = nil
	rt.ariaMu.Unlock()
	if cur == nil {
		return
	}
	cur.mu.Lock()
	defer cur.mu.Unlock()
	if cur.srv != nil {
		_ = cur.srv.Close()
	}
}

func (rt *Runtime) ariaHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Aria2-Secret")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeAriaErr(w, nil, -32700, "read error")
			return
		}
		if len(body) > 0 && body[0] == '[' {
			var batch []json.RawMessage
			if err := json.Unmarshal(body, &batch); err != nil {
				writeAriaErr(w, nil, -32700, "parse error")
				return
			}
			out := make([]any, 0, len(batch))
			for _, raw := range batch {
				out = append(out, rt.dispatchAria(r, raw))
			}
			writeAriaJSON(w, out)
			return
		}
		writeAriaJSON(w, rt.dispatchAria(r, body))
	})
}

type ariaCall struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func (rt *Runtime) dispatchAria(r *http.Request, raw json.RawMessage) any {
	var call ariaCall
	if err := json.Unmarshal(raw, &call); err != nil {
		return ariaFail(nil, -32700, "parse error")
	}
	params, err := decodeParams(call.Params)
	if err != nil {
		return ariaFail(call.ID, -32602, "invalid params")
	}
	secret := rt.Settings().Aria2Secret
	if !ariaAuthorized(r, secret, &params) {
		return ariaFail(call.ID, 1, "Unauthorized")
	}
	return rt.ariaExec(call.ID, call.Method, params)
}

func (rt *Runtime) ariaExec(id any, methodName string, params []json.RawMessage) any {
	method := strings.TrimPrefix(methodName, "aria2.")
	switch method {
	case "addUri":
		gid, err := rt.ariaAddURI(params)
		if err != nil {
			return ariaFail(id, 1, err.Error())
		}
		return ariaOK(id, gid)
	case "getVersion":
		return ariaOK(id, map[string]any{
			"version":         "1.37.0",
			"enabledFeatures": []string{"Async DNS", "BitTorrent", "Firefox3 Cookie", "GZip", "HTTPS", "Message Digest", "XML-RPC"},
		})
	case "tellStatus":
		gid, _ := paramString(params, 0)
		t, ok := rt.Mgr.Get(engine.TaskID(gid))
		if !ok {
			return ariaFail(id, 1, "GID "+gid+" is not found")
		}
		return ariaOK(id, ariaStatus(t))
	case "tellActive":
		return ariaOK(id, rt.ariaList(func(s engine.State) bool {
			return s == engine.StateRunning || s == engine.StateResolving || s == engine.StateVerifying
		}))
	case "tellWaiting":
		offset, num := pageParams(params, 0)
		list := rt.ariaList(func(s engine.State) bool {
			return s == engine.StateQueued || s == engine.StatePaused || s == engine.StateWaitingUser
		})
		return ariaOK(id, pageSlice(list, offset, num))
	case "tellStopped":
		offset, num := pageParams(params, 0)
		list := rt.ariaList(func(s engine.State) bool {
			return s == engine.StateDone || s == engine.StateFailed || s == engine.StateCanceled
		})
		return ariaOK(id, pageSlice(list, offset, num))
	case "pause":
		gid, _ := paramString(params, 0)
		if err := rt.Mgr.Pause(engine.TaskID(gid)); err != nil {
			return ariaFail(id, 1, err.Error())
		}
		return ariaOK(id, gid)
	case "unpause", "forcePause":
		if method == "forcePause" {
			gid, _ := paramString(params, 0)
			if err := rt.Mgr.Pause(engine.TaskID(gid)); err != nil {
				return ariaFail(id, 1, err.Error())
			}
			return ariaOK(id, gid)
		}
		gid, _ := paramString(params, 0)
		if err := rt.Mgr.Resume(engine.TaskID(gid)); err != nil {
			return ariaFail(id, 1, err.Error())
		}
		return ariaOK(id, gid)
	case "remove", "forceRemove":
		gid, _ := paramString(params, 0)
		if err := rt.Mgr.Remove(engine.TaskID(gid)); err != nil {
			return ariaFail(id, 1, err.Error())
		}
		return ariaOK(id, gid)
	case "pauseAll":
		for _, t := range rt.Mgr.List() {
			if t.State == engine.StateRunning || t.State == engine.StateQueued || t.State == engine.StateResolving {
				_ = rt.Mgr.Pause(t.ID)
			}
		}
		return ariaOK(id, "OK")
	case "unpauseAll":
		for _, t := range rt.Mgr.List() {
			if t.State == engine.StatePaused {
				_ = rt.Mgr.Resume(t.ID)
			}
		}
		return ariaOK(id, "OK")
	case "purgeDownloadResult", "removeDownloadResult":
		if method == "removeDownloadResult" {
			gid, _ := paramString(params, 0)
			if t, ok := rt.Mgr.Get(engine.TaskID(gid)); ok && (t.State == engine.StateDone || t.State == engine.StateFailed || t.State == engine.StateCanceled) {
				_ = rt.Mgr.Remove(t.ID)
			}
			return ariaOK(id, "OK")
		}
		for _, t := range rt.Mgr.List() {
			if t.State == engine.StateDone || t.State == engine.StateFailed || t.State == engine.StateCanceled {
				_ = rt.Mgr.Remove(t.ID)
			}
		}
		return ariaOK(id, "OK")
	case "getGlobalStat":
		var speed int64
		var active, waiting, stopped int
		for _, t := range rt.Mgr.List() {
			switch t.State {
			case engine.StateRunning, engine.StateResolving, engine.StateVerifying:
				active++
				speed += t.Speed
			case engine.StateQueued, engine.StatePaused, engine.StateWaitingUser:
				waiting++
			default:
				stopped++
			}
		}
		return ariaOK(id, map[string]string{
			"downloadSpeed":   strconv.FormatInt(speed, 10),
			"uploadSpeed":     "0",
			"numActive":       strconv.Itoa(active),
			"numWaiting":      strconv.Itoa(waiting),
			"numStopped":      strconv.Itoa(stopped),
			"numStoppedTotal": strconv.Itoa(stopped),
		})
	case "getGlobalOption", "getOption":
		return ariaOK(id, map[string]string{"dir": rt.Settings().DownloadDir})
	case "system.multicall":
		return ariaOK(id, rt.ariaMulticall(params))
	default:
		if strings.HasPrefix(methodName, "system.") && method != "system.multicall" {
			return ariaFail(id, -32601, "method not found")
		}
		return ariaFail(id, -32601, "method not found: "+methodName)
	}
}

func (rt *Runtime) ariaMulticall(params []json.RawMessage) []any {
	if len(params) == 0 {
		return nil
	}
	var calls []struct {
		MethodName string            `json:"methodName"`
		Params     []json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(params[0], &calls); err != nil {
		return []any{ariaFail(nil, -32602, "invalid params")}
	}
	out := make([]any, 0, len(calls))
	for _, c := range calls {
		switch res := rt.ariaExec("1", c.MethodName, c.Params).(type) {
		case map[string]any:
			if _, bad := res["error"]; bad {
				out = append(out, res)
			} else if result, ok := res["result"]; ok {
				out = append(out, []any{result})
			}
		default:
			out = append(out, res)
		}
	}
	return out
}
func (rt *Runtime) ariaAddURI(params []json.RawMessage) (string, error) {
	if len(params) == 0 {
		return "", fmt.Errorf("uris required")
	}
	var uris []string
	if err := json.Unmarshal(params[0], &uris); err != nil || len(uris) == 0 {
		return "", fmt.Errorf("uris required")
	}
	url := ""
	for _, u := range uris {
		u = strings.TrimSpace(u)
		if u != "" {
			url = u
			break
		}
	}
	if url == "" {
		return "", fmt.Errorf("empty uri")
	}
	req := AddRequest{URL: url}
	if len(params) > 1 {
		var opt map[string]any
		_ = json.Unmarshal(params[1], &opt)
		if d, ok := opt["dir"].(string); ok {
			req.Dir = strings.TrimSpace(d)
		}
		if name, ok := opt["out"].(string); ok {
			req.Name = strings.TrimSpace(name)
		}
		if n, ok := numberOpt(opt["split"]); ok && n > 0 {
			req.Conns = n
		}
		req.Headers = headerOpt(opt)
	}
	view, err := rt.Add(req, true)
	if err != nil {
		return "", err
	}
	return view.ID, nil
}

func (rt *Runtime) ariaList(match func(engine.State) bool) []map[string]any {
	var out []map[string]any
	for _, t := range rt.Mgr.List() {
		if match(t.State) {
			out = append(out, ariaStatus(t))
		}
	}
	if out == nil {
		out = []map[string]any{}
	}
	return out
}

func ariaStatus(t *engine.Task) map[string]any {
	status := "waiting"
	switch t.State {
	case engine.StateRunning, engine.StateResolving, engine.StateVerifying:
		status = "active"
	case engine.StatePaused, engine.StateWaitingUser:
		status = "paused"
	case engine.StateDone:
		status = "complete"
	case engine.StateFailed:
		status = "error"
	case engine.StateCanceled:
		status = "removed"
	}
	path := t.FinalPath
	if path == "" {
		path = t.Name
	}
	errMsg := ""
	if t.Err != nil {
		errMsg = t.Err.Message
	}
	uriStatus := "used"
	if status == "waiting" || status == "paused" {
		uriStatus = "waiting"
	}
	return map[string]any{
		"gid":             string(t.ID),
		"status":          status,
		"totalLength":     strconv.FormatInt(t.Size, 10),
		"completedLength": strconv.FormatInt(t.Done, 10),
		"uploadLength":    "0",
		"downloadSpeed":   strconv.FormatInt(t.Speed, 10),
		"uploadSpeed":     "0",
		"dir":             t.Dir,
		"errorCode":       "0",
		"errorMessage":    errMsg,
		"files": []any{map[string]any{
			"index":           "1",
			"path":            path,
			"length":          strconv.FormatInt(t.Size, 10),
			"completedLength": strconv.FormatInt(t.Done, 10),
			"selected":        "true",
			"uris": []any{map[string]string{
				"uri":    t.URL,
				"status": uriStatus,
			}},
		}},
	}
}

func ariaAuthorized(r *http.Request, secret string, params *[]json.RawMessage) bool {
	if secret == "" {
		return true
	}
	want := "token:" + secret
	if h := strings.TrimSpace(r.Header.Get("Authorization")); h != "" {
		h = strings.TrimPrefix(h, "Bearer ")
		if subtle.ConstantTimeCompare([]byte(h), []byte(secret)) == 1 || subtle.ConstantTimeCompare([]byte(h), []byte(want)) == 1 {
			return true
		}
	}
	if len(*params) == 0 {
		return false
	}
	var s string
	if json.Unmarshal((*params)[0], &s) == nil && subtle.ConstantTimeCompare([]byte(s), []byte(want)) == 1 {
		*params = (*params)[1:]
		return true
	}
	return false
}

func decodeParams(raw json.RawMessage) ([]json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var params []json.RawMessage
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, err
	}
	return params, nil
}

func paramString(params []json.RawMessage, i int) (string, bool) {
	if i >= len(params) {
		return "", false
	}
	var s string
	if err := json.Unmarshal(params[i], &s); err != nil {
		return "", false
	}
	return s, true
}

func pageParams(params []json.RawMessage, start int) (int, int) {
	offset, num := 0, 1000
	if s, ok := paramString(params, start); ok {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			offset = n
		}
	}
	if s, ok := paramString(params, start+1); ok {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			num = n
		}
	}
	return offset, num
}

func pageSlice(list []map[string]any, offset, num int) []map[string]any {
	if offset >= len(list) || num == 0 {
		return []map[string]any{}
	}
	end := offset + num
	if end > len(list) {
		end = len(list)
	}
	return list[offset:end]
}

func numberOpt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(n))
		return i, err == nil
	default:
		return 0, false
	}
}

func headerOpt(opt map[string]any) map[string]string {
	out := map[string]string{}
	if ref, ok := opt["referer"].(string); ok && ref != "" {
		out["Referer"] = ref
	}
	if ua, ok := opt["user-agent"].(string); ok && ua != "" {
		out["User-Agent"] = ua
	}
	raw, ok := opt["header"]
	if !ok {
		return out
	}
	switch h := raw.(type) {
	case string:
		addHeaderLine(out, h)
	case []any:
		for _, item := range h {
			if s, ok := item.(string); ok {
				addHeaderLine(out, s)
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func addHeaderLine(dst map[string]string, line string) {
	k, v, ok := strings.Cut(line, ":")
	if !ok {
		return
	}
	k = strings.TrimSpace(k)
	v = strings.TrimSpace(v)
	if k == "" || v == "" {
		return
	}
	dst[k] = v
}

func ariaOK(id, result any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
}

func ariaFail(id any, code int, msg string) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}}
}

func writeAriaJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func writeAriaErr(w http.ResponseWriter, id any, code int, msg string) {
	writeAriaJSON(w, ariaFail(id, code, msg))
}
