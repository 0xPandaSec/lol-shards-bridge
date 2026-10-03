//go:build windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

// enableConsole stellt die Windows-Konsole auf UTF-8 um und aktiviert
// ANSI-Escape-Sequenzen (für das Leeren des Bildschirms).
func enableConsole() {
	k32 := syscall.NewLazyDLL("kernel32.dll")
	p := k32.NewProc("SetConsoleOutputCP")
	if p.Find() == nil {
		p.Call(uintptr(65001))
	}
	const stdOutput = ^uintptr(11) + 1 // STD_OUTPUT_HANDLE (-11)
	h, _, _ := k32.NewProc("GetStdHandle").Call(stdOutput)
	if h == ^uintptr(0) {
		return
	}
	var mode uint32
	pr, _, _ := k32.NewProc("GetConsoleMode").Call(h, uintptr(unsafe.Pointer(&mode)))
	if pr == 0 {
		return
	}
	const enableVt = 0x0004
	k32.NewProc("SetConsoleMode").Call(h, uintptr(mode|enableVt))
}

// clearConsole leert Bildschirm und Scrollback.
func clearConsole() {
	fmt.Print("\x1b[2J\x1b[H\x1b[3J")
}
