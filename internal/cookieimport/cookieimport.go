// Package cookieimport reads the cookies of the user's browsers on this
// computer, for the agent's browser: Chromium's family (Chrome, Edge, Brave,
// Vivaldi, Opera, Chromium) and Firefox's (Firefox, LibreWolf, Waterfox,
// Zen, Floorp).
//
// On Linux, Chromium reads a copy of its own cookies using the system
// keyring. Windows Chromium profiles need a user-exported JSON file;
// Firefox keeps its cookies in SQLite on both systems.
package cookieimport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/storage"
	"github.com/chromedp/chromedp"
	_ "modernc.org/sqlite"
)

// Profile is a browser profile cookies can come from.
type Profile struct {
	Browser string `json:"browser"` // "Google Chrome"
	Name    string `json:"name"`    // the profile's own name
	Dir     string `json:"dir"`     // its folder
	Family  string `json:"family"`  // "chromium" or "firefox"
	root    string // chromium: the user data folder holding Local State
	exec    []string
}

// Label is how the settings show it.
func (profile Profile) Label() string {
	return profile.Browser + " · " + profile.Name
}

// Result is what an import brought.
type Result struct {
	Cookies []*network.CookieParam
	Sites   int // the sites they log into
	Skipped int // expired, session-only or private (container, partitioned) cookies
}

type chromium struct {
	name  string
	dirs  []string // user data folders, by system
	execs []string // its program: names on the PATH, or paths
}

func home(parts ...string) string {
	dir, _ := os.UserHomeDir()
	return filepath.Join(append([]string{dir}, parts...)...)
}

func env(name string, parts ...string) string {
	if value := os.Getenv(name); value != "" {
		return filepath.Join(append([]string{value}, parts...)...)
	}
	return ""
}

func chromiumBrowsers() []chromium {
	if runtime.GOOS == "windows" {
		return nil // protected profiles use an explicit JSON export
	}

	config := func(parts ...string) string {
		if dir, err := os.UserConfigDir(); err == nil {
			return filepath.Join(append([]string{dir}, parts...)...)
		}
		return ""
	}
	return []chromium{
		{"Google Chrome", []string{config("google-chrome"), home(".var", "app", "com.google.Chrome", "config", "google-chrome")}, []string{"google-chrome-stable", "google-chrome"}},
		{"Google Chrome Beta", []string{config("google-chrome-beta")}, []string{"google-chrome-beta"}},
		{"Chromium", []string{config("chromium"), home("snap", "chromium", "common", "chromium"), home(".var", "app", "org.chromium.Chromium", "config", "chromium")}, []string{"chromium", "chromium-browser"}},
		{"Microsoft Edge", []string{config("microsoft-edge"), home(".var", "app", "com.microsoft.Edge", "config", "microsoft-edge")}, []string{"microsoft-edge-stable", "microsoft-edge"}},
		{"Brave", []string{config("BraveSoftware", "Brave-Browser"), home(".var", "app", "com.brave.Browser", "config", "BraveSoftware", "Brave-Browser")}, []string{"brave-browser", "brave"}},
		{"Vivaldi", []string{config("vivaldi")}, []string{"vivaldi-stable", "vivaldi"}},
		{"Opera", []string{config("opera")}, []string{"opera"}},
	}
}

// firefoxRoots are the folders holding a profiles.ini.
func firefoxRoots() map[string][]string {
	if runtime.GOOS == "windows" {
		return map[string][]string{
			"Firefox":   {env("APPDATA", "Mozilla", "Firefox")},
			"LibreWolf": {env("APPDATA", "librewolf")},
			"Waterfox":  {env("APPDATA", "Waterfox")},
			"Zen":       {env("APPDATA", "zen")},
			"Floorp":    {env("APPDATA", "Floorp")},
		}
	}
	config := ""
	if dir, err := os.UserConfigDir(); err == nil {
		config = filepath.Join(dir, "mozilla", "firefox") // recent versions follow XDG
	}
	return map[string][]string{
		"Firefox":   {config, home(".mozilla", "firefox"), home("snap", "firefox", "common", ".mozilla", "firefox"), home(".var", "app", "org.mozilla.firefox", ".mozilla", "firefox")},
		"LibreWolf": {home(".librewolf"), home(".var", "app", "io.gitlab.librewolf-community", ".librewolf")},
		"Waterfox":  {home(".waterfox")},
		"Zen":       {home(".zen"), home(".var", "app", "app.zen_browser.zen", ".zen")},
		"Floorp":    {home(".floorp")},
	}
}

