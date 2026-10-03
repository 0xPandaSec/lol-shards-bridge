//go:build windows

package main

import "syscall"

// enableUTF8Console stellt die Windows-Konsole auf UTF-8 um, damit
// Unicode-Blockzeichen („█ ═ ╔ ╚“) korrekt gerendert werden.
func enableUTF8Console() {
	p := syscall.NewLazyDLL("kernel32.dll").NewProc("SetConsoleOutputCP")
	if err := p.Find(); err == nil {
		p.Call(uintptr(65001))
	}
}
