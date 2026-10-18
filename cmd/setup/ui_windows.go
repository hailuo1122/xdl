//go:build windows

package main

import _ "embed"

// Built by cmd/setup/ui (React + Motion). Run: pnpm --dir cmd/setup/ui build
//
//go:embed ui/dist/index.html
var uiHTML string