// Find lists the profiles of the browsers on this computer that hold
// cookies, and whose Chromium browser can be started.
func Find() []Profile {
	var profiles []Profile
	for _, browser := range chromiumBrowsers() {
		launcher := findExecutable(browser.execs)
		for _, root := range browser.dirs {
			if root == "" {
				continue
			}
			for _, profile := range chromiumProfiles(root) {
				if launcher == nil {
					continue // its cookies need the browser to be read
				}
				profile.Browser, profile.exec = browser.name, launcher
				profiles = append(profiles, profile)
			}
		}
	}
	roots := firefoxRoots()
	names := make([]string, 0, len(roots))
	for name := range roots {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, root := range roots[name] {
			for _, profile := range firefoxProfiles(root) {
				profile.Browser = name
				profiles = append(profiles, profile)
			}
		}
	}
	return profiles
}

func chromiumProfiles(root string) []Profile {
	data, err := os.ReadFile(filepath.Join(root, "Local State"))
	if err != nil {
		return nil
	}
	var state struct {
		Profile struct {
			InfoCache map[string]struct {
				Name string `json:"name"`
			} `json:"info_cache"`
		} `json:"profile"`
	}
	json.Unmarshal(data, &state)
	dirs := map[string]string{}
	for dir, info := range state.Profile.InfoCache {
		dirs[dir] = info.Name
	}
	if len(dirs) == 0 {
		dirs["Default"] = "Default"
		if _, err := os.Stat(filepath.Join(root, "Default")); err != nil {
			dirs = map[string]string{".": "Default"} // Opera keeps its one profile in the root
		}
	}
	var profiles []Profile
	for dir, name := range dirs {
		path := filepath.Join(root, dir)
		if chromiumCookies(path) == "" {
			continue
		}
		if name == "" {
			name = dir
		}
		profiles = append(profiles, Profile{Name: name, Dir: path, Family: "chromium", root: root})
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].Dir < profiles[j].Dir })
	return profiles
}

// chromiumCookies is a profile's cookie database: Network/Cookies since
// Chrome 96, Cookies before.
func chromiumCookies(profile string) string {
	for _, path := range []string{filepath.Join(profile, "Network", "Cookies"), filepath.Join(profile, "Cookies")} {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}

func firefoxProfiles(root string) []Profile {
	if root == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(root, "profiles.ini"))
	if err != nil {
		return nil
	}
	var profiles []Profile
	name, path, relative := "", "", true
	flush := func() {
		if path == "" {
			return
		}
		dir := path
		if relative {
			dir = filepath.Join(root, filepath.FromSlash(path))
		}
		if _, err := os.Stat(filepath.Join(dir, "cookies.sqlite")); err == nil {
			if name == "" {
				name = filepath.Base(dir)
			}
			profiles = append(profiles, Profile{Name: name, Dir: dir, Family: "firefox"})
		}
		name, path, relative = "", "", true
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r", ""), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			flush()
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		switch key {
		case "Name":
			name = value
		case "Path":
			path = value
		case "IsRelative":
			relative = value != "0"
		}
	}
	flush()
	return profiles
}

// findExecutable is the command that starts a browser: a path, a name on
// the PATH, or in the Flatpak the host's program through host-run.
func findExecutable(candidates []string) []string {
	flatpak := os.Getenv("FLATPAK_ID") != ""
	for _, candidate := range candidates {
		if filepath.IsAbs(candidate) {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return []string{candidate}
			}
			continue
		}
		if flatpak {
			if exec.Command("/app/lib/1loop/host/host-run", "sh", "-c", `command -v "$1" >/dev/null`, "sh", candidate).Run() == nil {
				return []string{"/app/lib/1loop/host/host-run", candidate}
			}
			continue
		}
		if path, err := exec.LookPath(candidate); err == nil {
			return []string{path}
		}
	}
	return nil
}

// ErrBrowserOpen: the browser keeps its cookies locked while it runs
// (Windows).
var ErrBrowserOpen = errors.New("browser open")

