package agent

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// claudeAccountsHome gives the test a home and a configuration folder of its
// own, and a fake claude that records its folder and arguments.
func claudeAccountsHome(t *testing.T) (home, record string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake claude is a shell script")
	}
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("CLAUDE_CONFIG_DIR", "") // restored after the test
	os.Unsetenv("CLAUDE_CONFIG_DIR")
	bin := filepath.Join(home, "bin")
	record = filepath.Join(home, "record")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho \"${CLAUDE_CONFIG_DIR:-system} $*\" >> " + record + "\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return home, record
}

func TestClaudeHomePerConnection(t *testing.T) {
	claudeAccountsHome(t)
	system := Connection{ID: "a1", Provider: "claudecode"}
	own := Connection{ID: "b2", Provider: "claudecode", ClaudeLogin: ClaudeOwnLogin}
	if system.ClaudeHome() != "" {
		t.Fatalf("system login has a folder: %q", system.ClaudeHome())
	}
	if want := filepath.Join(ClaudeAccountsDir(), "b2"); own.ClaudeHome() != want {
		t.Fatalf("own login folder = %q, want %q", own.ClaudeHome(), want)
	}
	if (Connection{ID: "../x", Provider: "claudecode", ClaudeLogin: ClaudeOwnLogin}).ClaudeHome() != "" {
		t.Fatal("an ID that is not a plain name became a folder")
	}
	if (Connection{ID: "c3", Provider: "openai", ClaudeLogin: ClaudeOwnLogin}).ClaudeHome() != "" {
		t.Fatal("a connection that is not Claude Code has a Claude folder")
	}
	if own.Config("/w").ClaudeHome != own.ClaudeHome() {
		t.Fatal("the run configuration does not carry the connection's folder")
	}
}

func TestNewClaudeLogin(t *testing.T) {
	if got := NewClaudeLogin(nil, "n"); got != "" {
		t.Fatalf("first connection: %q, want the system login", got)
	}
	existing := []Connection{{ID: "a", Provider: "claudecode"}}
	if got := NewClaudeLogin(existing, "n"); got != ClaudeOwnLogin {
		t.Fatalf("second connection: %q, want its own login", got)
	}
	if got := NewClaudeLogin(existing, "a"); got != "" {
		t.Fatalf("the system connection itself: %q", got)
	}
	if got := NewClaudeLogin([]Connection{{ID: "a", Provider: "claudecode", ClaudeLogin: ClaudeOwnLogin}}, "n"); got != "" {
		t.Fatalf("with only own logins: %q, want the system login", got)
	}
}

// A session is resumed only in the folder that created it: switching a chat
// to another account's connection starts a session there.
func TestClaudeSessionBelongsToItsFolder(t *testing.T) {
	ownA, ownB := "/cfg/1loop/claude-code/aa", "/cfg/1loop/claude-code/bb"
	cases := []struct {
		session, home, want string
	}{
		{"s1", "", "s1"},
		{claudeSessionRef("", "s1"), "", "s1"},
		{claudeSessionRef(ownA, "s1"), ownA, "s1"},
		{claudeSessionRef(ownA, "s1"), ownB, ""},
		{claudeSessionRef(ownA, "s1"), "", ""},
		{"s1", ownA, ""},
		{"", ownA, ""},
	}
	for _, c := range cases {
		if got := (Config{Session: c.session, ClaudeHome: c.home}).ClaudeSession(); got != c.want {
			t.Errorf("session %q in %q: %q, want %q", c.session, c.home, got, c.want)
		}
	}
	next := Config{Provider: "claudecode", Session: claudeSessionRef(ownA, "s1"), SessionMark: 3, ClaudeHome: ownB}
	if handoffResumes(next) {
		t.Fatal("a handoff resumes another account's session")
	}
	next.ClaudeHome = ownA
	if !handoffResumes(next) {
		t.Fatal("a handoff does not resume the account's own session")
	}
}

