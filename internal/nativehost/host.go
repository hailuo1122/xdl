package nativehost

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/xdl-app/xdl/internal/ipc"
)

const (
	HostName       = "com.xdl.host"
	connectTimeout = 300 * time.Millisecond
	launchWait     = 12 * time.Second
	retryDelay     = 1500 * time.Millisecond
)

// DevExtensionID is the fixed extension identity derived from the RSA key in
// extension/.keys/public-key.txt. Store IDs are appended when published.
const DevExtensionID = "jkmmncllhpjaklglnieoaacgchndkgkd"

// StoreExtensionIDs is filled in once the Chrome Web Store / Edge Add-ons
// listings exist. Both are listed in allowed_origins alongside the dev ID.
var StoreExtensionIDs = []string{}

// AllowedOrigins lists every extension origin permitted to talk to the host.
func AllowedOrigins() []string {
	ids := append([]string{DevExtensionID}, StoreExtensionIDs...)
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, "chrome-extension://"+id+"/")
	}
	return out
}

func originAllowed(origin string) bool {
	if origin == "" {
		// Manual invocation from a terminal; still only talks to the user pipe.
		return true
	}
	for _, allowed := range AllowedOrigins() {
		if strings.EqualFold(origin, allowed) {
			return true
		}
	}
	return false
}

// AppPathSidecar holds the path of the full GUI executable next to the host
// copy so the short-lived host can launch the real app, not itself.
const AppPathSidecar = "app-path.txt"

func ReadAppPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(exe), AppPathSidecar))
	if err != nil {
		return ""
	}
	p := strings.TrimSpace(string(raw))
	if p == "" {
		return ""
	}
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

func WriteAppPath(dir, appExe string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, AppPathSidecar), []byte(appExe+"\r\n"), 0o644)
}

// Run is the --native-host entry: stdio ↔ named pipe bridge.
// Never downloads here; only forwards JSON-RPC and returns quickly.
func Run(exePath, origin string) int {
	if !originAllowed(origin) {
		_ = ipc.WriteJSON(os.Stdout, ipc.Fail(nil, ipc.ErrForbidden, "origin not allowed", map[string]string{
			"code":   "ORIGIN_FORBIDDEN",
			"origin": origin,
		}))
		return 1
	}

	for {
		var raw json.RawMessage
		if err := ipc.ReadJSON(os.Stdin, &raw); err != nil {
			return 0 // stdin closed
		}

		var req ipc.Request
		if err := json.Unmarshal(raw, &req); err != nil {
			_ = ipc.WriteJSON(os.Stdout, ipc.Fail(nil, ipc.ErrParse, "invalid json", nil))
			continue
		}

		resp, err := forward(req)
		if err != nil {
			// Passive status checks must not wake the desktop app. Real work
			// (downloads) and explicit launch requests do.
			if shouldLaunch(&req) {
				if launchErr := ensureApp(exePath); launchErr != nil {
					_ = ipc.WriteJSON(os.Stdout, ipc.Fail(req.ID, ipc.ErrAppStarting, "app not ready", map[string]string{
						"code": "APP_STARTING",
					}))
					continue
				}
				resp, err = forward(req)
			}
		}
		if err != nil {
			_ = ipc.WriteJSON(os.Stdout, ipc.Fail(req.ID, ipc.ErrAppOffline, "app offline", map[string]string{
				"code": "APP_OFFLINE",
			}))
			continue
		}
		if err := ipc.WriteJSON(os.Stdout, resp); err != nil {
			return 1
		}
	}
}

func shouldLaunch(req *ipc.Request) bool {
	switch req.Method {
	case ipc.MethodDownloadAdd, ipc.MethodMediaProbe, ipc.MethodMediaStart, ipc.MethodIntegrationRepair, ipc.MethodFSChooseDir:
		return true
	}
	var p struct {
		Launch bool `json:"launch"`
	}
	_ = json.Unmarshal(req.Params, &p)
	return p.Launch
}

