package selfupdate

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestIsNewer(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"v0.4.1", "v0.5.0", true},
		{"0.4.1", "v0.5.0", true}, // brew/AUR stamp without the v
		{"v0.5.0", "v0.5.0", false},
		{"0.5.0", "v0.5.0", false},
		{"v0.5.1", "v0.5.0", false},
		{"v0.10.0", "v0.9.9", false}, // numeric, not lexical
		{"v0.9.9", "v0.10.0", true},
		{"v1.0.0-rc1", "v1.0.0", true}, // pre-release is older than its release
		{"v1.0.0", "v1.1.0-rc1", true},
		{"v0.5.1-0.20261026131709-7f7502a63470", "v0.5.0", false}, // pseudo-version past the tag
		{"v0.5.0+dirty", "v0.5.0", false},
		{"dev", "v0.5.0", true},
		{"", "v0.5.0", true},
		{"v0.4.1", "garbage", false},
	}
	for _, c := range cases {
		if got := IsNewer(c.current, c.latest); got != c.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}

func TestManagedBy(t *testing.T) {
	cases := map[string]string{
		"/nix/store/abc-tork-0.5.0/bin/tork":          "Nix",
		"/opt/homebrew/Cellar/tork/0.5.0/bin/tork":    "Homebrew",
		"/usr/local/Cellar/tork/0.5.0/bin/tork":       "Homebrew",
		"/usr/bin/tork":                               "your system package manager",
		"/usr/local/bin/tork":                         "",
		"/home/me/go/bin/tork":                        "",
		`C:\Users\me\AppData\Local\Programs\tork.exe`: "",
	}
	for exe, want := range cases {
		if got, _ := ManagedBy(exe); got != want {
			t.Errorf("ManagedBy(%q) = %q, want %q", exe, got, want)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func assertOnly(t *testing.T, dir string, names ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if strings.Join(got, ",") != strings.Join(names, ",") {
		t.Fatalf("%s contains %v, want %v", dir, got, names)
	}
}

func TestReplaceStrategies(t *testing.T) {
	for name, swap := range map[string]func(exe, staged string) error{
		"rename": swapRename,
		"aside":  swapAside,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			exe := filepath.Join(dir, "tork")
			built := filepath.Join(t.TempDir(), "tork-built")
			writeFile(t, exe, "old")
			writeFile(t, built, "new")
			staged, err := stage(exe, built)
			if err != nil {
				t.Fatal(err)
			}
			if err := swap(exe, staged); err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, exe); got != "new" {
				t.Fatalf("exe content = %q, want new", got)
			}
			// Nothing staged or set aside may survive when the old binary is
			// not running.
			assertOnly(t, dir, "tork")
		})
	}
}

func TestSwapAsideRollsBack(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "tork")
	writeFile(t, exe, "old")
	missing := filepath.Join(dir, "does-not-exist")
	if err := swapAside(exe, missing); err == nil {
		t.Fatal("expected an error for a missing staged binary")
	}
	if got := readFile(t, exe); got != "old" {
		t.Fatalf("exe content = %q after rollback, want old", got)
	}
	assertOnly(t, dir, "tork")
}

// fakeGo emulates the two go commands Run issues.
func fakeGo(t *testing.T, latest string, calls *[]string) GoRunner {
	return func(_ context.Context, dir string, env []string, args ...string) ([]byte, error) {
		*calls = append(*calls, strings.Join(args, " "))
		if dir == "" {
			t.Error("go ran without the scratch directory as its working directory")
		}
		var gobin string
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, "GOBIN="); ok {
				gobin = v
			}
		}
		if gobin == "" || !strings.HasPrefix(gobin, dir) {
			t.Errorf("GOBIN %q is not inside the scratch directory %q", gobin, dir)
		}
		switch args[0] {
		case "list":
			return []byte(`{"Path":"` + Module + `","Version":"` + latest + `"}`), nil
		case "install":
			if err := os.MkdirAll(gobin, 0o755); err != nil {
				return nil, err
			}
			// Also leave junk in the caches to prove they get cleaned up.
			if err := os.MkdirAll(filepath.Join(dir, "mod", "cache"), 0o755); err != nil {
				return nil, err
			}
			ro := filepath.Join(dir, "mod", "cache", "readonly")
			if err := os.WriteFile(ro, []byte("x"), 0o444); err != nil {
				return nil, err
			}
			return nil, os.WriteFile(filepath.Join(gobin, "tork"+exeSuffix()), []byte("built "+latest), 0o755)
		}
		return nil, errors.New("unexpected go command")
	}
}

