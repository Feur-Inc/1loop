package browser

// 1loop's own Chromium, for a computer without Chrome, Chromium or Edge:
// Chrome for Testing, Google's build of Chromium made to be driven, is
// downloaded the first time Browser Use starts, into 1loop's config folder
// (no administrator rights), then kept at Google's current stable version.

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"1loop/internal/platform"
)

const chromiumIndex = "https://googlechromelabs.github.io/chrome-for-testing/last-known-good-versions-with-downloads.json"

// ChromiumDir holds 1loop's Chromium, one folder per version, beside the
// rest of 1loop's settings (~/.config/1loop, %AppData%\1loop), or the
// server's data folder.
func ChromiumDir() string {
	if BrowserProfileOverride != "" {
		return filepath.Join(filepath.Dir(BrowserProfileOverride), "chromium")
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "1loop", "chromium")
}

// chromiumPlatform is Chrome for Testing's name for this system, or "" where
// it has no build.
func chromiumPlatform() string {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "linux/amd64":
		return "linux64"
	case "linux/arm64":
		return "linux-arm64"
	case "windows/amd64":
		return "win64"
	case "windows/386":
		return "win32"
	}
	return ""
}

// chromiumExecutable is the browser inside a version's folder.
func chromiumExecutable(dir, version string) string {
	name := "chrome"
	if runtime.GOOS == "windows" {
		name = "chrome.exe"
	}
	return filepath.Join(dir, version, "chrome-"+chromiumPlatform(), name)
}

var chromiumVersion = regexp.MustCompile(`^\d+(\.\d+){3}$`)

// installedChromium lists the complete versions in dir, newest first: a
// version's folder appears only once it is fully unpacked.
func installedChromium(dir string) []string {
	entries, _ := os.ReadDir(dir)
	var versions []string
	for _, entry := range entries {
		if !entry.IsDir() || !chromiumVersion.MatchString(entry.Name()) {
			continue
		}
		if info, err := os.Stat(chromiumExecutable(dir, entry.Name())); err == nil && !info.IsDir() {
			versions = append(versions, entry.Name())
		}
	}
	slices.SortFunc(versions, func(a, b string) int { return compareVersions(b, a) })
	return versions
}

func compareVersions(a, b string) int {
	left, right := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(left) && i < len(right); i++ {
		x, _ := strconv.Atoi(left[i])
		y, _ := strconv.Atoi(right[i])
		if x != y {
			return x - y
		}
	}
	return len(left) - len(right)
}

var chromiumMu sync.Mutex // one download at a time in this process

// ownChromium is 1loop's Chromium. An installed one is used at once and
// updated in the background for the next start; otherwise it is downloaded
// now, the pages saying how far it is.
func ownChromium(ctx context.Context) (string, error) {
	dir := ChromiumDir()
	if chromiumPlatform() == "" || dir == "" {
		return "", fmt.Errorf("Install Chromium or Google Chrome to use browser tabs")
	}
	if _, err := os.Stat("/etc/NIXOS"); err == nil {
		// Its binaries expect a standard Linux layout NixOS does not have.
		return "", fmt.Errorf("Add chromium to your NixOS packages to use browser tabs (the 1loop Nix package includes it)")
	}
	if versions := installedChromium(dir); len(versions) > 0 {
		go updateChromium(dir, versions[0])
		return chromiumExecutable(dir, versions[0]), nil
	}
	chromiumMu.Lock()
	defer chromiumMu.Unlock()
	if versions := installedChromium(dir); len(versions) > 0 { // another tab's download
		return chromiumExecutable(dir, versions[0]), nil
	}
	defer reportChromium("")
	reportChromium("Finding the latest Chromium…")
	version, address, err := latestChromium(ctx)
	if err != nil {
		return "", fmt.Errorf("Chromium for Browser Use could not be downloaded: %w", err)
	}
	if err := downloadChromium(ctx, dir, version, address, true); err != nil {
		return "", fmt.Errorf("Chromium for Browser Use could not be downloaded: %w", err)
	}
	return chromiumExecutable(dir, version), nil
}

// updateChromium downloads a newer stable version, at most once a day; the
// browser starts with it next time. The two newest versions stay, so a
// browser still running the previous one keeps its files.
func updateChromium(dir, installed string) {
	stamp := filepath.Join(dir, "checked")
	if info, err := os.Stat(stamp); err == nil && time.Since(info.ModTime()) < 24*time.Hour {
		return
	}
	if !chromiumMu.TryLock() {
		return
	}
	defer chromiumMu.Unlock()
	os.WriteFile(stamp, nil, 0600)
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	version, address, err := latestChromium(ctx)
	if err == nil && compareVersions(version, installed) > 0 {
		err = downloadChromium(ctx, dir, version, address, false)
	}
	if err != nil {
		platform.Devf("browser", "chromium update: %v", err)
		return
	}
	versions := installedChromium(dir)
	for _, old := range versions[min(2, len(versions)):] {
		os.RemoveAll(filepath.Join(dir, old))
	}
}

