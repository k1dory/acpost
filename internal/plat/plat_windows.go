package plat

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Lock takes the exclusive lock on f, held until Unlock or f's close. wait false: ErrLocked when
// another process holds it. The lock is on the file's first byte: these are lock files, read by
// no one.
func Lock(f *os.File, wait bool) error {
	how := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK)
	if !wait {
		how |= windows.LOCKFILE_FAIL_IMMEDIATELY
	}
	err := windows.LockFileEx(windows.Handle(f.Fd()), how, 0, 1, 0, new(windows.Overlapped))
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
		return ErrLocked
	}
	return err
}

// Unlock gives the lock back.
func Unlock(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, new(windows.Overlapped))
}

// Group is a started command and whatever it started: a job object.
type Group struct {
	mu     sync.Mutex
	job    windows.Handle // 0 once closed
	killed bool
}

// start starts cmd as the leader of a group of its own, and tells publish of the group before
// anything of it runs. (Go's exec cannot make a process in a job from its first moment; between
// its making and its place in the job, a few system calls, the starter's own death leaves it
// suspended: it never ran, and is not ended with the job.) The process is made suspended, put in its job, and only then let run:
// nothing it starts is outside the job, and a kill that comes meanwhile is not missed. lasting: the
// job ends with its last handle, this process's: a node that is killed leaves no agent behind.
func start(cmd *exec.Cmd, lasting bool, publish func(*Group)) (*Group, error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// No window of its own: a node with no console (one started in the background) would otherwise
	// have the system open one for each agent and each git, there to be closed by a hand.
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW
	// A program installed by npm is a .cmd file: run by the command interpreter, with a command
	// line that keeps its arguments whole.
	if file := batchFile(cmd); file != "" && cmd.SysProcAttr.CmdLine == "" && cmd.Err == nil {
		for i, a := range cmd.Args[1:] {
			if strings.ContainsAny(a, "\"\r\n") {
				// Named by its place, not shown: an argument may be a secret, and this error is told on.
				return nil, fmt.Errorf("start %s: its argument %d has a quote or a line end, and does not reach a .cmd file whole: name the program it runs instead (node and its script)", file, i+1)
			}
		}
		comspec := os.Getenv("ComSpec")
		if comspec == "" {
			comspec = filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
		}
		cmd.SysProcAttr.CmdLine = batchLine(comspec, file, cmd.Args[1:])
		cmd.Path = comspec
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("job object: %w", err)
	}
	if lasting {
		var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
			windows.CloseHandle(job)
			return nil, fmt.Errorf("job object: %w", err)
		}
	}
	g := &Group{job: job}
	if publish != nil {
		publish(g)
	}
	if err := cmd.Start(); err != nil {
		g.Close()
		return nil, err
	}
	if err := g.adopt(uint32(cmd.Process.Pid)); err != nil {
		// Still suspended, or killed with its job: it has started nothing.
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		g.Close()
		return nil, fmt.Errorf("start %s: %w", cmd.Path, err)
	}
	return g, nil
}

// adopt puts the suspended process pid in the job and lets it run, unless the group was killed
// meanwhile: then the process, now of the job, is killed with it and never runs.
func (g *Group) adopt(pid uint32) error {
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return fmt.Errorf("open the process: %w", err)
	}
	defer windows.CloseHandle(h)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.job == 0 {
		return errors.New("the group was closed while it started")
	}
	if err := windows.AssignProcessToJobObject(g.job, h); err != nil {
		return fmt.Errorf("job object: %w", err)
	}
	if g.killed {
		_ = windows.TerminateJobObject(g.job, 1)
		return errors.New("killed while it started")
	}
	return resume(pid)
}

// resume lets a process made suspended run: its threads (one, the main) are resumed.
func resume(pid uint32) error {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return fmt.Errorf("threads: %w", err)
	}
	defer windows.CloseHandle(snap)
	var te windows.ThreadEntry32
	te.Size = uint32(unsafe.Sizeof(te))
	resumed := 0
	for err = windows.Thread32First(snap, &te); err == nil; err = windows.Thread32Next(snap, &te) {
		if te.OwnerProcessID != pid {
			continue
		}
		th, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, te.ThreadID)
		if err != nil {
			return fmt.Errorf("thread: %w", err)
		}
		_, err = windows.ResumeThread(th)
		windows.CloseHandle(th)
		if err != nil {
			return fmt.Errorf("resume: %w", err)
		}
		resumed++
	}
	if resumed == 0 {
		return errors.New("resume: the process has no thread")
	}
	return nil
}

