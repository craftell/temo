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
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const updateAPI = "https://api.github.com/repos/craftell/temo/releases/latest"
const updateArchiveLimit = 64 << 20
const updateExpandedLimit = 80 << 20

var updateChannel = "unknown"

type updater struct {
	Client     *http.Client
	Executable func() (string, error)
	GOOS       string
	GOARCH     string
	Version    string
	Channel    string
}

type updateResult struct {
	Current        string
	Latest         string
	Updated        bool
	Available      bool
	CurrentUnknown bool
}

type updateRelease struct {
	Tag        string        `json:"tag_name"`
	Draft      bool          `json:"draft"`
	Prerelease bool          `json:"prerelease"`
	Assets     []updateAsset `json:"assets"`
}

type updateAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

func newUpdater() updater {
	return updater{
		Client: &http.Client{Timeout: time.Minute}, Executable: os.Executable,
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Version: buildVersion(), Channel: updateChannel,
	}
}

func parseUpdateVersion(value string) ([3]uint64, error) {
	var version [3]uint64
	value = strings.TrimPrefix(value, "v")
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return version, errors.New("expected a stable semantic version")
	}
	for i, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return version, errors.New("expected a stable semantic version")
		}
		for _, char := range part {
			if char < '0' || char > '9' {
				return version, errors.New("expected a stable semantic version")
			}
		}
		number, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return version, errors.New("version component is too large")
		}
		version[i] = number
	}
	return version, nil
}

func (u updater) Run(ctx context.Context, checkOnly bool) (updateResult, error) {
	result := updateResult{Current: u.Version}
	current, currentErr := parseUpdateVersion(u.Version)
	result.CurrentUnknown = currentErr != nil
	if (u.GOOS != "darwin" && u.GOOS != "linux") || (u.GOARCH != "amd64" && u.GOARCH != "arm64") {
		return result, errors.New("self-update supports macOS and Linux on amd64 and arm64")
	}
	var target string
	var original os.FileInfo
	if !checkOnly {
		if u.Channel != "github-release" || currentErr != nil {
			return result, errors.New("self-update requires an official GitHub release build; use your original install method or install the latest GitHub release")
		}
		if u.Executable == nil {
			return result, errors.New("cannot locate the executable")
		}
		var err error
		target, err = u.Executable()
		if err != nil {
			return result, fmt.Errorf("locate executable: %w", err)
		}
		target, original, err = updateTarget(target)
		if err != nil {
			return result, err
		}
		lock, err := acquireUpdateLock(target + ".update.lock")
		if err != nil {
			return result, err
		}
		defer lock.Close()
		defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		if err := unchangedUpdateTarget(target, original); err != nil {
			return result, err
		}
	}
	client := http.Client{Timeout: time.Minute}
	if u.Client != nil {
		client = *u.Client
		if client.Timeout <= 0 || client.Timeout > time.Minute {
			client.Timeout = time.Minute
		}
	}
	client.Jar = nil
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !allowedUpdateURL(req.URL) {
			return errors.New("refusing unsafe update redirect")
		}
		req.Header.Del("Authorization")
		req.Header.Del("Cookie")
		return nil
	}
	data, err := fetchUpdate(ctx, &client, updateAPI, 1<<20)
	if err != nil {
		return result, err
	}
	var release updateRelease
	if err := json.Unmarshal(data, &release); err != nil {
		return result, errors.New("invalid GitHub release metadata")
	}
	latest, err := parseUpdateVersion(release.Tag)
	if err != nil || release.Draft || release.Prerelease {
		return result, errors.New("GitHub did not return a stable release")
	}
	result.Latest = release.Tag
	if currentErr == nil {
		for i := range current {
			if latest[i] != current[i] {
				result.Available = latest[i] > current[i]
				break
			}
		}
	}
	if checkOnly || !result.Available {
		return result, nil
	}
	versionText := strings.TrimPrefix(release.Tag, "v")
	archiveName := fmt.Sprintf("temo_%s_%s_%s.tar.gz", versionText, u.GOOS, u.GOARCH)
	archive, err := release.asset(archiveName, updateArchiveLimit)
	if err != nil {
		return result, err
	}
	checksums, err := release.asset("checksums.txt", 1<<20)
	if err != nil {
		return result, err
	}
	checksumData, err := fetchUpdate(ctx, &client, checksums.URL, 1<<20)
	if err != nil {
		return result, err
	}
	expected, err := updateChecksum(checksumData, archiveName)
	if err != nil {
		return result, err
	}
	archiveData, err := fetchUpdate(ctx, &client, archive.URL, updateArchiveLimit)
	if err != nil {
		return result, err
	}
	if int64(len(archiveData)) != archive.Size || sha256.Sum256(archiveData) != expected {
		return result, errors.New("release archive size or SHA-256 checksum does not match; executable was not changed")
	}
	binary, err := extractUpdateBinary(archiveData)
	if err != nil {
		return result, err
	}
	if err := installUpdate(ctx, target, original, binary); err != nil {
		return result, err
	}
	result.Updated = true
	return result, nil
}

func (release updateRelease) asset(name string, limit int64) (updateAsset, error) {
	var found updateAsset
	count := 0
	for _, asset := range release.Assets {
		if asset.Name == name {
			count++
			found = asset
		}
	}
	expected := "https://github.com/craftell/temo/releases/download/" + url.PathEscape(release.Tag) + "/" + name
	if count != 1 || found.URL != expected || found.Size <= 0 || found.Size > limit {
		return updateAsset{}, fmt.Errorf("missing or invalid official release asset %s", name)
	}
	return found, nil
}