// Read gets a profile's cookies, the browser left untouched: it works on a
// copy, in a folder of 1loop's cache.
func Read(ctx context.Context, profile Profile) (Result, error) {
	if runtime.GOOS == "windows" && profile.Family != "firefox" {
		return Result{}, fmt.Errorf("Direct import of this browser's protected cookies is unavailable on Windows. Export them as JSON and choose that file instead.")
	}
	cache, err := os.UserCacheDir() // the Flatpak's is also the host's: the host browser reads the copy
	if err != nil {
		cache = os.TempDir()
	}
	if err := os.MkdirAll(filepath.Join(cache, "1loop"), 0o700); err != nil {
		return Result{}, err
	}
	work, err := os.MkdirTemp(filepath.Join(cache, "1loop"), "cookie-import-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(work) // its own copy only
	var cookies []*network.CookieParam
	var skipped int
	if profile.Family == "firefox" {
		cookies, skipped, err = readFirefox(ctx, profile, work)
	} else {
		cookies, skipped, err = readChromium(ctx, profile, work)
	}
	if err != nil {
		return Result{}, err
	}
	return result(cookies, skipped), nil
}

func result(cookies []*network.CookieParam, skipped int) Result {
	sites := map[string]bool{}
	for _, cookie := range cookies {
		sites[site(cookie)] = true
	}
	return Result{Cookies: cookies, Sites: len(sites), Skipped: skipped}
}

func site(cookie *network.CookieParam) string {
	host := strings.TrimPrefix(cookie.Domain, ".")
	if host == "" {
		host = strings.TrimPrefix(strings.TrimPrefix(cookie.URL, "https://"), "http://")
		host, _, _ = strings.Cut(host, "/")
	}
	return host
}

func copyFile(from, to string) error {
	source, err := os.Open(from)
	if err != nil {
		return err
	}
	defer source.Close()
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return err
	}
	target, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(target, source); err != nil {
		target.Close()
		return err
	}
	return target.Close()
}

// locked tells a file the browser holds open without sharing (Windows).
func locked(err error) bool {
	if err == nil || runtime.GOOS != "windows" {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "used by another process") || strings.Contains(text, "utilisé par un autre processus") || strings.Contains(text, "sharing violation")
}

func readChromium(ctx context.Context, profile Profile, work string) ([]*network.CookieParam, int, error) {
	database := chromiumCookies(profile.Dir)
	relative, _ := filepath.Rel(profile.Dir, database) // where this browser looks for it, on this system
	copy := filepath.Join(work, "Default", relative)
	if err := copyFile(database, copy); err != nil {
		if locked(err) {
			return nil, 0, fmt.Errorf("%w: close %s, then import again (it keeps its cookies locked while it runs)", ErrBrowserOpen, profile.Browser)
		}
		return nil, 0, err
	}
	for _, extra := range []string{"-journal", "-wal"} {
		copyFile(database+extra, copy+extra) // a write in progress, when there is one
	}
	if err := copyFile(filepath.Join(profile.root, "Local State"), filepath.Join(work, "Local State")); err != nil {
		return nil, 0, err
	}
	// A browser may silently reject a profile copy whose encryption key
	// cannot be used here. Do not report that as a successful empty import.
	db, err := sql.Open("sqlite", copy)
	if err != nil {
		return nil, 0, err
	}
	var stored int
	err = db.QueryRowContext(ctx, "SELECT count(*) FROM cookies").Scan(&stored)
	db.Close()
	if err != nil {
		return nil, 0, fmt.Errorf("The cookie database could not be read. Close the source browser and try again: %w", err)
	}
	options := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(profile.exec[0]),
		chromedp.UserDataDir(work),
		chromedp.Flag("profile-directory", "Default"),
		chromedp.Flag("headless", "new"),
		chromedp.Flag("disable-dev-shm-usage", true),
		// Its own key store: the system keyring (Linux), not chromedp's
		// default stand-ins, or the cookies could not be decrypted.
		chromedp.Flag("password-store", false),
		chromedp.Flag("use-mock-keychain", false),
	)
	if len(profile.exec) > 1 { // host-run NAME: the browser's name comes first
		name := profile.exec[1]
		options = append(options, chromedp.ModifyCmdFunc(func(cmd *exec.Cmd) {
			cmd.Args = append([]string{cmd.Args[0], name}, cmd.Args[1:]...)
		}))
	}
	run, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	alloc, stopAlloc := chromedp.NewExecAllocator(run, options...)
	defer stopAlloc()
	browser, stopBrowser := chromedp.NewContext(alloc)
	defer stopBrowser()
	var found []*network.Cookie
	err = chromedp.Run(browser, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		found, err = storage.GetCookies().Do(ctx)
		return err
	}))
	chromedp.Cancel(browser)
	if err != nil {
		return nil, 0, fmt.Errorf("%s could not be started to read its cookies: %w", profile.Browser, err)
	}
	if stored > 0 && len(found) == 0 {
		return nil, 0, fmt.Errorf("%s could not read the copied cookies. Export cookies as JSON and choose that file instead", profile.Browser)
	}
	var cookies []*network.CookieParam
	skipped := 0
	for _, cookie := range found {
		if cookie.Session || cookie.PartitionKey != nil {
			skipped++
			continue
		}
		param := &network.CookieParam{Name: cookie.Name, Value: cookie.Value, Path: cookie.Path, Secure: cookie.Secure, HTTPOnly: cookie.HTTPOnly, SameSite: cookie.SameSite, Priority: cookie.Priority}
		place(param, cookie.Domain, cookie.Secure)
		if cookie.Expires > 0 {
			expires := cdp.TimeSinceEpoch(time.Unix(int64(cookie.Expires), 0))
			param.Expires = &expires
		}
		cookies = append(cookies, param)
	}
	return cookies, skipped, nil
}

