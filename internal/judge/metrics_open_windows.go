package judge

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func openMetricsResult(path string) (*os.File, error) {
	extended, err := windowsMetricsResultPath(path)
	if err != nil {
		return nil, err
	}
	name, err := syscall.UTF16PtrFromString(extended)
	if err != nil {
		return nil, err
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), extended), nil
}

func lstatMetricsResult(path string) (os.FileInfo, error) {
	// os.SameFile may reopen the path retained by Lstat to read the file ID.
	// Give that reopen the same extended path that opened the data handle.
	extended, err := windowsMetricsResultPath(path)
	if err != nil {
		return nil, err
	}
	return os.Lstat(extended)
}

func windowsMetricsResultPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return extendedMetricsPath(absolute), nil
}

// CreateFile needs the extended prefix for long absolute paths.
func extendedMetricsPath(path string) string {
	switch {
	case strings.HasPrefix(path, `\\?\`), strings.HasPrefix(path, `\\.\`):
		return path
	case strings.HasPrefix(path, `\\`):
		return `\\?\UNC\` + path[2:]
	default:
		return `\\?\` + path
	}
}
