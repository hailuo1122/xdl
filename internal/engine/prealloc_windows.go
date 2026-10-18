//go:build windows

package engine

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// preallocatePlatform extends the file to size and forces NTFS to reserve
// clusters (FileAllocationInfo). When SeManageVolumePrivilege is available it
// also calls SetFileValidData so the FS does not have to zero the range first.
//
// Truncate alone only moves EOF; on NTFS writes into the hole still pay
// allocation cost under concurrent WriteAt. True allocation reduces that
// fragmentation and random-write penalty for multi-connection downloads.
func preallocatePlatform(f *os.File, size int64) error {
	if size <= 0 {
		return nil
	}
	if err := f.Truncate(size); err != nil {
		return err
	}
	h := windows.Handle(f.Fd())

	type fileAllocationInfo struct {
		AllocationSize int64
	}
	alloc := fileAllocationInfo{AllocationSize: size}
	_ = windows.SetFileInformationByHandle(
		h,
		windows.FileAllocationInfo,
		(*byte)(unsafe.Pointer(&alloc)),
		uint32(unsafe.Sizeof(alloc)),
	)

	// Best-effort: skip zero-fill. Fails without SeManageVolumePrivilege —
	// normal for unelevated users; allocation above still helps.
	if err := enablePrivilege("SeManageVolumePrivilege"); err == nil {
		_ = windows.SetFileValidData(h, size)
	}
	return nil
}

func enablePrivilege(name string) error {
	var token windows.Token
	err := windows.OpenProcessToken(
		windows.CurrentProcess(),
		windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY,
		&token,
	)
	if err != nil {
		return err
	}
	defer token.Close()

	var luid windows.LUID
	if err := windows.LookupPrivilegeValue(nil, windows.StringToUTF16Ptr(name), &luid); err != nil {
		return err
	}
	tp := windows.Tokenprivileges{
		PrivilegeCount: 1,
		Privileges: [1]windows.LUIDAndAttributes{
			{Luid: luid, Attributes: windows.SE_PRIVILEGE_ENABLED},
		},
	}
	return windows.AdjustTokenPrivileges(token, false, &tp, 0, nil, nil)
}
