// Package selfupdate replaces the running tork binary with a fresh build of
// the latest tagged release.
//
// It deliberately builds from source with the local Go toolchain instead of
// downloading a prebuilt binary: `go install module@version` fetches the tagged
// source and verifies it against the public checksum database
// (sum.golang.org), so a tampered tag or mirror is rejected. Every download,
// module cache, build cache and intermediate binary lives in one temporary
// directory that is removed when the update finishes, succeeds or not.
package selfupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Module is the module path tork is published under.
const Module = "github.com/melqtx/tork"

const mainPackage = Module + "/cmd/tork"

// GoRunner runs the go command in dir with env appended to the process
// environment and returns its standard output.
type GoRunner func(ctx context.Context, dir string, env []string, args ...string) ([]byte, error)

// Updater checks for and installs newer tork releases.
type Updater struct {
	// Current is the running version, for example "v0.5.0", "0.5.0" or "dev".
	Current string
	// Exe is the resolved path of the binary to replace.
	Exe string
	// Go runs the go command.
	Go GoRunner
	// TempDir is where the scratch directory is created; "" means the OS default.
	TempDir string
	// Out receives progress messages.
	Out io.Writer
}

// Options controls a Run.
type Options struct {
	// CheckOnly reports whether an update exists without installing it.
	CheckOnly bool
	// Force rebuilds the latest release even when Current is not older.
	Force bool
}

// Result describes what a Run found and did.
type Result struct {
	Current   string
	Latest    string
	Available bool
	Updated   bool
}

// New returns an Updater for the running executable, using the go command
// found on PATH.
func New(current string, out io.Writer) (*Updater, error) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		return nil, errors.New("tork update builds the new release from source and needs Go 1.26.10 or newer on PATH (https://go.dev/dl)")
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate running binary: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return &Updater{Current: current, Exe: exe, Go: ExecGo(goBin), Out: out}, nil
}

// ExecGo returns a GoRunner that executes goBin.
func ExecGo(goBin string) GoRunner {
	return func(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, goBin, args...)
		cmd.Dir = dir
		// Later entries win, so env overrides anything inherited.
		cmd.Env = append(os.Environ(), env...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			if msg := strings.TrimSpace(stderr.String()); msg != "" {
				return nil, fmt.Errorf("go %s: %w\n%s", args[0], err, msg)
			}
			return nil, fmt.Errorf("go %s: %w", args[0], err)
		}
		return stdout.Bytes(), nil
	}
}

// Run looks up the latest release and, unless opts.CheckOnly, builds it and
// replaces u.Exe when it is newer than u.Current (or opts.Force is set).
func (u *Updater) Run(ctx context.Context, opts Options) (res Result, err error) {
	res.Current = u.Current
	if !opts.CheckOnly {
		if err := CheckReplaceable(u.Exe); err != nil {
			return res, err
		}
	}

	work, err := os.MkdirTemp(u.TempDir, "tork-update-*")
	if err != nil {
		return res, fmt.Errorf("create scratch directory: %w", err)
	}
	defer func() {
		if rmErr := removeAll(work); rmErr != nil && err == nil {
			err = fmt.Errorf("remove scratch directory %s: %w", work, rmErr)
		}
	}()
	env := buildEnv(work)

	u.printf("checking %s for a newer release…\n", Module)
	latest, err := u.latest(ctx, work, env)
	if err != nil {
		return res, err
	}
	res.Latest = latest
	res.Available = IsNewer(u.Current, latest)
	u.printf("current %s · latest %s\n", u.Current, latest)

	if opts.CheckOnly {
		if res.Available {
			u.printf("an update is available; run tork update to install it\n")
		} else {
			u.printf("already up to date\n")
		}
		return res, nil
	}
	if !res.Available && !opts.Force {
		u.printf("already up to date\n")
		return res, nil
	}

	u.printf("building %s from source (this can take a minute)…\n", latest)
	if _, err := u.Go(ctx, work, env, "install", "-trimpath", mainPackage+"@"+latest); err != nil {
		return res, fmt.Errorf("build %s: %w", latest, err)
	}
	built := filepath.Join(work, "bin", "tork"+exeSuffix())
	if _, err := os.Stat(built); err != nil {
		return res, fmt.Errorf("build %s: no binary produced: %w", latest, err)
	}
	if err := Replace(u.Exe, built); err != nil {
		return res, fmt.Errorf("install %s over %s: %w", latest, u.Exe, err)
	}
	res.Updated = true
	u.printf("updated tork %s → %s (%s)\n", u.Current, latest, u.Exe)
	return res, nil
}

func (u *Updater) latest(ctx context.Context, work string, env []string) (string, error) {
	out, err := u.Go(ctx, work, env, "list", "-m", "-json", Module+"@latest")
	if err != nil {
		return "", fmt.Errorf("look up latest release: %w", err)
	}
	var mod struct{ Version string }
	if err := json.Unmarshal(out, &mod); err != nil {
		return "", fmt.Errorf("look up latest release: %w", err)
	}
	if _, _, ok := parseVersion(mod.Version); !ok {
		return "", fmt.Errorf("look up latest release: unexpected version %q", mod.Version)
	}
	return mod.Version, nil
}

func (u *Updater) printf(format string, args ...any) {
	if u.Out != nil {
		fmt.Fprintf(u.Out, format, args...)
	}
}

