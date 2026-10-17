//go:build windows

package nativehost

import (
	"fmt"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

var browserKeys = []string{
	`Software\Google\Chrome\NativeMessagingHosts\` + HostName,
	`Software\Microsoft\Edge\NativeMessagingHosts\` + HostName,
	`Software\Chromium\NativeMessagingHosts\` + HostName,
}

// InstallRegistry writes HKCU NativeMessagingHosts entries pointing at manifestPath.
func InstallRegistry(manifestPath string) error {
	abs, err := filepath.Abs(manifestPath)
	if err != nil {
		return err
	}
	for _, keyPath := range browserKeys {
		key, _, err := registry.CreateKey(registry.CURRENT_USER, keyPath, registry.SET_VALUE)
		if err != nil {
			return fmt.Errorf("create %s: %w", keyPath, err)
		}
		err = key.SetStringValue("", abs)
		key.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// UninstallRegistry removes HKCU native host keys.
func UninstallRegistry() error {
	for _, keyPath := range browserKeys {
		_ = registry.DeleteKey(registry.CURRENT_USER, keyPath)
	}
	return nil
}

// registryManifest reads the default value of a NativeMessagingHosts key.
func registryManifest(keyPath string) (string, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, keyPath, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer key.Close()
	p, _, err := key.GetStringValue("")
	return p, err
}