// latestChromium asks Google which Chrome for Testing version is stable now.
func latestChromium(ctx context.Context) (string, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, chromiumIndex, nil)
	if err != nil {
		return "", "", err
	}
	response, err := (&http.Client{Timeout: time.Minute}).Do(request)
	if err != nil {
		return "", "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("%s answered %s", request.URL.Host, response.Status)
	}
	var index struct {
		Channels map[string]struct {
			Version   string `json:"version"`
			Downloads map[string][]struct {
				Platform string `json:"platform"`
				URL      string `json:"url"`
			} `json:"downloads"`
		} `json:"channels"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&index); err != nil {
		return "", "", err
	}
	stable := index.Channels["Stable"]
	for _, download := range stable.Downloads["chrome"] {
		if download.Platform == chromiumPlatform() && chromiumVersion.MatchString(stable.Version) &&
			strings.HasPrefix(download.URL, "https://storage.googleapis.com/chrome-for-testing-public/") {
			return stable.Version, download.URL, nil
		}
	}
	return "", "", fmt.Errorf("no stable Chromium is listed for %s", chromiumPlatform())
}

// downloadChromium fetches a version's archive into a temporary folder of
// dir, checks it against the MD5 Google's storage gives for the file, then
// unpacks it and moves it into place in one rename.
func downloadChromium(ctx context.Context, dir, version, address string, shown bool) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	work, err := os.MkdirTemp(dir, ".download-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	response, err := (&http.Client{Timeout: 30 * time.Minute}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s", request.URL.Host, response.Status)
	}
	platform.Devf("browser", "downloading chromium %s from %s", version, address)
	var expected []byte
	for _, value := range response.Header.Values("X-Goog-Hash") {
		if encoded, ok := strings.CutPrefix(value, "md5="); ok {
			expected, _ = base64.StdEncoding.DecodeString(encoded)
		}
	}
	if len(expected) != md5.Size {
		return fmt.Errorf("the download came without its checksum")
	}
	archive, err := os.Create(filepath.Join(work, "chromium.zip"))
	if err != nil {
		return err
	}
	defer archive.Close()
	sum := md5.New()
	total, done, said := response.ContentLength, int64(0), -1
	buffer := make([]byte, 256<<10)
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			if _, err := archive.Write(buffer[:n]); err != nil {
				return err
			}
			sum.Write(buffer[:n])
			done += int64(n)
			if percent := int(done * 100 / max(total, 1)); shown && total > 0 && percent != said {
				said = percent
				reportChromium(fmt.Sprintf("Downloading Chromium for Browser Use… %d%% of %d MB", percent, total>>20))
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if !bytes.Equal(sum.Sum(nil), expected) {
		return fmt.Errorf("the download is damaged (checksum mismatch)")
	}
	if shown {
		reportChromium("Unpacking Chromium…")
	}
	unpacked := filepath.Join(work, version)
	if err := unzip(archive.Name(), unpacked); err != nil {
		return err
	}
	if err := os.Rename(unpacked, filepath.Join(dir, version)); err != nil {
		if _, installed := os.Stat(chromiumExecutable(dir, version)); installed != nil { // not another process's copy
			return err
		}
	}
	platform.Devf("browser", "chromium %s installed in %s", version, dir)
	return nil
}

// unzip extracts an archive into dest, keeping Unix permissions; an entry
// that would land outside dest is refused.
func unzip(path, dest string) error {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer reader.Close()
	for _, file := range reader.File {
		target := filepath.Join(dest, filepath.FromSlash(file.Name))
		if !strings.HasPrefix(target, dest+string(filepath.Separator)) {
			return fmt.Errorf("the archive holds an unsafe path: %s", file.Name)
		}
		mode := file.Mode()
		switch {
		case mode.IsDir():
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
			continue
		case mode&os.ModeSymlink != 0:
			link, err := readEntry(file)
			if err != nil {
				return err
			}
			resolved := filepath.Join(filepath.Dir(target), string(link))
			if filepath.IsAbs(string(link)) || !strings.HasPrefix(resolved, dest+string(filepath.Separator)) {
				return fmt.Errorf("the archive holds an unsafe link: %s", file.Name)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			if err := os.Symlink(string(link), target); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		if err := writeEntry(file, target, mode.Perm()|0600); err != nil {
			return err
		}
	}
	return nil
}

func readEntry(file *zip.File) ([]byte, error) {
	in, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer in.Close()
	return io.ReadAll(io.LimitReader(in, 4096))
}

func writeEntry(file *zip.File, target string, mode os.FileMode) error {
	in, err := file.Open()
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

var missingLibrary = regexp.MustCompile(`error while loading shared libraries: ([^:\s]+)`)

// chromiumStartError names the system library 1loop's Chromium lacks, the
// usual reason it does not start on a minimal Linux.
func chromiumStartError(err error) error {
	if match := missingLibrary.FindStringSubmatch(err.Error()); match != nil {
		return fmt.Errorf("1loop's Chromium needs %s, which this system lacks: install the package of your distribution that provides it", match[1])
	}
	return err
}

// The pages waiting for the first download show its progress.
var chromiumWatchers struct {
	sync.Mutex
	next  int
	watch map[int]func(string)
	text  string
}

func watchChromium(show func(string)) func() {
	chromiumWatchers.Lock()
	if chromiumWatchers.watch == nil {
		chromiumWatchers.watch = map[int]func(string){}
	}
	id := chromiumWatchers.next
	chromiumWatchers.next++
	chromiumWatchers.watch[id] = show
	text := chromiumWatchers.text
	chromiumWatchers.Unlock()
	if text != "" {
		show(text)
	}
	return func() {
		chromiumWatchers.Lock()
		delete(chromiumWatchers.watch, id)
		chromiumWatchers.Unlock()
	}
}

func reportChromium(text string) {
	chromiumWatchers.Lock()
	chromiumWatchers.text = text
	shows := make([]func(string), 0, len(chromiumWatchers.watch))
	for _, show := range chromiumWatchers.watch {
		shows = append(shows, show)
	}
	chromiumWatchers.Unlock()
	if text != "" && !strings.HasPrefix(text, "Downloading") {
		platform.Devf("browser", "%s", text)
	}
	for _, show := range shows {
		show(text)
	}
}