func TestClaudeCommandUsesConnectionFolder(t *testing.T) {
	home, _ := claudeAccountsHome(t)
	t.Setenv("CLAUDE_CONFIG_DIR", "/elsewhere")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "token")
	own := filepath.Join(ClaudeAccountsDir(), "b2")
	cmd, err := claudeCommand(context.Background(), own, home)
	if err != nil {
		t.Fatal(err)
	}
	env := strings.Join(cmd.Env, "\n")
	if !strings.Contains(env, "CLAUDE_CONFIG_DIR="+own) || strings.Contains(env, "CLAUDE_CONFIG_DIR=/elsewhere") || strings.Contains(env, "CLAUDE_CODE_OAUTH_TOKEN") {
		t.Fatalf("own login environment:\n%s", env)
	}
	if info, err := os.Stat(own); err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("folder not created privately: %v %v", info, err)
	}
	cmd, err = claudeCommand(context.Background(), "", home)
	if err != nil {
		t.Fatal(err)
	}
	env = strings.Join(cmd.Env, "\n")
	if !strings.Contains(env, "CLAUDE_CONFIG_DIR=/elsewhere") || !strings.Contains(env, "CLAUDE_CODE_OAUTH_TOKEN=token") {
		t.Fatalf("the system login lost the user's own setup:\n%s", env)
	}
}

func TestClaudeHomeLinksSharedSettings(t *testing.T) {
	home, _ := claudeAccountsHome(t)
	shared := filepath.Join(home, ".claude")
	os.MkdirAll(filepath.Join(shared, "agents"), 0o700)
	os.WriteFile(filepath.Join(shared, "settings.json"), []byte(`{"model":"opus"}`), 0o600)
	os.WriteFile(filepath.Join(shared, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"system"}}`), 0o600)
	own := filepath.Join(ClaudeAccountsDir(), "b2")
	if err := prepareClaudeHome(own); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(own, "settings.json")); err != nil || string(data) != `{"model":"opus"}` {
		t.Fatalf("settings not shared: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(own, "agents")); err != nil {
		t.Fatalf("agents not shared: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(own, ".credentials.json")); !os.IsNotExist(err) {
		t.Fatal("the system login's credentials were shared")
	}
	if ClaudeSignedIn(Connection{ID: "b2", Provider: "claudecode", ClaudeLogin: ClaudeOwnLogin}) {
		t.Fatal("a new own login reads as signed in")
	}
	if !ClaudeSignedIn(Connection{ID: "a1", Provider: "claudecode"}) {
		t.Fatal("the system login reads as signed out")
	}
}

func TestClaudeAccountOf(t *testing.T) {
	home, _ := claudeAccountsHome(t)
	os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"oauthAccount":{"emailAddress":"first@example.com","organizationName":"One"}}`), 0o600)
	own := Connection{ID: "b2", Provider: "claudecode", ClaudeLogin: ClaudeOwnLogin}
	os.MkdirAll(own.ClaudeHome(), 0o700)
	os.WriteFile(filepath.Join(own.ClaudeHome(), ".claude.json"), []byte(`{"oauthAccount":{"emailAddress":"second@example.com"}}`), 0o600)
	if got := ClaudeAccountOf(Connection{ID: "a1", Provider: "claudecode"}); got.Email != "first@example.com" || got.Organization != "One" {
		t.Fatalf("system account = %+v", got)
	}
	if got := ClaudeAccountOf(own).Email; got != "second@example.com" {
		t.Fatalf("own account = %q", got)
	}
	if got := ClaudeAccountOf(Connection{ID: "c3", Provider: "claudecode", ClaudeLogin: ClaudeOwnLogin}).Email; got != "" {
		t.Fatalf("never signed in: %q", got)
	}
}

func TestClaudeSignInAndOutPerConnection(t *testing.T) {
	home, record := claudeAccountsHome(t)
	own := Connection{ID: "b2", Provider: "claudecode", ClaudeLogin: ClaudeOwnLogin}
	system := Connection{ID: "a1", Provider: "claudecode"}
	for _, conn := range []Connection{system, own} {
		process, err := StartClaudeSignIn(context.Background(), conn, home, "", func(string) {})
		if err != nil {
			t.Fatal(err)
		}
		if err := <-process.Done; err != nil {
			t.Fatal(err)
		}
	}
	if err := SignOutClaude(context.Background(), system); err != nil {
		t.Fatal(err)
	}
	if err := SignOutClaude(context.Background(), own); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(record)
	want := "system auth login --claudeai\n" + own.ClaudeHome() + " auth login --claudeai\n" + own.ClaudeHome() + " auth logout\n"
	if string(data) != want {
		t.Fatalf("claude ran:\n%s\nwant:\n%s", data, want)
	}
}

