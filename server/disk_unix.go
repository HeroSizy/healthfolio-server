//go:build darwin || linux || freebsd || openbsd || netbsd

package server

import "golang.org/x/sys/unix"

// diskSpace is the free (for this user) and total bytes of the disk holding path.
func diskSpace(path string) (free, size int64) {
	var st unix.Statfs_t
	if unix.Statfs(path, &st) != nil {
		return 0, 0
	}
	return int64(st.Bavail) * int64(st.Bsize), int64(st.Blocks) * int64(st.Bsize)
}
