//go:build windows && !uninstaller

package main

import _ "embed"

// payload.zip is a stub in the repo. scripts/pack-setup.ps1 replaces it
// with the real archive for the duration of the setup.exe build.
//
//go:embed payload.zip
var payloadZip []byte
