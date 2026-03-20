//go:build !windows

package tui

import (
	"os"
	"syscall"
)

func setNonBlock(f *os.File) error {
	return syscall.SetNonblock(int(f.Fd()), true)
}

func setBlock(f *os.File) error {
	return syscall.SetNonblock(int(f.Fd()), false)
}