func newTestUpdater(t *testing.T, current, latest string, calls *[]string) (*Updater, string, string) {
	t.Helper()
	installDir := t.TempDir()
	scratch := t.TempDir()
	exe := filepath.Join(installDir, "tork"+exeSuffix())
	writeFile(t, exe, "running "+current)
	var out bytes.Buffer
	return &Updater{Current: current, Exe: exe, Go: fakeGo(t, latest, calls), TempDir: scratch, Out: &out}, exe, scratch
}

func TestRunUpdatesAndCleansUp(t *testing.T) {
	var calls []string
	u, exe, scratch := newTestUpdater(t, "v0.4.1", "v0.5.0", &calls)
	res, err := u.Run(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Updated || !res.Available || res.Latest != "v0.5.0" {
		t.Fatalf("unexpected result %+v", res)
	}
	if got := readFile(t, exe); got != "built v0.5.0" {
		t.Fatalf("exe content = %q", got)
	}
	want := "install -trimpath " + mainPackage + "@v0.5.0"
	if len(calls) != 2 || calls[1] != want {
		t.Fatalf("go calls = %q, want list then %q", calls, want)
	}
	assertOnly(t, scratch)
	assertOnly(t, filepath.Dir(exe), filepath.Base(exe))
}

func TestRunUpToDate(t *testing.T) {
	var calls []string
	u, exe, scratch := newTestUpdater(t, "v0.5.0", "v0.5.0", &calls)
	res, err := u.Run(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Updated || res.Available {
		t.Fatalf("unexpected result %+v", res)
	}
	if len(calls) != 1 {
		t.Fatalf("expected only the version lookup, got %q", calls)
	}
	if got := readFile(t, exe); got != "running v0.5.0" {
		t.Fatalf("exe was modified: %q", got)
	}
	assertOnly(t, scratch)
}

func TestRunForce(t *testing.T) {
	var calls []string
	u, exe, _ := newTestUpdater(t, "v0.5.0", "v0.5.0", &calls)
	res, err := u.Run(context.Background(), Options{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Updated {
		t.Fatalf("force did not update: %+v", res)
	}
	if got := readFile(t, exe); got != "built v0.5.0" {
		t.Fatalf("exe content = %q", got)
	}
}

func TestRunCheckOnly(t *testing.T) {
	var calls []string
	u, exe, scratch := newTestUpdater(t, "dev", "v0.5.0", &calls)
	res, err := u.Run(context.Background(), Options{CheckOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available || res.Updated {
		t.Fatalf("unexpected result %+v", res)
	}
	if got := readFile(t, exe); got != "running dev" {
		t.Fatalf("check-only modified the binary: %q", got)
	}
	assertOnly(t, scratch)
}

func TestRunRefusesManagedInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("package-manager paths are Unix paths")
	}
	var calls []string
	u := &Updater{Current: "v0.4.1", Exe: "/nix/store/abc-tork/bin/tork", Go: fakeGo(t, "v0.5.0", &calls)}
	if _, err := u.Run(context.Background(), Options{}); err == nil || !strings.Contains(err.Error(), "Nix") {
		t.Fatalf("expected a Nix refusal, got %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("go ran before the refusal: %q", calls)
	}
}
