//go:build windows

package tui

import "os"

func setNonBlock(_ *os.File) error { return nil }
func setBlock(_ *os.File) error   { return nil }
