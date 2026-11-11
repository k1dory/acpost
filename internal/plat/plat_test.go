package plat

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The test binary is also the processes the group tests start: a parent that starts a child and
// tells its id, and that child, both sleeping.
func TestMain(m *testing.M) {
	switch os.Getenv("PLAT_HELPER") {
	case "parent":
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), "PLAT_HELPER=child")
		if err := child.Start(); err != nil {
			fmt.Println("error:", err)
			os.Exit(1)
		}
		fmt.Println(child.Process.Pid)
		time.Sleep(time.Minute)
		os.Exit(0)
	case "child":
		time.Sleep(time.Minute)
		os.Exit(0)
	case "owner": // starts a group to last, tells its leader's id, and waits to be killed
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), "PLAT_HELPER=child")
		if _, err := Start(child); err != nil {
			fmt.Println("error:", err)
			os.Exit(1)
		}
		fmt.Println(child.Process.Pid)
		time.Sleep(time.Minute)
		os.Exit(0)
	case "args": // tells the arguments it got
		b, _ := json.Marshal(os.Args[1:])
		fmt.Println(string(b))
		os.Exit(0)
	case "mark": // says it ran
		_ = os.WriteFile(os.Getenv("PLAT_MARK"), []byte("ran"), 0o600)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// family starts the parent as a group (by start) and returns its child's process id.
func family(t *testing.T, cmd *exec.Cmd, start func(*exec.Cmd) (*Group, error)) (*Group, int) {
	t.Helper()
	cmd.Env = append(os.Environ(), "PLAT_HELPER=parent")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	g, err := start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Kill(); _ = cmd.Wait(); g.Close() })
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatalf("the parent told no child: %q %v", line, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(line)
	}
	if !Alive(pid) {
		t.Fatalf("the child %d is not seen running", pid)
	}
	return g, pid
}

func gone(pid int) bool {
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if !Alive(pid) {
			return true
		}
	}
	return false
}

func TestKillEndsWhatTheCommandStarted(t *testing.T) {
	cmd := exec.Command(os.Args[0])
	g, child := family(t, cmd, Start)
	if g.Gone() {
		t.Fatal("a running group taken as gone")
	}
	if err := g.Stop(); errors.Is(err, ErrGone) {
		t.Fatal("a running group has nothing to stop")
	}
	if err := g.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	if !gone(child) {
		t.Fatal("the command's child outlived the group's kill")
	}
	for end := time.Now().Add(10 * time.Second); !g.Gone(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("a killed group is not gone")
		}
	}
	if err := g.Stop(); !errors.Is(err, ErrGone) {
		t.Fatalf("stop of a group gone: %v", err)
	}
}

// The leader's own end leaves what it started in the group: that is still killed.
func TestKillAfterTheLeaderIsGone(t *testing.T) {
	cmd := exec.Command(os.Args[0])
	g, child := family(t, cmd, Start)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	if !Alive(child) {
		t.Skip("the child went with its parent here")
	}
	if g.Gone() {
		t.Fatal("a group with a process left taken as gone")
	}
	if err := g.Kill(); err != nil {
		t.Fatal(err)
	}
	if !gone(child) {
		t.Fatal("what the leader left outlived the group's kill")
	}
}

func TestCancelEndsWhatTheCommandStarted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0])
	_, child := family(t, cmd, StartCancelable)
	cancel()
	_ = cmd.Wait()
	if !gone(child) {
		t.Fatal("the command's child outlived its cancelled context")
	}
}

func TestOutput(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "go", "env", "GOOS")
	out, err := Output(cmd)
	if err != nil || strings.TrimSpace(string(out)) == "" {
		t.Fatalf("%q %v", out, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Output(exec.CommandContext(ctx, "go", "env", "GOOS")); err == nil {
		t.Fatal("a command of an ended context ran")
	}
}

func TestLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	open := func() *os.File {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { f.Close() })
		return f
	}
	a, b := open(), open()
	if err := Lock(a, false); err != nil {
		t.Fatal(err)
	}
	if err := Lock(b, false); !errors.Is(err, ErrLocked) {
		t.Fatalf("a held lock taken again: %v", err)
	}
	got := make(chan error, 1)
	go func() { got <- Lock(b, true) }()
	select {
	case err := <-got:
		t.Fatalf("a held lock did not make its second taker wait: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	if err := Unlock(a); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-got:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a lock given back is not taken by the one waiting")
	}
	// A lock goes with its file's close.
	b.Close()
	if err := Lock(a, false); err != nil {
		t.Fatalf("after the holder's close: %v", err)
	}
}

func TestPrivateDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b")
	if err := MkdirPrivate(dir); err != nil {
		t.Fatal(err)
	}
	if err := PrivateDir(dir); err != nil {
		t.Fatalf("a directory made private is not: %v", err)
	}
	if err := PrivateDir(filepath.Dir(dir)); err != nil {
		t.Fatalf("its parent, made on the way, is not private: %v", err)
	}
	if err := MkdirPrivate(dir); err != nil {
		t.Fatalf("a directory already there: %v", err)
	}
	if err := openToAll(dir); err != nil {
		t.Fatal(err)
	}
	var open *OpenDirError
	if err := PrivateDir(dir); !errors.As(err, &open) || open.Fix == "" {
		t.Fatalf("a directory open to everyone taken as private: %v", err)
	}
	// One already there is left as it is: not closed behind its owner's back.
	if err := MkdirPrivate(dir); err != nil {
		t.Fatal(err)
	}
	if PrivateDir(dir) == nil {
		t.Fatal("a directory that was there was changed")
	}
}

