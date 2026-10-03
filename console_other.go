//go:build !windows

package main

import "fmt"

func enableConsole() {}

func clearConsole() {
	fmt.Print("\x1b[2J\x1b[H\x1b[3J")
}
