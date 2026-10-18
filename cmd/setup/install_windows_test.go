//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeRel(t *testing.T) {
	ok := []string{"xdl.exe", "extension/manifest.json", `extension\chunks\app.js`}
	for _, name := range ok {
		got, err := safeRel(name)
		if err != nil {
			t.Fatalf("safeRel(%q): %v", name, err)
		}
		if strings.Contains(got, "..") {
			t.Fatalf("safeRel(%q) = %q", name, got)
		}
	}
	bad := []string{"", "../xdl.exe", "/windows/x", `..\xdl.exe`, "C:/x.exe", "extension/../../outside"}
	for _, name := range bad {
		if _, err := safeRel(name); err == nil {
			t.Fatalf("safeRel(%q) should reject", name)
		}
	}
}

func TestCleanInstallDir(t *testing.T) {
	if _, err := cleanInstallDir(`C:\`); err == nil {
		t.Fatal("root accepted")
	}
	if _, err := cleanInstallDir(""); err == nil {
		t.Fatal("empty accepted")
	}
	windir := os.Getenv("WINDIR")
	if windir != "" {
		if _, err := cleanInstallDir(filepath.Join(windir, "System32")); err == nil {
			t.Fatal("system directory accepted")
		}
	}
	dir := filepath.Join(os.TempDir(), "xdl-setup-test")
	got, err := cleanInstallDir("  " + dir + "  ")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(got, dir) {
		t.Fatalf("got %s", got)
	}
}

func TestFormatBytes(t *testing.T) {
	if formatBytes(1536) != "1.5 KB" {
		t.Fatal(formatBytes(1536))
	}
}