// killStarting kills a command whose group is not known yet. Not reached here: the group is told
// before the command starts.
func killStarting(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

// jobAccounting is JOBOBJECT_BASIC_ACCOUNTING_INFORMATION.
type jobAccounting struct {
	TotalUserTime, TotalKernelTime, ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime int64
	TotalPageFaultCount, TotalProcesses, ActiveProcesses, TotalTerminatedProcesses     uint32
}

// active is how many processes the job has (none once it is closed). Under g.mu.
func (g *Group) active() (uint32, error) {
	if g.job == 0 {
		return 0, nil
	}
	var a jobAccounting
	err := windows.QueryInformationJobObject(g.job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&a)), uint32(unsafe.Sizeof(a)), nil)
	return a.ActiveProcesses, err
}

// Stop asks the group to end; ErrGone when nothing of it is left. Windows has no request a
// process without a console hears: Stop only says whether there is anything to wait for, and the
// Kill that follows ends it.
func (g *Group) Stop() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if n, err := g.active(); err == nil && n == 0 {
		return ErrGone
	}
	return nil
}

// Kill ends the group at once.
func (g *Group) Kill() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.killed = true // a process on its way into the job does not run
	if g.job == 0 {
		return nil
	}
	return windows.TerminateJobObject(g.job, 1)
}

// Gone says whether nothing of the group is left.
func (g *Group) Gone() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	n, err := g.active()
	return err == nil && n == 0
}

// Close lets go of the job's handle, and the group is from here taken as gone. One started to
// last (Start) ends with it; one of a cancelable command goes on, as what a command left running.
func (g *Group) Close() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.job != 0 {
		windows.CloseHandle(g.job)
		g.job = 0
	}
}

// Detach makes cmd, once started, a process with no console of this one's: it outlives this one
// and its terminal.
func Detach(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP
	cmd.SysProcAttr.HideWindow = true
}

// Alive says whether a process with this id runs.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED) // there, and not ours to open
	}
	defer windows.CloseHandle(h)
	ev, err := windows.WaitForSingleObject(h, 0)
	return err == nil && ev == uint32(windows.WAIT_TIMEOUT)
}

// OpenRead opens a file for reading; the caller checks what it got.
func OpenRead(path string) (*os.File, error) { return os.Open(path) }

// PrivateDir says whether dir is closed to other users: its access list lets in only this user,
// the system and the administrators (who reach everything anyway). The error says how to close it.
func PrivateDir(dir string) error {
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return &os.PathError{Op: "access list", Path: dir, Err: err}
	}
	me, err := currentUser()
	if err != nil {
		return err
	}
	// Its owner changes its access list at will: another user's is not this user's alone,
	// whatever its list says now.
	if owner, _, err := sd.Owner(); err != nil || !trustedOwner(owner, me) {
		whose := "an owner not read"
		if err == nil && owner != nil {
			whose = "owned by " + owner.String()
		}
		return &OpenDirError{Dir: dir, To: whose, Fix: `icacls "` + dir + `" /setowner "*` + me.String() + `"`}
	}
	// The repair: an access list of its own for this user (the system and the administrators
	// keep theirs or reach it anyway), and each other grant named and removed.
	fix := `icacls "` + dir + `" /inheritance:r /grant:r "*` + me.String() + `:(OI)(CI)F"`
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return &OpenDirError{Dir: dir, To: "no access list: everyone", Fix: fix}
	}
	var others []string
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return &os.PathError{Op: "access list", Path: dir, Err: err}
		}
		switch ace.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE:
			continue
		case windows.ACCESS_ALLOWED_ACE_TYPE:
		default:
			return &OpenDirError{Dir: dir, To: fmt.Sprintf("an access entry of a kind not known here, %d", ace.Header.AceType),
				Fix: "give it an access list for this user alone"}
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if ace.Mask == 0 || sid.Equals(me) || sid.IsWellKnown(windows.WinLocalSystemSid) ||
			sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) || sid.IsWellKnown(windows.WinCreatorOwnerSid) {
			continue
		}
		if name := sid.String(); !slices.Contains(others, name) {
			others = append(others, name)
		}
	}
	if len(others) == 0 {
		return nil
	}
	for _, sid := range others {
		fix += " /remove:g *" + sid
	}
	return &OpenDirError{Dir: dir, To: strings.Join(others, ", "), Fix: fix}
}

