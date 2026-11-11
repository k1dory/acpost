//go:build unix

package main

import (
	"os"
	"syscall"
)

// askToStop asks a process to end as a person would: SIGTERM.
func askToStop(pid int) { _ = syscall.Kill(pid, syscall.SIGTERM) }

// interrupt is a Ctrl-C at the process.
func interrupt(p *os.Process) { _ = p.Signal(syscall.SIGINT) }

// stopsCleanly says whether a process asked to stop ends with its own exit code (and writes what
// it has to say on the way).
const stopsCleanly = true
