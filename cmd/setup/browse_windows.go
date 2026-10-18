//go:build windows

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	shell32                  = windows.NewLazySystemDLL("shell32.dll")
	procSHBrowseForFolderW   = shell32.NewProc("SHBrowseForFolderW")
	procSHGetPathFromIDListW = shell32.NewProc("SHGetPathFromIDListW")
	ole32                    = windows.NewLazySystemDLL("ole32.dll")
	procCoTaskMemFree        = ole32.NewProc("CoTaskMemFree")
	user32                   = windows.NewLazySystemDLL("user32.dll")
	procSendMessageW         = user32.NewProc("SendMessageW")
	browseCB                 = syscall.NewCallback(browseCallback)
)

const (
	bifReturnOnlyFSDirs = 0x00000001
	bifNewDialogStyle   = 0x00000040
	bifEditBox          = 0x00000010
	bffmInitialized     = 1
	bffmSetSelectionW   = 0x467
)

type browseInfo struct {
	hwndOwner      uintptr
	pidlRoot       uintptr
	pszDisplayName *uint16
	lpszTitle      *uint16
	ulFlags        uint32
	lpfn           uintptr
	lParam         uintptr
	iImage         int32
}

func browseFolder(owner windows.HWND, initial string) (string, error) {
	err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED)
	if err == nil {
		defer windows.CoUninitialize()
	}

	title, terr := windows.UTF16PtrFromString("选择 XDL 的安装位置")
	if terr != nil {
		return "", terr
	}
	display := make([]uint16, windows.MAX_PATH)
	initial = existingAncestor(initial)
	initialPtr, _ := windows.UTF16PtrFromString(initial)

	bi := browseInfo{
		hwndOwner:      uintptr(owner),
		pszDisplayName: &display[0],
		lpszTitle:      title,
		ulFlags:        bifReturnOnlyFSDirs | bifNewDialogStyle | bifEditBox,
		lpfn:           browseCB,
		lParam:         uintptr(unsafe.Pointer(initialPtr)),
	}
	pidl, _, _ := procSHBrowseForFolderW.Call(uintptr(unsafe.Pointer(&bi)))
	runtime.KeepAlive(initialPtr)
	if pidl == 0 {
		return "", nil
	}
	defer procCoTaskMemFree.Call(pidl)

	buf := make([]uint16, windows.MAX_PATH)
	ok, _, _ := procSHGetPathFromIDListW.Call(pidl, uintptr(unsafe.Pointer(&buf[0])))
	if ok == 0 {
		return "", nil
	}
	return windows.UTF16ToString(buf), nil
}

func browseCallback(hwnd, msg, lParam, data uintptr) uintptr {
	if msg == bffmInitialized && data != 0 {
		_, _, _ = procSendMessageW.Call(hwnd, bffmSetSelectionW, 1, data)
	}
	return 0
}

func existingAncestor(dir string) string {
	dir = filepath.Clean(dir)
	for dir != "" && dir != "." {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			return dir
		}
		next := filepath.Dir(dir)
		if next == dir {
			break
		}
		dir = next
	}
	return ""
}
