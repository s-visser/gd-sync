package ui

import (
	"os"

	"golang.org/x/sys/windows"
)

// enableColour turns on ANSI escape handling in the Windows console. Windows
// Terminal has it on already; the classic console needs asking.
func enableColour() bool {
	h := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if windows.GetConsoleMode(h, &mode) != nil {
		return false
	}
	return windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) == nil
}
