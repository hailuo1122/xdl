//go:build windows && uninstaller

package main

// The uninstaller binary does not carry the program archive.
var payloadZip []byte
