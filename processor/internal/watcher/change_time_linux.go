package watcher

import (
	"os"
	"syscall"
)

func fileChangeTime(info os.FileInfo) int64 {
	s := info.Sys().(*syscall.Stat_t)
	return s.Ctim.Sec*1e9 + s.Ctim.Nsec
}
