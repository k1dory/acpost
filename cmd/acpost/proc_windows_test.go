package main

import "os"

// askToStop ends a process: Windows has no request a process without a console hears.
func askToStop(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
		_ = p.Release()
	}
}

// interrupt is a Ctrl-C at the process: not to be sent here to one process alone.
func interrupt(p *os.Process) { _ = p.Kill() }

// stopsCleanly says whether a process asked to stop ends with its own exit code.
const stopsCleanly = false
