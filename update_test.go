package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type updateTestTransport func(*http.Request) (*http.Response, error)

func (f updateTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type updateTestEntry struct {
	name string
	kind byte
	data string
	size int64
}

func updateTestArchive(t *testing.T, entries ...updateTestEntry) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		kind := entry.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		size := int64(len(entry.data))
		if entry.size != 0 {
			size = entry.size
		}
		if kind != tar.TypeReg {
			size = 0
		}
		h := &tar.Header{Name: entry.name, Mode: 0755, Size: size, Typeflag: kind}
		if kind == tar.TypeSymlink || kind == tar.TypeLink {
			h.Linkname = "../outside"
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(entry.data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

type updateTestFixture struct {
	tag           string
	draft         bool
	prerelease    bool
	archive       []byte
	checksums     string
	status        int
	requests      []string
	mu            sync.Mutex
	beforeArchive func()
	transportErr  error
}

func newUpdateTestFixture(t *testing.T) *updateTestFixture {
	t.Helper()
	f := &updateTestFixture{
		tag:     "v0.3.0",
		archive: updateTestArchive(t, updateTestEntry{name: "temo", data: "new executable\n"}),
	}
	f.rehash()
	return f
}

func (f *updateTestFixture) assetName() string {
	return "temo_" + strings.TrimPrefix(f.tag, "v") + "_linux_amd64.tar.gz"
}

func (f *updateTestFixture) rehash() {
	sum := sha256.Sum256(f.archive)
	f.checksums = hex.EncodeToString(sum[:]) + "  " + f.assetName() + "\n"
}

func (f *updateTestFixture) client() *http.Client {
	return &http.Client{Transport: updateTestTransport(func(r *http.Request) (*http.Response, error) {
		f.mu.Lock()
		f.requests = append(f.requests, r.URL.String())
		f.mu.Unlock()
		if f.transportErr != nil {
			return nil, f.transportErr
		}
		var body []byte
		switch r.URL.String() {
		case "https://api.github.com/repos/craftell/temo/releases/latest":
			base := "https://github.com/craftell/temo/releases/download/" + f.tag + "/"
			body, _ = json.Marshal(map[string]any{
				"tag_name": f.tag, "draft": f.draft, "prerelease": f.prerelease,
				"assets": []map[string]any{
					{"name": f.assetName(), "browser_download_url": base + f.assetName(), "size": len(f.archive)},
					{"name": "checksums.txt", "browser_download_url": base + "checksums.txt", "size": len(f.checksums)},
				},
			})
		case "https://github.com/craftell/temo/releases/download/" + f.tag + "/checksums.txt":
			body = []byte(f.checksums)
		case "https://github.com/craftell/temo/releases/download/" + f.tag + "/" + f.assetName():
			if f.beforeArchive != nil {
				f.beforeArchive()
			}
			body = f.archive
		default:
			return nil, fmt.Errorf("unexpected update request %s", r.URL)
		}
		status := f.status
		if status == 0 {
			status = http.StatusOK
		}
		return &http.Response{
			StatusCode: status, Status: fmt.Sprintf("%d", status),
			Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)),
			ContentLength: int64(len(body)), Request: r,
		}, nil
	})}
}

func updateTestExecutable(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "temo")
	if err := os.WriteFile(path, []byte("old executable\n"), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func updateTestUpdater(f *updateTestFixture, path string) updater {
	return updater{
		Client: f.client(), Executable: func() (string, error) { return path, nil },
		GOOS: "linux", GOARCH: "amd64", Version: "0.2.0", Channel: "github-release",
	}
}

func assertUpdateTestOldBinary(t *testing.T, path string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "old executable\n" {
		t.Fatalf("original executable changed: %q, %v", got, err)
	}
}

func TestUpdateCheckLeavesFilesystemAlone(t *testing.T) {
	f := newUpdateTestFixture(t)
	u := updateTestUpdater(f, "")
	u.Executable = func() (string, error) {
		t.Error("check-only resolved the executable")
		return "", errors.New("must not be called")
	}
	u.Channel = "unknown"
	result, err := u.Run(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Available || result.Updated {
		t.Fatalf("check should report an available unapplied update: %+v", result)
	}
	if len(f.requests) != 1 {
		t.Fatalf("check downloaded release assets: %v", f.requests)
	}
}

func TestUpdateCheckUnknownVersion(t *testing.T) {
	f := newUpdateTestFixture(t)
	u := updateTestUpdater(f, "")
	u.Version = "dev"
	u.Executable = func() (string, error) {
		t.Fatal("check-only resolved executable")
		return "", nil
	}
	result, err := u.Run(context.Background(), true)
	if err != nil || !result.CurrentUnknown || result.Updated {
		t.Fatalf("development check: %+v, %v", result, err)
	}
}

func TestUpdateInstallsVerifiedRelease(t *testing.T) {
	f := newUpdateTestFixture(t)
	path := updateTestExecutable(t)
	result, err := updateTestUpdater(f, path).Run(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "new executable\n" || !result.Updated {
		t.Fatalf("verified replacement: %q, %+v, %v", got, result, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm()&0111 == 0 {
		t.Fatalf("replacement is not executable: %v", err)
	}
}

func TestUpdateNeverDowngradesOrReinstalls(t *testing.T) {
	for _, version := range []string{"0.3.0", "0.4.0", "1.0.0", "v0.3.0"} {
		t.Run(version, func(t *testing.T) {
			f := newUpdateTestFixture(t)
			path := updateTestExecutable(t)
			u := updateTestUpdater(f, path)
			u.Version = version
			result, err := u.Run(context.Background(), false)
			if err != nil || result.Updated || result.Available {
				t.Fatalf("current version should remain untouched: %+v, %v", result, err)
			}
			assertUpdateTestOldBinary(t, path)
			if len(f.requests) != 1 {
				t.Fatalf("no-op downloaded assets: %v", f.requests)
			}
		})
	}
}

func TestUpdateRefusesUnstableOrInvalidRelease(t *testing.T) {
	for _, tc := range []struct {
		name, tag         string
		draft, prerelease bool
	}{
		{name: "prerelease flag", tag: "v0.3.0", prerelease: true},
		{name: "draft flag", tag: "v0.3.0", draft: true},
		{name: "prerelease tag", tag: "v0.3.0-rc.1"},
		{name: "build metadata", tag: "v0.3.0+test"},
		{name: "leading zero", tag: "v00.3.0"},
		{name: "incomplete", tag: "v0.3"},
		{name: "overflow", tag: "v18446744073709551616.0.0"},
		{name: "path", tag: "../v0.3.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newUpdateTestFixture(t)
			f.tag, f.draft, f.prerelease = tc.tag, tc.draft, tc.prerelease
			f.rehash()
			path := updateTestExecutable(t)
			if _, err := updateTestUpdater(f, path).Run(context.Background(), false); err == nil {
				t.Fatal("unsafe release accepted")
			}
			assertUpdateTestOldBinary(t, path)
		})
	}
}

func TestUpdateFailuresPreserveInstalledBinary(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*updateTestFixture)
	}{
		{"http error", func(f *updateTestFixture) { f.status = http.StatusForbidden }},
		{"transport error", func(f *updateTestFixture) { f.transportErr = errors.New("network unavailable") }},
		{"bad checksum", func(f *updateTestFixture) { f.checksums = strings.Repeat("0", 64) + "  " + f.assetName() + "\n" }},
		{"missing checksum", func(f *updateTestFixture) { f.checksums = strings.Repeat("0", 64) + "  different.tar.gz\n" }},
		{"malformed checksum", func(f *updateTestFixture) { f.checksums = "bad  " + f.assetName() + "\n" }},
		{"duplicate checksum", func(f *updateTestFixture) { f.checksums += f.checksums }},
		{"invalid gzip", func(f *updateTestFixture) { f.archive = []byte("not gzip"); f.rehash() }},
		{"missing executable", func(f *updateTestFixture) { f.archive = updateTestArchive(t, updateTestEntry{name: "README.md", data: "readme"}); f.rehash() }},
		{"duplicate executable", func(f *updateTestFixture) { f.archive = updateTestArchive(t, updateTestEntry{name: "temo", data: "one"}, updateTestEntry{name: "temo", data: "two"}); f.rehash() }},
		{"traversal", func(f *updateTestFixture) { f.archive = updateTestArchive(t, updateTestEntry{name: "../temo", data: "bad"}); f.rehash() }},
		{"absolute path", func(f *updateTestFixture) { f.archive = updateTestArchive(t, updateTestEntry{name: "/temo", data: "bad"}); f.rehash() }},
		{"symlink", func(f *updateTestFixture) { f.archive = updateTestArchive(t, updateTestEntry{name: "temo", kind: tar.TypeSymlink}); f.rehash() }},
		{"hardlink", func(f *updateTestFixture) { f.archive = updateTestArchive(t, updateTestEntry{name: "temo", kind: tar.TypeLink}); f.rehash() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newUpdateTestFixture(t)
			tc.change(f)
			path := updateTestExecutable(t)
			if _, err := updateTestUpdater(f, path).Run(context.Background(), false); err == nil {
				t.Fatal("invalid update accepted")
			}
			assertUpdateTestOldBinary(t, path)
		})
	}
}

func TestUpdateRefusesUnmanagedAndUnknownBuilds(t *testing.T) {
	for _, tc := range []struct{ channel, version string }{
		{"unknown", "0.2.0"}, {"homebrew", "0.2.0"}, {"github-release", "dev"},
	} {
		t.Run(tc.channel+"/"+tc.version, func(t *testing.T) {
			f := newUpdateTestFixture(t)
			path := updateTestExecutable(t)
			u := updateTestUpdater(f, path)
			u.Channel, u.Version = tc.channel, tc.version
			if _, err := u.Run(context.Background(), false); err == nil {
				t.Fatal("unmanaged or unknown build overwritten")
			}
			assertUpdateTestOldBinary(t, path)
		})
	}
}

func TestUpdateRefusesSymlinkInstallation(t *testing.T) {
	f := newUpdateTestFixture(t)
	path := updateTestExecutable(t)
	link := filepath.Join(t.TempDir(), "temo")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := updateTestUpdater(f, link).Run(context.Background(), false); err == nil {
		t.Fatal("symlink installation overwritten")
	}
	assertUpdateTestOldBinary(t, path)
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink changed: %v", err)
	}
}

func TestUpdateCancellation(t *testing.T) {
	f := newUpdateTestFixture(t)
	path := updateTestExecutable(t)
	u := updateTestUpdater(f, path)
	u.Client = &http.Client{Transport: updateTestTransport(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := u.Run(ctx, false); err == nil {
		t.Fatal("cancelled update succeeded")
	}
	assertUpdateTestOldBinary(t, path)
}

func TestUpdateRejectsOversizedArchiveEntry(t *testing.T) {
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "temo", Mode: 0755, Size: 65 << 20, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	f := newUpdateTestFixture(t)
	f.archive = out.Bytes()
	f.rehash()
	path := updateTestExecutable(t)
	if _, err := updateTestUpdater(f, path).Run(context.Background(), false); err == nil {
		t.Fatal("oversized binary header accepted")
	}
	assertUpdateTestOldBinary(t, path)
}

func TestUpdateConcurrentAttemptDoesNotReplaceActiveUpdate(t *testing.T) {
	f := newUpdateTestFixture(t)
	path := updateTestExecutable(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	f.beforeArchive = func() {
		once.Do(func() { close(entered) })
		<-release
	}
	done := make(chan error, 1)
	go func() {
		_, err := updateTestUpdater(f, path).Run(context.Background(), false)
		done <- err
	}()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("first updater did not reach download: %v", err)
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("first update stalled")
	}
	other := newUpdateTestFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	_, secondErr := updateTestUpdater(other, path).Run(ctx, false)
	cancel()
	close(release)
	if secondErr == nil {
		t.Error("concurrent updater replaced an active update")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("first updater failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first updater did not finish")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "new executable\n" {
		t.Fatalf("active update output corrupted: %q, %v", got, err)
	}
}

func TestUpdateDoesNotOverwriteChangedTarget(t *testing.T) {
	f := newUpdateTestFixture(t)
	path := updateTestExecutable(t)
	f.beforeArchive = func() {
		replacement := filepath.Join(filepath.Dir(path), "replacement")
		if err := os.WriteFile(replacement, []byte("another installed version\n"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(replacement, path); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := updateTestUpdater(f, path).Run(context.Background(), false); err == nil {
		t.Fatal("concurrently replaced target overwritten")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "another installed version\n" {
		t.Fatalf("concurrent replacement not preserved: %q, %v", got, err)
	}
}