func allowedUpdateURL(u *url.URL) bool {
	if u == nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Fragment != "" {
		return false
	}
	switch u.Host {
	case "api.github.com":
		return u.Path == "/repos/craftell/temo/releases/latest" && u.RawQuery == ""
	case "github.com":
		return strings.HasPrefix(u.Path, "/craftell/temo/releases/download/") && u.RawQuery == ""
	case "release-assets.githubusercontent.com", "objects.githubusercontent.com":
		return true
	default:
		return false
	}
}

func fetchUpdate(ctx context.Context, client *http.Client, address string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil || !allowedUpdateURL(req.URL) {
		return nil, errors.New("refusing unsafe update URL")
	}
	req.Header.Set("User-Agent", "temo-updater")
	req.Header.Set("Accept", "application/octet-stream")
	if req.URL.Host == "api.github.com" {
		req.Header.Set("Accept", "application/vnd.github+json")
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, errors.New("update download failed; check network access and retry")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update download returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > limit {
		return nil, errors.New("update download exceeds size limit")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, errors.New("could not read update download")
	}
	if int64(len(body)) > limit {
		return nil, errors.New("update download exceeds size limit")
	}
	return body, nil
}

func updateChecksum(data []byte, name string) ([32]byte, error) {
	var expected [32]byte
	found := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return expected, errors.New("invalid release checksums")
		}
		if strings.TrimPrefix(fields[1], "*") != name {
			continue
		}
		decoded, err := hex.DecodeString(fields[0])
		if found || err != nil || len(decoded) != len(expected) {
			return expected, errors.New("invalid or duplicate release checksum")
		}
		copy(expected[:], decoded)
		found = true
	}
	if !found {
		return expected, errors.New("release checksum not found")
	}
	return expected, nil
}

func extractUpdateBinary(data []byte) ([]byte, error) {
	compressed, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("invalid release gzip archive")
	}
	defer compressed.Close()
	expanded, err := io.ReadAll(io.LimitReader(compressed, updateExpandedLimit+1))
	if err != nil || len(expanded) > updateExpandedLimit {
		return nil, errors.New("invalid or oversized release archive")
	}
	reader := bytes.NewReader(expanded)
	archive := tar.NewReader(reader)
	seen := make(map[string]bool)
	var binary []byte
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errors.New("invalid release tar archive")
		}
		if seen[header.Name] || (header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA) ||
			(header.Name != "temo" && header.Name != "LICENSE" && header.Name != "README.md") ||
			header.Size < 0 || header.Size > updateArchiveLimit {
			return nil, errors.New("release archive contains an unexpected or unsafe entry")
		}
		seen[header.Name] = true
		if header.Name == "temo" {
			if header.Size == 0 || header.Mode&0111 == 0 {
				return nil, errors.New("release executable is empty or not executable")
			}
			binary, err = io.ReadAll(io.LimitReader(archive, updateArchiveLimit+1))
			if err != nil || int64(len(binary)) != header.Size {
				return nil, errors.New("could not read release executable")
			}
		}
	}
	if len(binary) == 0 {
		return nil, errors.New("release archive has no temo executable")
	}
	for _, value := range expanded[len(expanded)-reader.Len():] {
		if value != 0 {
			return nil, errors.New("release archive contains trailing data")
		}
	}
	return binary, nil
}

func updateTarget(target string) (string, os.FileInfo, error) {
	absolute, err := filepath.Abs(target)
	if err != nil {
		return "", nil, err
	}
	entry, err := os.Lstat(absolute)
	if err != nil {
		return "", nil, err
	}
	if entry.Mode()&os.ModeSymlink != 0 {
		return "", nil, errors.New("refusing to replace a symlinked executable; use the original install method")
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", nil, fmt.Errorf("resolve executable: %w", err)
	}
	absolute = resolved
	for _, marker := range []string{"/Cellar/", "/Caskroom/", "/.linuxbrew/", "/nix/store/", "/snap/", "/go/bin/", "/.local/share/mise/", "/.asdf/"} {
		if strings.Contains(absolute, marker) {
			return "", nil, errors.New("package-managed executable; update using the original install method")
		}
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 || info.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 {
		return "", nil, errors.New("self-update requires a regular, non-privileged executable")
	}
	return absolute, info, nil
}

func acquireUpdateLock(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, errors.New("cannot create update lock; use your original install method or a user-writable install directory")
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, errors.New("invalid update lock")
	}
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, errors.New("another update is running; wait and retry")
	}
	return file, nil
}

func unchangedUpdateTarget(target string, original os.FileInfo) error {
	_, now, err := updateTarget(target)
	if err != nil {
		return err
	}
	if !os.SameFile(original, now) || original.Size() != now.Size() || !original.ModTime().Equal(now.ModTime()) || original.Mode() != now.Mode() {
		return errors.New("executable changed during update; restart temo and retry")
	}
	return nil
}

func installUpdate(ctx context.Context, target string, original os.FileInfo, binary []byte) error {
	staged, err := os.CreateTemp(filepath.Dir(target), ".temo-update-*")
	if err != nil {
		return errors.New("cannot stage update beside executable; use your original install method or a user-writable install directory")
	}
	defer os.Remove(staged.Name())
	defer staged.Close()
	if _, err := staged.Write(binary); err != nil {
		return fmt.Errorf("stage update: %w", err)
	}
	if err := staged.Chmod(original.Mode().Perm()); err != nil {
		return fmt.Errorf("set update permissions: %w", err)
	}
	if err := staged.Sync(); err != nil {
		return fmt.Errorf("sync update: %w", err)
	}
	if err := staged.Close(); err != nil {
		return fmt.Errorf("close staged update: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := unchangedUpdateTarget(target, original); err != nil {
		return err
	}
	if err := os.Rename(staged.Name(), target); err != nil {
		return fmt.Errorf("replace executable: %w", err)
	}
	return nil
}