// trustedOwner says whether owner is this user, the system or the administrators.
func trustedOwner(owner, me *windows.SID) bool {
	return owner != nil && (owner.Equals(me) || owner.IsWellKnown(windows.WinLocalSystemSid) || owner.IsWellKnown(windows.WinBuiltinAdministratorsSid))
}

// privateSD is the security of what is this user's alone: an access list of its own, inherited
// from no parent, for this user, the system and the administrators; for a directory, handed on
// to what is made in it.
func privateSD(dir bool) (*windows.SECURITY_DESCRIPTOR, error) {
	me, err := currentUser()
	if err != nil {
		return nil, err
	}
	on := ""
	if dir {
		on = "OICI"
	}
	return windows.SecurityDescriptorFromString("D:P(A;" + on + ";FA;;;" + me.String() + ")(A;" + on + ";FA;;;SY)(A;" + on + ";FA;;;BA)")
}

// MkdirPrivate makes dir (and its parents) when it is not there, closed to other users from the
// moment it is there: each directory is made with its access list, not given one after. A
// directory already there is left as it is.
func MkdirPrivate(dir string) error {
	dir = filepath.Clean(dir)
	if st, err := os.Stat(dir); err == nil {
		if !st.IsDir() {
			return &os.PathError{Op: "mkdir", Path: dir, Err: syscall.ENOTDIR}
		}
		return nil
	}
	if parent := filepath.Dir(dir); parent != dir {
		if err := MkdirPrivate(parent); err != nil {
			return err
		}
	}
	sd, err := privateSD(true)
	if err != nil {
		return err
	}
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return &os.PathError{Op: "mkdir", Path: dir, Err: err}
	}
	sa := windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(sa))
	err = windows.CreateDirectory(p, &sa)
	if err == nil || errors.Is(err, windows.ERROR_ALREADY_EXISTS) { // made meanwhile by another: as it is
		return nil
	}
	return &os.PathError{Op: "mkdir", Path: dir, Err: err}
}

// PrivateFile closes a file to other users: an access list of its own, whatever it had. (A file
// made in a private directory has its list from there; one that was there before, or was moved
// in, keeps its own, and a path that is known is opened past a closed directory.)
func PrivateFile(path string) error {
	had, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return &os.PathError{Op: "owner", Path: path, Err: err}
	}
	me, err := currentUser()
	if err != nil {
		return err
	}
	// Another user's file stays theirs to open again, whatever list it is given: refused.
	if owner, _, err := had.Owner(); err != nil || !trustedOwner(owner, me) {
		return &OpenDirError{Dir: path, To: "owned by another user", Fix: `icacls "` + path + `" /setowner "*` + me.String() + `"`}
	}
	sd, err := privateSD(false)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
	if err != nil {
		return &os.PathError{Op: "access list", Path: path, Err: err}
	}
	return nil
}

func currentUser() (*windows.SID, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("this user: %w", err)
	}
	return u.User.Sid, nil
}

// Denied says whether err is the system refusing this process a file or a socket.
func Denied(err error) bool {
	return errors.Is(err, fs.ErrPermission) || errors.Is(err, windows.WSAEACCES)
}

// createPrivate makes a new file for this user alone: made with its access list.
func createPrivate(path string) (*os.File, error) {
	sd, err := privateSD(false)
	if err != nil {
		return nil, err
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "create", Path: path, Err: err}
	}
	sa := windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(sa))
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_DELETE,
		&sa, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "create", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}

// ReadFile reads a file that another process may be replacing by a rename meanwhile: opened so
// that it may be (Go's own open keeps a file from being renamed over while it is read).
func ReadFile(path string) ([]byte, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(h), path)
	defer f.Close()
	return io.ReadAll(f)
}

