package nativehost

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/xdl-app/xdl/internal/ipc"
)

func TestOriginsIncludeDevID(t *testing.T) {
	origins := AllowedOrigins()
	want := "chrome-extension://" + DevExtensionID + "/"
	found := false
	for _, o := range origins {
		if o == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("origins %v missing %s", origins, want)
	}
	if !originAllowed(want) {
		t.Fatal("allowed origin rejected")
	}
	if originAllowed("chrome-extension://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/") {
		t.Fatal("unknown origin accepted")
	}
	if !originAllowed("") {
		t.Fatal("manual invocation should be accepted")
	}
}

func TestWriteManifestAndQuery(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "host.exe")
	if err := os.WriteFile(exe, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifestPath, err := WriteManifest(dir, exe, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m.Name != HostName || m.Type != "stdio" || m.Path != exe {
		t.Fatalf("manifest = %+v", m)
	}
	for _, o := range m.AllowedOrigins {
		if o == "chrome-extension://"+DevExtensionID+"/" {
			return
		}
	}
	t.Fatalf("dev origin missing from %v", m.AllowedOrigins)
}

func TestShouldLaunch(t *testing.T) {
	cases := []struct {
		method string
		params string
		want   bool
	}{
		{ipc.MethodDownloadAdd, `{"url":"https://x"}`, true},
		{ipc.MethodPing, `{}`, false},
		{ipc.MethodPing, `{"launch":true}`, true},
		{ipc.MethodIntegrationRepair, ``, true},
		{ipc.MethodSettingsGet, `{}`, false},
	}
	for _, c := range cases {
		req := &ipc.Request{Method: c.method, Params: json.RawMessage(c.params)}
		if got := shouldLaunch(req); got != c.want {
			t.Errorf("shouldLaunch(%s, %s) = %v, want %v", c.method, c.params, got, c.want)
		}
	}
}
