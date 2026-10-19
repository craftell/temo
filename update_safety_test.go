package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func safetyResponse(r *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode:    status,
		Header:        make(http.Header),
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       r,
	}
}

func TestUpdateSafetyRedirectPolicy(t *testing.T) {
	for _, tc := range []struct {
		name     string
		location string
		allowed  bool
	}{
		{"release CDN", "https://release-assets.githubusercontent.com/example?signature=test", true},
		{"object CDN", "https://objects.githubusercontent.com/example", true},
		{"HTTP downgrade", "http://release-assets.githubusercontent.com/example", false},
		{"foreign host", "https://example.com/example", false},
		{"host suffix", "https://github.com.example.com/example", false},
		{"credentials", "https://user:password@objects.githubusercontent.com/example", false},
		{"explicit port", "https://objects.githubusercontent.com:443/example", false},
		{"other repository", "https://github.com/other/temo/releases/download/v0.3.0/file", false},
		{"other API", "https://api.github.com/user", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			u := updater{GOOS: "linux", GOARCH: "amd64", Version: "0.2.0"}
			u.Client = &http.Client{Transport: updateTestTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					response := safetyResponse(r, http.StatusFound, "")
					response.Header.Set("Location", tc.location)
					return response, nil
				}
				if r.URL.String() != tc.location {
					t.Fatalf("unexpected redirect destination: %s", r.URL)
				}
				return safetyResponse(r, http.StatusOK, `{"tag_name":"v0.3.0"}`), nil
			})}
			result, err := u.Run(context.Background(), true)
			if tc.allowed {
				if err != nil || !result.Available || calls != 2 {
					t.Fatalf("safe redirect rejected: %+v, %v, calls=%d", result, err, calls)
				}
			} else if err == nil || calls != 1 {
				t.Fatalf("unsafe redirect followed: err=%v, calls=%d", err, calls)
			}
		})
	}
}

func TestUpdateSafetyRedirectLoopBounded(t *testing.T) {
	calls := 0
	u := updater{GOOS: "linux", GOARCH: "amd64", Version: "0.2.0"}
	u.Client = &http.Client{Transport: updateTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		response := safetyResponse(r, http.StatusFound, "")
		response.Header.Set("Location", updateAPI)
		return response, nil
	})}
	if _, err := u.Run(context.Background(), true); err == nil {
		t.Fatal("redirect loop accepted")
	}
	if calls != 5 {
		t.Fatalf("expected at most five transport calls, got %d", calls)
	}
}

func TestUpdateSafetyClientTimeoutBounded(t *testing.T) {
	for _, timeout := range []time.Duration{0, -time.Second, time.Hour, 5 * time.Second} {
		t.Run(timeout.String(), func(t *testing.T) {
			client := &http.Client{Timeout: timeout}
			client.Transport = updateTestTransport(func(r *http.Request) (*http.Response, error) {
				deadline, ok := r.Context().Deadline()
				if !ok {
					t.Fatal("update request has no deadline")
				}
				remaining := time.Until(deadline)
				maximum := time.Minute
				if timeout > 0 && timeout < maximum {
					maximum = timeout
				}
				if remaining <= 0 || remaining > maximum {
					t.Fatalf("unbounded deadline: %s", remaining)
				}
				return safetyResponse(r, http.StatusOK, `{"tag_name":"v0.3.0"}`), nil
			})
			u := updater{Client: client, GOOS: "linux", GOARCH: "amd64", Version: "0.2.0"}
			if _, err := u.Run(context.Background(), true); err != nil {
				t.Fatal(err)
			}
			if client.Timeout != timeout || client.CheckRedirect != nil {
				t.Fatal("updater mutated its caller's client")
			}
		})
	}
}

func TestUpdateSafetyInvalidMetadataAndSizeLimits(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   string
		length int64
	}{
		{"invalid JSON", "{broken", 7},
		{"declared oversized", "{}", (1 << 20) + 1},
		{"streamed oversized", strings.Repeat(" ", (1 << 20) + 1), -1},
		{"trailing JSON", `{"tag_name":"v0.3.0"}{}`, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := updateTestExecutable(t)
			u := updateTestUpdater(newUpdateTestFixture(t), path)
			u.Client = &http.Client{Transport: updateTestTransport(func(r *http.Request) (*http.Response, error) {
				response := safetyResponse(r, http.StatusOK, tc.body)
				response.ContentLength = tc.length
				return response, nil
			})}
			if _, err := u.Run(context.Background(), false); err == nil {
				t.Fatal("invalid metadata accepted")
			}
			assertUpdateTestOldBinary(t, path)
			assertSafetyNoStaging(t, filepath.Dir(path))
		})
	}
}

type safetyZeroReader struct{}

func (safetyZeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestUpdateSafetyExpandedArchiveLimit(t *testing.T) {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := io.CopyN(writer, safetyZeroReader{}, updateExpandedLimit+1); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := extractUpdateBinary(compressed.Bytes()); err == nil || !strings.Contains(err.Error(), "oversized") {
		t.Fatalf("expanded gzip limit was not enforced: %v", err)
	}
}

func assertSafetyNoStaging(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".temo-update-") {
			t.Errorf("staging debris left behind: %s", entry.Name())
		}
	}
}

func TestUpdateSafetyLockRejectsSymlinkAndDirectory(t *testing.T) {
	for _, kind := range []string{"symlink", "directory"} {
		t.Run(kind, func(t *testing.T) {
			path := updateTestExecutable(t)
			lockPath := path + ".update.lock"
			if kind == "symlink" {
				if err := os.Symlink(path, lockPath); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Mkdir(lockPath, 0700); err != nil {
				t.Fatal(err)
			}
			f := newUpdateTestFixture(t)
			if _, err := updateTestUpdater(f, path).Run(context.Background(), false); err == nil {
				t.Fatal("unsafe update lock accepted")
			}
			if len(f.requests) != 0 {
				t.Fatal("download started before lock validation")
			}
			assertUpdateTestOldBinary(t, path)
			assertSafetyNoStaging(t, filepath.Dir(path))
		})
	}
}

func TestUpdateSafetyLockKeepsStableInode(t *testing.T) {
	path := updateTestExecutable(t)
	lockPath := path + ".update.lock"
	f := newUpdateTestFixture(t)
	u := updateTestUpdater(f, path)
	u.Version = "0.3.0"
	if _, err := u.Run(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := u.Run(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	after, err := os.Lstat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("lock was removed or replaced between updates")
	}
	assertUpdateTestOldBinary(t, path)
}

func TestUpdateSafetyStagingFailurePreservesOriginal(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "install")
	moved := filepath.Join(root, "preserved")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "temo")
	if err := os.WriteFile(path, []byte("old executable\n"), 0755); err != nil {
		t.Fatal(err)
	}
	f := newUpdateTestFixture(t)
	f.beforeArchive = func() {
		if err := os.Rename(directory, moved); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := updateTestUpdater(f, path).Run(context.Background(), false); err == nil {
		t.Fatal("update succeeded despite missing staging directory")
	}
	assertUpdateTestOldBinary(t, filepath.Join(moved, "temo"))
	assertSafetyNoStaging(t, moved)
	assertSafetyNoStaging(t, root)
}

func TestUpdateSafetyCancelledInstallCleansStaging(t *testing.T) {
	path := updateTestExecutable(t)
	target, original, err := updateTarget(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := installUpdate(ctx, target, original, []byte("new executable\n")); err == nil {
		t.Fatal("cancelled installation succeeded")
	}
	assertUpdateTestOldBinary(t, path)
	assertSafetyNoStaging(t, filepath.Dir(path))
}
