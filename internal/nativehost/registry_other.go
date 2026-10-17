//go:build !windows

package nativehost

func InstallRegistry(manifestPath string) error { return nil }
func UninstallRegistry() error                  { return nil }
func registryManifest(string) (string, error)   { return "", nil }
