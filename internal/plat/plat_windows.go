package plat

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
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
	mu  sync.Mutex
	job windows.Handle // 0 once closed
}

// Start starts cmd as the leader of a group of its own. The process is made suspended, put in its
// job, and only then let run: nothing it starts is outside the job.
func Start(cmd *exec.Cmd) (*Group, error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("job object: %w", err)
	}
	if err := cmd.Start(); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	if err := adopt(job, uint32(cmd.Process.Pid)); err != nil {
		// Still suspended (or already killed by its context): it has started nothing.
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		windows.CloseHandle(job)
		return nil, fmt.Errorf("start %s: %w", cmd.Path, err)
	}
	return &Group{job: job}, nil
}

// adopt puts the suspended process pid in job and lets it run.
func adopt(job windows.Handle, pid uint32) error {
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return fmt.Errorf("open the process: %w", err)
	}
	defer windows.CloseHandle(h)
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		return fmt.Errorf("job object: %w", err)
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

// killStarting kills a command whose Start has not returned its group yet: suspended, it has
// started nothing.
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

// Close lets go of the job's handle; the group goes on, and is from here taken as gone.
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
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return &os.PathError{Op: "access list", Path: dir, Err: err}
	}
	fix := `icacls "` + dir + `" /inheritance:r /grant:r "%USERNAME%:(OI)(CI)F"`
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return &OpenDirError{Dir: dir, To: "no access list: everyone", Fix: fix}
	}
	me, err := currentUser()
	if err != nil {
		return err
	}
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
			return &OpenDirError{Dir: dir, To: fmt.Sprintf("an access entry of a kind not known here, %d", ace.Header.AceType), Fix: fix}
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if ace.Mask == 0 || sid.Equals(me) || sid.IsWellKnown(windows.WinLocalSystemSid) ||
			sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) || sid.IsWellKnown(windows.WinCreatorOwnerSid) {
			continue
		}
		return &OpenDirError{Dir: dir, To: sid.String(), Fix: fix}
	}
	return nil
}

// MakePrivate closes to other users a directory this process has just made: an access list of
// its own, inherited from no parent, for this user, the system and the administrators.
func MakePrivate(dir string) error {
	me, err := currentUser()
	if err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + me.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	err = windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
	if err != nil {
		return &os.PathError{Op: "access list", Path: dir, Err: err}
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
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.WSAEACCES)
}
