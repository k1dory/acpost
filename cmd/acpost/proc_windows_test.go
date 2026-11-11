package main

import (
	"os"
	"os/exec"
	"strconv"
)

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

// canInterrupt says whether a Ctrl-C can be sent to one process: on Windows it goes to a whole
// console, the test's own too.
const canInterrupt = false

// shortTmp is where sockets are made: the system's temporary directory.
const shortTmp = ""

// commandLine is the command line of a running process ("" when it is not seen).
func commandLine(pid int) string {
	out, _ := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		`(Get-CimInstance Win32_Process -Filter "ProcessId=`+strconv.Itoa(pid)+`").CommandLine`).Output()
	return string(out)
}

// noSuchFile is how the system says a file (or the directory it would be in) is not there.
const noSuchFile = "cannot find the"

// termLine is typed into a terminal's shell (PowerShell): it prints via-http (joined by the
// shell: not seen in what is typed) and ends.
const termLine = "pwd; echo ('via-' + 'http'); exit\r"
