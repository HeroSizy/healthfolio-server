//go:build !darwin && !linux && !freebsd && !openbsd && !netbsd && !windows

package server

func diskSpace(string) (free, size int64) { return 0, 0 }