func TestAlive(t *testing.T) {
	if !Alive(os.Getpid()) {
		t.Fatal("this process is not seen running")
	}
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "PLAT_HELPER=child")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	if !gone(pid) || Alive(0) || Alive(-1) {
		t.Fatal("a process ended and reaped is seen running")
	}
}

func TestFileLinks(t *testing.T) {
	for _, c := range []struct {
		uri    string
		drives bool
		want   string
	}{
		{"/home/u/a b.txt", false, "/home/u/a b.txt"},
		{"/C:/Users/u/a b.txt", true, `C:\Users\u\a b.txt`},
		{"/c:/x", true, `c:\x`},
		{"/Users/u/x", true, `\Users\u\x`}, // no drive: of the current one
		{"/C:/x", false, "/C:/x"},          // a directory named C: elsewhere
	} {
		if got := uriPath(c.uri, c.drives); got != c.want {
			t.Errorf("uriPath(%q, %v) = %q, want %q", c.uri, c.drives, got, c.want)
		}
	}
	abs, err := filepath.Abs(filepath.Join(t.TempDir(), "dir with space", "ф.txt"))
	if err != nil {
		t.Fatal(err)
	}
	uri := FileURI(abs)
	if !strings.HasPrefix(uri, "file:///") || strings.Contains(uri, `\`) || strings.Contains(uri, " ") {
		t.Fatalf("%s is not a file link", uri)
	}
	u, err := url.Parse(uri)
	if err != nil || URIPath(u.Path) != abs {
		t.Fatalf("%s reads back as %v (%v), want %q", uri, u, err, abs)
	}
}

// A file that was open to others (there before, or moved in) is closed.
func TestPrivateFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "store.db")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := openToAll(file); err != nil {
		t.Fatal(err)
	}
	if PrivateDir(file) == nil {
		t.Fatal("a file open to everyone taken as private")
	}
	if err := PrivateFile(file); err != nil {
		t.Fatal(err)
	}
	if err := PrivateDir(file); err != nil {
		t.Fatalf("a file closed is not private: %v", err)
	}
	if err := PrivateFile(filepath.Join(t.TempDir(), "absent")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a file not there: %v", err)
	}
}

// A file made private is so from its first moment, in a directory that hands everyone its
// access too: a secret written there is not read by others.
func TestCreatePrivateTempInAnOpenDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := openToAll(dir); err != nil {
		t.Fatal(err)
	}
	f, err := CreatePrivateTemp(dir, "secrets.")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := PrivateDir(f.Name()); err != nil {
		t.Fatalf("made in a directory open to everyone: %v", err)
	}
	if _, err := f.Write([]byte("s3cret")); err != nil {
		t.Fatal(err)
	}
	g, err := CreatePrivateTemp(dir, "secrets.")
	if err != nil || g.Name() == f.Name() {
		t.Fatalf("a second one: %v %v", g, err)
	}
	g.Close()
}

// A file replaced by a rename while others read it: every reader gets one whole version, and no
// writer is refused (a reader that kept the file from being renamed over would, on Windows).
func TestReadFileWhileItIsReplaced(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.json")
	write := func(i int) error {
		f, err := CreatePrivateTemp(dir, "secrets.")
		if err != nil {
			return err
		}
		_, err = f.Write([]byte(strings.Repeat(strconv.Itoa(i%10), 4096)))
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
		return Rename(f.Name(), path)
	}
	if err := write(0); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	bad := make(chan string, 4)
	done := make(chan struct{})
	for range 4 {
		go func() {
			defer func() { done <- struct{}{} }()
			for {
				select {
				case <-stop:
					return
				default:
				}
				b, err := ReadFile(path)
				if err != nil || len(b) != 4096 || strings.Trim(string(b), string(b[:1])) != "" {
					bad <- fmt.Sprintf("%d bytes, %v", len(b), err)
					return
				}
			}
		}()
	}
	for i := 1; i <= 300; i++ {
		if err := write(i); err != nil {
			close(stop)
			t.Fatalf("write %d: %v", i, err)
		}
	}
	close(stop)
	for range 4 {
		<-done
	}
	select {
	case why := <-bad:
		t.Fatal("a reader: " + why)
	default:
	}
}

// Which file:// paths name a file of this machine's disks: on Windows not a share of another
// machine, a device, a path with no drive, or a stream of a file, however they are written.
func TestLocalFile(t *testing.T) {
	for _, c := range []struct {
		p      string
		drives bool
		want   string
		ok     bool
	}{
		{"/home/u/a.txt", false, "/home/u/a.txt", true},
		{"relative/a.txt", false, "", false},
		{"", false, "", false},
		{"/C:/Users/u/a b.txt", true, `C:\Users\u\a b.txt`, true},
		{"/c:/x", true, `c:\x`, true},
		{"//server/share/a.txt", true, "", false}, // file:////server/share/a.txt
		{`\\server\share\a.txt`, true, "", false}, // the same, its slashes percent-encoded in the link
		{"//./pipe/x", true, "", false},
		{"//?/C:/x", true, "", false},
		{"/Users/u/x", true, "", false},   // no drive
		{"/C:x", true, "", false},         // of the drive's own directory
		{"/C:/x:stream", true, "", false}, // a stream of a file
		{"C:/x", true, "", false},         // not a link's absolute path
		{"/1:/x", true, "", false},
	} {
		got, ok := localFile(c.p, c.drives)
		if ok != c.ok || ok && got != c.want {
			t.Errorf("localFile(%q, %v) = %q, %v; want %q, %v", c.p, c.drives, got, ok, c.want, c.ok)
		}
	}
}
