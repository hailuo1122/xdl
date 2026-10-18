//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
)

func createShortcut(linkPath, target, workDir, description string) error {
	if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
		return fmt.Errorf("无法创建开始菜单文件夹：%s", err.Error())
	}
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err == nil {
		defer ole.CoUninitialize()
	}
	unk, err := oleutil.CreateObject("WScript.Shell")
	if err != nil {
		return fmt.Errorf("无法创建快捷方式：%s", err.Error())
	}
	defer unk.Release()
	wsh, err := unk.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return fmt.Errorf("无法创建快捷方式：%s", err.Error())
	}
	defer wsh.Release()
	scRaw, err := oleutil.CallMethod(wsh, "CreateShortcut", linkPath)
	if err != nil {
		return fmt.Errorf("无法创建快捷方式：%s", err.Error())
	}
	sc := scRaw.ToIDispatch()
	defer sc.Release()
	if _, err = oleutil.PutProperty(sc, "TargetPath", target); err != nil {
		return fmt.Errorf("无法创建快捷方式：%s", err.Error())
	}
	if _, err = oleutil.PutProperty(sc, "WorkingDirectory", workDir); err != nil {
		return fmt.Errorf("无法创建快捷方式：%s", err.Error())
	}
	if _, err = oleutil.PutProperty(sc, "Description", description); err != nil {
		return fmt.Errorf("无法创建快捷方式：%s", err.Error())
	}
	if _, err = oleutil.CallMethod(sc, "Save"); err != nil {
		return fmt.Errorf("无法创建快捷方式：%s", err.Error())
	}
	return nil
}
