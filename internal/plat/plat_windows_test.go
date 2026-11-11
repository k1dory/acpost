package plat

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// openToAll gives everyone the directory, as one made under a shared folder is.
func openToAll(dir string) error {
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;WD)")
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

// The repair the error names closes the directory: explicit grants to others are removed too.
func TestPrivateDirRepairCloses(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "d")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := openToAll(dir); err != nil {
		t.Fatal(err)
	}
	var open *OpenDirError
	if err := PrivateDir(dir); !errors.As(err, &open) {
		t.Fatalf("open to everyone: %v", err)
	}
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /c ` + open.Fix} // as a person pastes it
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", open.Fix, err, out)
	}
	if err := PrivateDir(dir); err != nil {
		t.Fatalf("after %s: %v", open.Fix, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "f"), nil, 0o600); err != nil {
		t.Fatalf("the repaired directory is closed to its own user: %v", err)
	}
}

// A kill that comes the moment the group is known, before the command is in it, is not missed:
// the command never runs.
func TestKillWhileStartingRunsNothing(t *testing.T) {
	mark := filepath.Join(t.TempDir(), "ran")
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "PLAT_HELPER=mark", "PLAT_MARK="+mark)
	g, err := start(cmd, false, func(g *Group) { _ = g.Kill() })
	if err == nil {
		_ = cmd.Wait()
		g.Close()
		t.Fatal("a command killed while starting was started")
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(mark); err == nil {
		t.Fatal("a command killed while starting ran")
	}
}

// A group started to last ends with the process that started it, however that one ends: a node
// killed leaves no agent, and nothing an agent started.
func TestGroupEndsWithItsStarter(t *testing.T) {
	owner := exec.Command(os.Args[0])
	owner.Env = append(os.Environ(), "PLAT_HELPER=owner")
	out, err := owner.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	pid, perr := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || perr != nil {
		_ = owner.Process.Kill()
		t.Fatalf("%q %v", line, err)
	}
	t.Cleanup(func() {
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Kill()
		}
	})
	if !Alive(pid) {
		t.Fatal("the group's leader is not seen running")
	}
	_ = owner.Process.Kill()
	_ = owner.Wait()
	if !gone(pid) {
		t.Fatal("the group outlived the process that started it")
	}
}

// A program installed by npm is a .cmd file: it starts, from a path with a space, and what it
// starts is in the group.
func TestStartsACmdFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "with space")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(dir, "agent.cmd")
	if err := os.WriteFile(wrapper, []byte("@echo off\r\n\""+os.Args[0]+"\" %*\r\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(wrapper)
	g, child := family(t, cmd, Start)
	if err := g.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	if !gone(child) {
		t.Fatal("what the .cmd file's program started outlived the group's kill")
	}
}