func TestImportFindsEveryAccountsSessions(t *testing.T) {
	home, _ := claudeAccountsHome(t)
	t.Setenv("CODEX_HOME", filepath.Join(home, "no-codex"))
	own := Connection{ID: "b2", Provider: "claudecode", ClaudeLogin: ClaudeOwnLogin}
	line := `{"type":"user","cwd":"/project","message":{"role":"user","content":"hello"}}` + "\n"
	for _, dir := range []string{filepath.Join(home, ".claude"), own.ClaudeHome()} {
		os.MkdirAll(filepath.Join(dir, "projects", "-project"), 0o700)
	}
	os.WriteFile(filepath.Join(home, ".claude", "projects", "-project", "s-system.jsonl"), []byte(line), 0o600)
	os.WriteFile(filepath.Join(own.ClaudeHome(), "projects", "-project", "s-own.jsonl"), []byte(line), 0o600)
	os.WriteFile(filepath.Join(own.ClaudeHome(), ".claude.json"), []byte(`{"oauthAccount":{"emailAddress":"second@example.com"}}`), 0o600)
	found := map[string]ImportSession{}
	for _, session := range FindImportSessions() {
		found[session.ID] = session
	}
	if session, ok := found["s-system"]; !ok || session.Home != "" {
		t.Fatalf("system session: %+v %v", session, ok)
	}
	session, ok := found["s-own"]
	if !ok || session.Home != own.ClaudeHome() || session.Account != "second@example.com" {
		t.Fatalf("own login's session: %+v %v", session, ok)
	}
	if (Config{Session: ClaudeSessionFor(session.Home, session.ID), ClaudeHome: own.ClaudeHome()}).ClaudeSession() != "s-own" {
		t.Fatal("an imported session does not resume in its own folder")
	}
}

func TestClaudeSessionSurvivesLoginSwitch(t *testing.T) {
	a, b := Config{}, Config{ClaudeHome: filepath.Join("accounts", "bbbb")}
	cfg := Config{}
	cfg.SetClaudeSession("sess-a")
	cfg.SyncClaudeSession(4)
	if a.ClaudeSession() != "" || cfg.ClaudeSession() != "sess-a" || cfg.ClaudeMark() != 4 {
		t.Fatalf("login A: session %q mark %d", cfg.ClaudeSession(), cfg.ClaudeMark())
	}
	// The chat runs with login B: A's session is kept aside.
	cfg.ClaudeHome = b.ClaudeHome
	if cfg.ClaudeSession() != "" {
		t.Fatalf("login B resumed %q", cfg.ClaudeSession())
	}
	cfg.SetClaudeSession("sess-b@bbbb")
	cfg.SyncClaudeSession(9)
	if cfg.ClaudeSession() != "sess-b" || cfg.ClaudeMark() != 9 {
		t.Fatalf("login B: session %q mark %d", cfg.ClaudeSession(), cfg.ClaudeMark())
	}
	// Back to A: its session and mark are still there.
	cfg.ClaudeHome = ""
	if cfg.ClaudeSession() != "sess-a" || cfg.ClaudeMark() != 4 {
		t.Fatalf("back on A: session %q mark %d", cfg.ClaudeSession(), cfg.ClaudeMark())
	}
	cfg.SyncClaudeSession(12) // A runs again and has seen more
	cfg.SetClaudeSession("sess-a")
	cfg.ClaudeHome = b.ClaudeHome
	if cfg.ClaudeSession() != "sess-b" || cfg.ClaudeMark() != 9 {
		t.Fatalf("back on B: session %q mark %d", cfg.ClaudeSession(), cfg.ClaudeMark())
	}
	cfg.ClaudeHome = ""
	if cfg.ClaudeMark() != 12 {
		t.Fatalf("A's mark %d, want 12", cfg.ClaudeMark())
	}
	cfg.ClearSessions()
	if cfg.ClaudeSession() != "" || len(cfg.OtherSessions) != 0 {
		t.Fatal("ClearSessions left a session")
	}
}

func TestHarnessSessionsKeepEachOther(t *testing.T) {
	cfg := Config{Provider: "claudecode"}
	cfg.SetClaudeSession("sess-claude")
	cfg.SyncClaudeSession(3)
	cfg.Provider = "antigravity"
	if HarnessSession(cfg) != "" {
		t.Fatalf("Antigravity resumed %q", HarnessSession(cfg))
	}
	cfg.SetClaudeSession(agySessionPrefix + "conv-1")
	cfg.SyncClaudeSession(7)
	if HarnessSession(cfg) != "conv-1" || HarnessMark(cfg) != 7 {
		t.Fatalf("Antigravity: %q mark %d", HarnessSession(cfg), HarnessMark(cfg))
	}
	cfg.Provider = "claudecode"
	if HarnessSession(cfg) != "sess-claude" || HarnessMark(cfg) != 3 {
		t.Fatalf("back on Claude: %q mark %d", HarnessSession(cfg), HarnessMark(cfg))
	}
}
