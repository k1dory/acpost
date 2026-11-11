package node

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// pseudoConsole is a shell on a Windows pseudo-console (ConPTY): what is typed goes down one
// pipe, what the console shows comes up another, as a terminal's bytes.
type pseudoConsole struct {
	in   *os.File // typed: to the console
	out  *os.File // shown: from the console
	proc windows.Handle

	mu     sync.Mutex
	pc     windows.Handle // 0 once closed
	closed bool
}

// consoleShell is the shell a terminal runs: PowerShell (the newer one when it is installed),
// or what ComSpec names.
func consoleShell() string {
	for _, name := range []string{"pwsh.exe", "powershell.exe"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	if p := os.Getenv("ComSpec"); p != "" {
		return p
	}
	return "cmd.exe"
}

// hasConsoles says whether this Windows has pseudo-consoles (from Windows 10 1809).
var hasConsoles = sync.OnceValue(func() bool {
	return windows.NewLazySystemDLL("kernel32.dll").NewProc("CreatePseudoConsole").Find() == nil
})

func startConsole(dir string, env []string, cols, rows int) (console, error) {
	if !hasConsoles() {
		return nil, errNoConsole
	}
	shell := consoleShell()
	fail := func(what string, err error) (console, error) {
		return nil, fmt.Errorf("start %s: %s: %v", shell, what, err)
	}
	var inR, inW, outR, outW windows.Handle
	if err := windows.CreatePipe(&inR, &inW, nil, 0); err != nil {
		return fail("pipe", err)
	}
	if err := windows.CreatePipe(&outR, &outW, nil, 0); err != nil {
		windows.CloseHandle(inR)
		windows.CloseHandle(inW)
		return fail("pipe", err)
	}
	var pc windows.Handle
	err := windows.CreatePseudoConsole(windows.Coord{X: int16(cols), Y: int16(rows)}, inR, outW, 0, &pc)
	// The console has its own copies of its ends of the pipes.
	windows.CloseHandle(inR)
	windows.CloseHandle(outW)
	if err != nil {
		windows.CloseHandle(inW)
		windows.CloseHandle(outR)
		return fail("pseudo-console", err)
	}
	// What the console shows has no reader yet: its pipe is closed first, or the console's own
	// close may wait for a reader of what it has to say (before Windows 11 24H2 it does).
	undo := func() {
		windows.CloseHandle(outR)
		windows.CloseHandle(inW)
		windows.ClosePseudoConsole(pc)
	}
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		undo()
		return fail("attributes", err)
	}
	defer attrs.Delete()
	// The attribute's value is the console's handle itself, passed where a pointer goes.
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, *(*unsafe.Pointer)(unsafe.Pointer(&pc)), unsafe.Sizeof(pc)); err != nil {
		undo()
		return fail("attributes", err)
	}
	si := windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	si.Cb = uint32(unsafe.Sizeof(si))
	// Standard handles named, and none given: the shell takes the console's. Without this a node
	// whose own output is a file or a pipe (one started in the background) hands the shell those,
	// and the shell writes past its terminal.
	si.Flags |= windows.STARTF_USESTDHANDLES
	line, err := windows.UTF16PtrFromString(windows.EscapeArg(shell))
	if err != nil {
		undo()
		return fail("command line", err)
	}
	cwd, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		undo()
		return fail("directory", err)
	}
	var pi windows.ProcessInformation
	err = windows.CreateProcess(nil, line, nil, nil, false,
		windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_UNICODE_ENVIRONMENT, envBlock(env), cwd, &si.StartupInfo, &pi)
	if err != nil {
		undo()
		return nil, fmt.Errorf("start %s: %v", shell, err)
	}
	windows.CloseHandle(pi.Thread)
	return &pseudoConsole{in: os.NewFile(uintptr(inW), "console-in"), out: os.NewFile(uintptr(outR), "console-out"), proc: pi.Process, pc: pc}, nil
}

// envBlock is an environment as CreateProcess takes it: NAME=value strings, sorted without
// regard to case, each ended by a zero, the block by one more.
func envBlock(env []string) *uint16 {
	env = append([]string(nil), env...)
	sort.SliceStable(env, func(i, j int) bool {
		a, _, _ := strings.Cut(env[i], "=")
		b, _, _ := strings.Cut(env[j], "=")
		return strings.ToUpper(a) < strings.ToUpper(b)
	})
	var b []uint16
	for _, kv := range env {
		if strings.IndexByte(kv, 0) >= 0 {
			continue
		}
		b = append(b, utf16.Encode([]rune(kv))...)
		b = append(b, 0)
	}
	b = append(b, 0, 0)
	return &b[0]
}

func (c *pseudoConsole) Read(b []byte) (int, error)  { return c.out.Read(b) }
func (c *pseudoConsole) Write(b []byte) (int, error) { return c.in.Write(b) }

func (c *pseudoConsole) Resize(cols, rows int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pc == 0 {
		return nil // ended: nothing to size
	}
	return windows.ResizePseudoConsole(c.pc, windows.Coord{X: int16(cols), Y: int16(rows)})
}

// Wait is the shell's end. The console is closed with it: whatever the shell left attached goes
// (as a terminal's hangup), and the reader, once it has what was shown, sees the end.
func (c *pseudoConsole) Wait() int {
	_, _ = windows.WaitForSingleObject(c.proc, windows.INFINITE)
	var code uint32
	_ = windows.GetExitCodeProcess(c.proc, &code)
	c.hangup()
	return int(code)
}

func (c *pseudoConsole) hangup() {
	c.mu.Lock()
	pc := c.pc
	c.pc = 0
	c.mu.Unlock()
	if pc != 0 {
		windows.ClosePseudoConsole(pc)
	}
}

// Kill ends the shell; Wait, seeing that, closes the console under the rest.
func (c *pseudoConsole) Kill() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed { // its handle is still this shell's
		_ = windows.TerminateProcess(c.proc, 1)
	}
}

func (c *pseudoConsole) Close() error {
	c.hangup()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	_ = c.in.Close()
	err := c.out.Close()
	windows.CloseHandle(c.proc)
	return err
}
