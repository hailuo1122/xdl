//go:build windows

package app

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	winmm      = windows.NewLazySystemDLL("winmm.dll")
	playSoundW = winmm.NewProc("PlaySoundW")
)

const (
	sndAsync     = 0x0001
	sndNoDefault = 0x0002
	sndAlias     = 0x00010000
)

func playAlert(alias string) {
	p, err := windows.UTF16PtrFromString(alias)
	if err != nil {
		return
	}
	_, _, _ = playSoundW.Call(uintptr(unsafe.Pointer(p)), 0, sndAsync|sndAlias|sndNoDefault)
}