// buildEnv confines every cache and output of the go command to work, so
// removing work removes everything the update downloaded or built.
func buildEnv(work string) []string {
	return []string{
		"GOBIN=" + filepath.Join(work, "bin"),
		"GOPATH=" + filepath.Join(work, "gopath"),
		"GOMODCACHE=" + filepath.Join(work, "mod"),
		"GOCACHE=" + filepath.Join(work, "cache"),
		// Module cache files are read-only by default; keep them deletable.
		"GOFLAGS=-modcacherw",
		"GO111MODULE=on",
		// A cross-compiling environment must not produce a binary for
		// another platform over the one that is running.
		"GOOS=" + runtime.GOOS,
		"GOARCH=" + runtime.GOARCH,
	}
}

// CheckReplaceable reports why exe cannot be replaced in place: it belongs to
// a package manager, or its directory is not writable.
func CheckReplaceable(exe string) error {
	if manager, hint := ManagedBy(exe); manager != "" {
		return fmt.Errorf("tork at %s is managed by %s; %s", exe, manager, hint)
	}
	probe, err := os.CreateTemp(filepath.Dir(exe), ".tork-update-probe-*")
	if err != nil {
		return fmt.Errorf("cannot replace %s: %w", exe, err)
	}
	name := probe.Name()
	probe.Close()
	os.Remove(name)
	return nil
}

// ManagedBy reports the package manager that owns exe, with a hint on how to
// update through it, or "" when exe looks like a manual install.
func ManagedBy(exe string) (manager, hint string) {
	p := filepath.ToSlash(exe)
	switch {
	case strings.Contains(p, "/nix/store/"):
		return "Nix", "update it through your Nix configuration"
	case strings.Contains(p, "/Cellar/"):
		return "Homebrew", "update it with: brew upgrade tork"
	case strings.HasPrefix(p, "/usr/bin/"), strings.HasPrefix(p, "/usr/sbin/"):
		return "your system package manager", "update it with that (for example: pacman -Syu)"
	}
	return "", ""
}

// Replace installs built over exe. On Unix the new file is renamed over the
// old one, which is atomic and safe while exe is running. Windows refuses to
// overwrite or delete a running executable but allows renaming it, so exe is
// moved aside to exe+".old" first; RemoveLeftover deletes it on a later start.
func Replace(exe, built string) error {
	staged, err := stage(exe, built)
	if err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return swapAside(exe, staged)
	}
	return swapRename(exe, staged)
}

// stage copies built next to exe so the final rename stays on one filesystem.
func stage(exe, built string) (string, error) {
	src, err := os.Open(built)
	if err != nil {
		return "", err
	}
	defer src.Close()
	dst, err := os.CreateTemp(filepath.Dir(exe), ".tork-new-*"+exeSuffix())
	if err != nil {
		return "", err
	}
	name := dst.Name()
	_, copyErr := io.Copy(dst, src)
	closeErr := dst.Close()
	if err := errors.Join(copyErr, closeErr, os.Chmod(name, 0o755)); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

func swapRename(exe, staged string) error {
	if err := os.Rename(staged, exe); err != nil {
		os.Remove(staged)
		return err
	}
	return nil
}

func swapAside(exe, staged string) error {
	old := exe + ".old"
	os.Remove(old) // a leftover from an earlier update
	if err := os.Rename(exe, old); err != nil {
		os.Remove(staged)
		return err
	}
	if err := os.Rename(staged, exe); err != nil {
		if rbErr := os.Rename(old, exe); rbErr != nil {
			return fmt.Errorf("%w (and restoring the previous binary from %s failed: %v)", err, old, rbErr)
		}
		os.Remove(staged)
		return err
	}
	// Succeeds unless old is the binary that is still running.
	os.Remove(old)
	return nil
}

// RemoveLeftover deletes the previous binary a Windows update had to leave
// behind because it was still running. It is cheap and safe to call on every
// start.
func RemoveLeftover() {
	if runtime.GOOS != "windows" {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	os.Remove(exe + ".old")
}

// removeAll is os.RemoveAll that retries after clearing read-only bits, which
// Windows otherwise refuses to delete.
func removeAll(dir string) error {
	if err := os.RemoveAll(dir); err == nil {
		return nil
	}
	_ = filepath.WalkDir(dir, func(p string, _ fs.DirEntry, err error) error {
		if err == nil {
			_ = os.Chmod(p, 0o700)
		}
		return nil
	})
	return os.RemoveAll(dir)
}

// IsNewer reports whether latest is a newer release than current. Versions
// may carry a leading "v" (go install stamps one, release packages do not).
// An unknown current version, such as "dev" from a plain `go build`, counts
// as older than any release; an unparseable latest is never newer.
func IsNewer(current, latest string) bool {
	l, lPre, ok := parseVersion(latest)
	if !ok {
		return false
	}
	c, cPre, ok := parseVersion(current)
	if !ok {
		return true
	}
	for i := range l {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	switch {
	case lPre == cPre:
		return false
	case lPre == "":
		return true // a release is newer than its pre-releases
	case cPre == "":
		return false
	default:
		return lPre > cPre
	}
}

// parseVersion splits "v1.2.3-pre+build" into its numbers and pre-release.
func parseVersion(v string) (nums [3]int, pre string, ok bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	v, _, _ = strings.Cut(v, "+")
	v, pre, _ = strings.Cut(v, "-")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return nums, "", false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nums, "", false
		}
		nums[i] = n
	}
	return nums, pre, true
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}
