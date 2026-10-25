//go:build !windows

package session

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

const Supported = true

var ErrRunning = errors.New("background session is already running")

func runtimeDir(dir string) (string, error) {
	path := filepath.Join(dir, "session")
	if err := os.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
		return "", err
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("session path must be a real directory")
	}
	if err := os.Chmod(path, 0700); err != nil {
		return "", err
	}
	return path, nil
}

// Listen holds an OS lock for the lifetime of the worker. Only its owner can
// replace a stale socket; parallel launches cannot create competing engines.
func Listen(dir string) (net.Listener, func(), error) {
	run, err := runtimeDir(dir)
	if err != nil {
		return nil, nil, err
	}
	fd, err := syscall.Open(filepath.Join(run, "owner.lock"), syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, nil, err
	}
	lock := os.NewFile(uintptr(fd), "session owner")
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, nil, ErrRunning
		}
		return nil, nil, err
	}
	path := filepath.Join(run, "ui.sock")
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			lock.Close()
			return nil, nil, errors.New("session socket path is occupied by a regular file")
		}
		if err := os.Remove(path); err != nil {
			lock.Close()
			return nil, nil, err
		}
	} else if !os.IsNotExist(err) {
		lock.Close()
		return nil, nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		lock.Close()
		return nil, nil, fmt.Errorf("listen for UI: %w", err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	if err := os.Chmod(path, 0600); err != nil {
		ln.Close()
		os.Remove(path)
		lock.Close()
		return nil, nil, err
	}
	return ln, func() { ln.Close(); os.Remove(path); lock.Close() }, nil
}

func dial(dir string) (net.Conn, error) {
	return net.DialTimeout("unix", filepath.Join(dir, "session", "ui.sock"), connectTimeout)
}

func Start(executable, dir string, args []string) error {
	run, err := runtimeDir(dir)
	if err != nil {
		return err
	}
	fd, err := syscall.Open(filepath.Join(run, "worker.log"), syscall.O_CREAT|syscall.O_APPEND|syscall.O_WRONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	log := os.NewFile(uintptr(fd), "background log")
	defer log.Close()
	cmd := exec.Command(executable, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