// Rename puts a file written whole in place of another, also while that one is being read: the
// rename is asked for as Unix does it (the old file goes on for those who have it open), which
// NTFS gives from Windows 10 1709. Where it is not given, the plain rename; and whoever keeps
// the old file from being replaced for a moment (a scanner) is waited for, two seconds at most.
func Rename(from, to string) error {
	var err error
	for end := time.Now().Add(2 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		err = renameOver(from, to)
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) || errors.Is(err, windows.ERROR_NOT_SUPPORTED) || errors.Is(err, windows.ERROR_INVALID_FUNCTION) {
			err = os.Rename(from, to)
		}
		if err == nil || !(errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_ACCESS_DENIED)) || time.Now().After(end) {
			return err
		}
	}
}

// fileRenameInfo is FILE_RENAME_INFO, before its name.
type fileRenameInfo struct {
	Flags          uint32
	RootDirectory  windows.Handle
	FileNameLength uint32
	FileName       [1]uint16
}

// renameOver renames from to to, replacing it, with POSIX semantics.
func renameOver(from, to string) error {
	fail := func(err error) error { return &os.LinkError{Op: "rename", Old: from, New: to, Err: err} }
	to, err := filepath.Abs(to)
	if err != nil {
		return fail(err)
	}
	name, err := windows.UTF16FromString(to)
	if err != nil {
		return fail(err)
	}
	p, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return fail(err)
	}
	h, err := windows.CreateFile(p, windows.DELETE|windows.SYNCHRONIZE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return fail(err)
	}
	defer windows.CloseHandle(h)
	chars := len(name) - 1 // without the ending zero
	var head fileRenameInfo
	buf := make([]byte, int(unsafe.Offsetof(head.FileName))+2*len(name))
	info := (*fileRenameInfo)(unsafe.Pointer(&buf[0]))
	info.Flags = windows.FILE_RENAME_REPLACE_IF_EXISTS | windows.FILE_RENAME_POSIX_SEMANTICS
	info.FileNameLength = uint32(2 * chars)
	copy(unsafe.Slice(&info.FileName[0], len(name)), name)
	if err := windows.SetFileInformationByHandle(h, windows.FileRenameInfoEx, &buf[0], uint32(len(buf))); err != nil {
		return fail(err)
	}
	return nil
}

// batchLine is the command line that runs a .cmd or .bat file with its arguments as they are:
// the command interpreter named, with /s so that the outer quotes alone are taken off and each
// argument's own quoting stays. Every argument is quoted: & | < > ^ and spaces are then its
// text. An argument with a quote of its own is not taken (start refuses it): the interpreter
// knows no escaped quote, and what follows one would be its to read. %NAME% it still replaces,
// as in any batch call.
func batchLine(comspec, file string, args []string) string {
	line := quoteArg(file)
	for _, a := range args {
		line += " " + quoteArg(a)
	}
	return quoteArg(comspec) + ` /d /s /c "` + line + `"`
}

// quoteArg is s in double quotes, as programs read their command line: a quote in it escaped,
// and the backslashes before a quote (or before the closing one) doubled.
func quoteArg(s string) string {
	b := []byte{'"'}
	slashes := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			slashes++
		case '"':
			for ; slashes > 0; slashes-- {
				b = append(b, '\\')
			}
			b = append(b, '\\')
		default:
			slashes = 0
		}
		b = append(b, s[i])
	}
	for ; slashes > 0; slashes-- {
		b = append(b, '\\')
	}
	return string(append(b, '"'))
}

// batchFile is the .cmd or .bat file cmd would run ("" when it runs a program): its path as
// given, or with the extension the system would add (C:\tools\agent for agent.cmd; a relative
// one is of cmd.Dir).
func batchFile(cmd *exec.Cmd) string {
	p := localPath(cmd.Path, cmd.Dir)
	if lp, err := exec.LookPath(p); err == nil {
		p = lp
	}
	if !isBatch(p) {
		return ""
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	return p
}

// localPath is where a command's path leads for a process in dir: as it is when it is whole
// (or a bare name, looked for on PATH), on dir's drive when it starts at a root (\tools\agent),
// under dir when it is relative (.\agent, bin\agent).
func localPath(p, dir string) string {
	switch {
	case filepath.IsAbs(p) || dir == "" || !strings.ContainsAny(p, `\/`):
		return p
	case filepath.VolumeName(p) != "": // C:agent: of that drive's own directory, not dir's
		return p
	case p[0] == '\\' || p[0] == '/':
		return filepath.VolumeName(dir) + p
	}
	return filepath.Join(dir, p)
}

// isBatch says whether path is a file the command interpreter runs.
func isBatch(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".cmd" || ext == ".bat"
}
