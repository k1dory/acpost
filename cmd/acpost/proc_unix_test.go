//go:build unix

package main

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// askToStop asks a process to end as a person would: SIGTERM.
func askToStop(pid int) { _ = syscall.Kill(pid, syscall.SIGTERM) }

// interrupt is a Ctrl-C at the process.
func interrupt(p *os.Process) { _ = p.Signal(syscall.SIGINT) }

// stopsCleanly says whether a process asked to stop ends with its own exit code (and writes what
// it has to say on the way).
const stopsCleanly = true

// canInterrupt says whether a Ctrl-C can be sent to one process.
const canInterrupt = true

// shortTmp is where sockets are made: a Unix socket's path is short, and macOS's own temporary
// directory is not.
const shortTmp = "/tmp"

// commandLine is the command line of a running process ("" when it is not seen).
func commandLine(pid int) string {
	out, _ := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	return string(out)
}

// noSuchFile is how the system says a file is not there.
const noSuchFile = "no such file"

// termLine is typed into a terminal's shell: it prints via-http (joined by the shell: not seen in
// what is typed) and ends.
const termLine = "pwd; echo via-\"\"http; exit\n"