// place sets where a cookie goes: a domain cookie keeps its domain, a
// host-only one goes by its host's address.
func place(param *network.CookieParam, host string, secure bool) {
	if strings.HasPrefix(host, ".") {
		param.Domain = host
		return
	}
	scheme := "http://"
	if secure {
		scheme = "https://"
	}
	path := param.Path
	if path == "" {
		path = "/"
	}
	param.URL = scheme + host + path
}

func readFirefox(ctx context.Context, profile Profile, work string) ([]*network.CookieParam, int, error) {
	copy := filepath.Join(work, "cookies.sqlite")
	if err := copyFile(filepath.Join(profile.Dir, "cookies.sqlite"), copy); err != nil {
		if locked(err) {
			return nil, 0, fmt.Errorf("%w: close %s, then import again", ErrBrowserOpen, profile.Browser)
		}
		return nil, 0, err
	}
	copyFile(filepath.Join(profile.Dir, "cookies.sqlite-wal"), copy+"-wal") // the latest changes, while Firefox runs
	db, err := sql.Open("sqlite", copy)
	if err != nil {
		return nil, 0, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT name, value, host, path, expiry, isSecure, isHttpOnly, sameSite, originAttributes FROM moz_cookies`)
	if err != nil {
		return nil, 0, fmt.Errorf("%s's cookies could not be read: %w", profile.Browser, err)
	}
	defer rows.Close()
	now := time.Now()
	var cookies []*network.CookieParam
	skipped := 0
	for rows.Next() {
		var name, value, host, path, attributes string
		var expiry, secure, httpOnly, sameSite int64
		if err := rows.Scan(&name, &value, &host, &path, &expiry, &secure, &httpOnly, &sameSite, &attributes); err != nil {
			return nil, 0, err
		}
		if expiry > 100_000_000_000 {
			expiry /= 1000 // milliseconds in recent versions
		}
		at := time.Unix(expiry, 0)
		if attributes != "" || expiry <= 0 || at.Before(now) {
			skipped++ // a container's, private browsing's or partitioned; expired
			continue
		}
		param := &network.CookieParam{Name: name, Value: value, Path: path, Secure: secure != 0, HTTPOnly: httpOnly != 0}
		switch sameSite {
		case 1:
			param.SameSite = network.CookieSameSiteLax
		case 2:
			param.SameSite = network.CookieSameSiteStrict
		case 0:
			if param.Secure {
				param.SameSite = network.CookieSameSiteNone // Chrome refuses None without Secure
			}
		}
		place(param, host, param.Secure)
		expires := cdp.TimeSinceEpoch(at)
		param.Expires = &expires
		cookies = append(cookies, param)
	}
	return cookies, skipped, rows.Err()
}