func ensureApp(exePath string) error {
	if err := ipc.Ping(connectTimeout); err == nil {
		return nil
	}
	// Launch GUI detached
	if exePath == "" {
		exePath = ReadAppPath()
	}
	if exePath == "" {
		var err error
		exePath, err = os.Executable()
		if err != nil {
			return err
		}
	}
	cmd := exec.Command(exePath)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start app: %w", err)
	}
	_ = cmd.Process.Release()

	deadline := time.Now().Add(launchWait)
	for time.Now().Before(deadline) {
		if err := ipc.Ping(100 * time.Millisecond); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("APP_STARTING")
}

func forward(req ipc.Request) (ipc.Response, error) {
	return ipc.Call(2*time.Second, req)
}

// Manifest describes the native messaging host JSON.
type Manifest struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Path           string   `json:"path"`
	Type           string   `json:"type"`
	AllowedOrigins []string `json:"allowed_origins"`
}

// DefaultAllowedOrigins kept for callers that build manifests by hand.
var DefaultAllowedOrigins = AllowedOrigins()

func WriteManifest(dir, exePath string, origins []string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if origins == nil {
		origins = AllowedOrigins()
	}
	abs, err := filepath.Abs(exePath)
	if err != nil {
		abs = exePath
	}
	m := Manifest{
		Name:           HostName,
		Description:    "XDL native messaging host",
		Path:           abs,
		Type:           "stdio",
		AllowedOrigins: origins,
	}
	path := filepath.Join(dir, HostName+".json")
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// BrowserIntegration is the per-browser registration state shown in the UI.
type BrowserIntegration struct {
	Browser      string `json:"browser"`
	ManifestPath string `json:"manifestPath,omitempty"`
	Installed    bool   `json:"installed"`
}

// Status is the browser-integration snapshot exposed to the shell and popup.
type Status struct {
	HostExists     bool                 `json:"hostExists"`
	HostPath       string               `json:"hostPath,omitempty"`
	ManifestExists bool                 `json:"manifestExists"`
	ManifestPath   string               `json:"manifestPath,omitempty"`
	ExtensionID    string               `json:"extensionId"`
	Origins        []string             `json:"origins"`
	Browsers       []BrowserIntegration `json:"browsers"`
	Installed      bool                 `json:"installed"`
}

// QueryStatus inspects the host copy, manifest and HKCU registrations.
func QueryStatus(dir string) Status {
	st := Status{
		ManifestPath: filepath.Join(dir, HostName+".json"),
		ExtensionID:  DevExtensionID,
		Origins:      AllowedOrigins(),
	}
	if raw, err := os.ReadFile(st.ManifestPath); err == nil {
		st.ManifestExists = true
		var m Manifest
		if json.Unmarshal(raw, &m) == nil && m.Path != "" {
			st.HostPath = m.Path
			if _, err := os.Stat(m.Path); err == nil {
				st.HostExists = true
			}
		}
	}
	browsers := []struct{ name, key string }{
		{"Chrome", `Software\Google\Chrome\NativeMessagingHosts\` + HostName},
		{"Edge", `Software\Microsoft\Edge\NativeMessagingHosts\` + HostName},
		{"Chromium", `Software\Chromium\NativeMessagingHosts\` + HostName},
	}
	for _, b := range browsers {
		bi := BrowserIntegration{Browser: b.name}
		if p, err := registryManifest(b.key); err == nil && p != "" {
			bi.ManifestPath = p
			if _, err := os.Stat(p); err == nil {
				bi.Installed = true
			}
		}
		st.Browsers = append(st.Browsers, bi)
		if bi.Installed {
			st.Installed = true
		}
	}
	if !st.Installed && st.ManifestExists {
		// Manifest is present but the registry was not readable; treat the
		// presence of the executable copy as "needs repair" rather than
		// claiming a working integration.
		st.Installed = false
	}
	return st
}
