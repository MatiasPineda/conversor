package app

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"time"

	"conversor/internal/engines"
	"conversor/internal/i18n"
)

// One app runs per user. The first launch listens on a local socket in a
// folder only this user can open; later launches send their request there
// and exit. The protocol is one JSON Request per connection, answered with
// "ok".

// maxRequest bounds a request; a request is a list of paths.
const maxRequest = 4 << 20

// RuntimeDir is $CONVERSOR_RUNTIME_DIR, else $XDG_RUNTIME_DIR/conversor,
// else a per-user folder.
func RuntimeDir() string {
	if d := os.Getenv("CONVERSOR_RUNTIME_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" && runtime.GOOS == "linux" {
		return filepath.Join(d, "conversor")
	}
	if runtime.GOOS == "windows" {
		if d := engines.DataDir(); d != "" {
			return filepath.Join(d, "run")
		}
	}
	name := "user"
	if u, err := user.Current(); err == nil {
		name = u.Uid
	}
	return filepath.Join(os.TempDir(), "conversor-"+name)
}

// Primary is the running app's end of the socket.
type Primary struct {
	ln      net.Listener
	release func()
}

// Become makes this process the app, or hands req to the one already
// running. It returns a Primary when this process is the app, or nil when
// the request was handed over. A lock file decides which process is the
// app; the system drops the lock if the app dies, so a crash never blocks
// the next launch. File managers start one process per selected file, so
// several may race here at once.
func Become(req Request) (*Primary, error) {
	dir := RuntimeDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(dir)
		if err != nil {
			return nil, err
		}
		if info.Mode().Perm()&0o077 != 0 {
			if err := os.Chmod(dir, 0o700); err != nil {
				return nil, errors.New(i18n.T("%s is open to other users", dir))
			}
		}
	}
	sock := filepath.Join(dir, "app.sock")
	if len(sock) > 100 {
		// Socket paths are limited to about 108 bytes; use a short private
		// folder named after the long one.
		short := filepath.Join(os.TempDir(), fmt.Sprintf("conversor-%x", crc32.ChecksumIEEE([]byte(dir))))
		if err := os.MkdirAll(short, 0o700); err != nil {
			return nil, err
		}
		sock = filepath.Join(short, "app.sock")
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		release, ok, err := tryLock(filepath.Join(dir, "app.lock"))
		if err != nil {
			return nil, err
		}
		if ok {
			// Whatever socket is there is left from a crash.
			os.Remove(sock)
			ln, err := net.Listen("unix", sock)
			if err != nil {
				release()
				return nil, err
			}
			return &Primary{ln: ln, release: release}, nil
		}
		// Another process is the app, or is starting to be.
		if err := send(sock, req); err == nil {
			return nil, nil
		}
		if time.Now().After(deadline) {
			return nil, errors.New(i18n.T("the running app isn't answering"))
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func send(sock string, req Request) error {
	conn, err := net.DialTimeout("unix", sock, time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return err
	}
	reply, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil || reply != "ok\n" {
		return errors.New(i18n.T("the running app didn't take the request"))
	}
	return nil
}

// Listen accepts requests on a goroutine and hands each to handle.
func (p *Primary) Listen(handle func(Request)) {
	go func() {
		for {
			conn, err := p.ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				var req Request
				dec := json.NewDecoder(bufio.NewReader(&limited{conn, maxRequest}))
				if dec.Decode(&req) != nil {
					return
				}
				conn.Write([]byte("ok\n"))
				handle(req)
			}()
		}
	}()
}

// Close stops listening and gives up being the app.
func (p *Primary) Close() {
	p.ln.Close()
	p.release()
}

type limited struct {
	c net.Conn
	n int
}

func (l *limited) Read(b []byte) (int, error) {
	if l.n <= 0 {
		return 0, errors.New(i18n.T("request too large"))
	}
	if len(b) > l.n {
		b = b[:l.n]
	}
	n, err := l.c.Read(b)
	l.n -= n
	return n, err
}
