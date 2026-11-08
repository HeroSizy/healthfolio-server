//go:build windows

package server

import "golang.org/x/sys/windows"

// diskSpace is the free (for this user) and total bytes of the disk holding path.
func diskSpace(path string) (free, size int64) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0
	}
	var avail, total, totalFree uint64
	if windows.GetDiskFreeSpaceEx(p, &avail, &total, &totalFree) != nil {
		return 0, 0
	}
	return int64(avail), int64(total)
}
