package agent

// Several Claude Code accounts. Claude Code has no account switcher: its
// login, settings and sessions live in one configuration folder, ~/.claude
// (and ~/.claude.json), or the folder CLAUDE_CONFIG_DIR names. A Claude Code
// connection either uses that folder, the system Claude Code login, or keeps
// a folder of its own under 1loop's configuration, so each connection signs
// in to its own account and keeps its own usage limits and sessions. Every
// process 1loop starts for the connection (sign-in, models, runs, summaries)
// gets its folder.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ClaudeOwnLogin is Connection.ClaudeLogin for a connection with its own
// Claude Code folder; "" is the system Claude Code login.
const ClaudeOwnLogin = "own"

var claudeHomeKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ClaudeAccountsDir holds the Claude Code folders of the connections that
// have their own login, one per connection ID.
func ClaudeAccountsDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "1loop", "claude-code")
}

// ClaudeHome is the CLAUDE_CONFIG_DIR of the connection's Claude Code
// processes, "" for the system Claude Code login.
func (conn Connection) ClaudeHome() string {
	if conn.Provider != "claudecode" || conn.ClaudeLogin != ClaudeOwnLogin || !claudeHomeKey.MatchString(conn.ID) {
		return ""
	}
	if dir := ClaudeAccountsDir(); dir != "" {
		return filepath.Join(dir, conn.ID)
	}
	return ""
}

// NewClaudeLogin is the login a new Claude Code connection starts with: the
// system one when no other connection uses it, else a login of its own, so
// a second connection is a second account.
func NewClaudeLogin(connections []Connection, id string) string {
	for _, conn := range connections {
		if conn.ID != id && conn.Provider == "claudecode" && conn.ClaudeLogin != ClaudeOwnLogin {
			return ClaudeOwnLogin
		}
	}
	return ""
}

// claudeDataDir is the folder Claude Code keeps its credentials and
// sessions in for this home.
func claudeDataDir(home string) string {
	if home != "" {
		return home
	}
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	user, _ := os.UserHomeDir()
	return filepath.Join(user, ".claude")
}

// claudeStateFile is Claude Code's .claude.json for this home: inside the
// configuration folder, or next to ~/.claude without CLAUDE_CONFIG_DIR.
func claudeStateFile(home string) string {
	if home == "" && os.Getenv("CLAUDE_CONFIG_DIR") == "" {
		user, _ := os.UserHomeDir()
		return filepath.Join(user, ".claude.json")
	}
	return filepath.Join(claudeDataDir(home), ".claude.json")
}

// ClaudeAccount is the account a Claude Code folder is signed in to, as
// Claude Code records it; empty when it is signed out or never was.
type ClaudeAccount struct {
	Email, Organization string
}

// ClaudeAccountOf reads the account of a connection's Claude Code folder.
func ClaudeAccountOf(conn Connection) ClaudeAccount {
	return claudeAccountIn(conn.ClaudeHome())
}

func claudeAccountIn(home string) ClaudeAccount {
	data, err := os.ReadFile(claudeStateFile(home))
	if err != nil {
		return ClaudeAccount{}
	}
	var state struct {
		OAuth *struct {
			Email        string `json:"emailAddress"`
			Organization string `json:"organizationName"`
		} `json:"oauthAccount"`
	}
	if json.Unmarshal(data, &state) != nil || state.OAuth == nil {
		return ClaudeAccount{}
	}
	return ClaudeAccount{Email: state.OAuth.Email, Organization: state.OAuth.Organization}
}

// ClaudeSignedIn tells whether the folder holds a Claude login. Only where
// Claude Code keeps it in a file (Linux, Windows, the phone); elsewhere the
// account Claude Code recorded stands for it.
func ClaudeSignedIn(conn Connection) bool {
	data, err := os.ReadFile(filepath.Join(claudeDataDir(conn.ClaudeHome()), ".credentials.json"))
	if err != nil {
		return ClaudeAccountOf(conn).Email != ""
	}
	var credentials struct {
		OAuth struct {
			Access  string `json:"accessToken"`
			Refresh string `json:"refreshToken"`
		} `json:"claudeAiOauth"`
	}
	if json.Unmarshal(data, &credentials) != nil {
		return false
	}
	return credentials.OAuth.Access != "" || credentials.OAuth.Refresh != ""
}

// claudeSharedFiles are the user's Claude Code settings a new folder links
// to, so a second account keeps the same rules, instructions, agents and
// commands; its login, sessions and history stay its own.
var claudeSharedFiles = []string{"settings.json", "CLAUDE.md", "agents", "commands", "skills", "output-styles"}

// prepareClaudeHome creates a connection's folder, linked to the user's
// shared settings the first time.
func prepareClaudeHome(home string) error {
	if _, err := os.Stat(home); err == nil {
		return nil
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	shared := claudeDataDir("")
	if strings.HasPrefix(shared, ClaudeAccountsDir()) {
		return nil
	}
	for _, name := range claudeSharedFiles {
		if _, err := os.Stat(filepath.Join(shared, name)); err == nil {
			_ = os.Symlink(filepath.Join(shared, name), filepath.Join(home, name)) // optional: Windows may refuse links
		}
	}
	return nil
}

// A chat's Claude Code session exists only in the folder that created it:
// its reference names that folder, so a chat moved to another Claude Code
// account starts a session there instead of resuming one it cannot reach.
// The system login's sessions keep their plain ID.
func claudeSessionRef(home, id string) string {
	if home == "" || id == "" {
		return id
	}
	return id + "@" + filepath.Base(home)
}

// ClaudeSessionFor is the reference of a session found in a Claude Code
// folder (chat import).
func ClaudeSessionFor(home, id string) string {
	return claudeSessionRef(home, id)
}

// LoginSession is a chat's session in one Claude Code login (its reference)
// and how many of the chat's items it has seen.
type LoginSession struct {
	Session string `json:"session"`
	Mark    int    `json:"mark,omitempty"`
}

// sessionLogin is the login a session reference belongs to: its folder
// name, "" for the system login.
func sessionLogin(ref string) string {
	_, key, _ := strings.Cut(ref, "@")
	return key
}

func (cfg Config) claudeLogin() string {
	if cfg.ClaudeHome == "" {
		return ""
	}
	return filepath.Base(cfg.ClaudeHome)
}

// loginSession is the chat's session and mark in the configuration's login:
// the active one, or the one kept when the chat ran with another login.
func (cfg Config) loginSession() LoginSession {
	login := cfg.claudeLogin()
	if cfg.Session != "" && sessionLogin(cfg.Session) == login {
		return LoginSession{cfg.Session, cfg.SessionMark}
	}
	return cfg.OtherSessions[login]
}

// ClaudeSession is the session the configuration resumes in Claude Code,
// "" when its login has none.
func (cfg Config) ClaudeSession() string {
	id, _, _ := strings.Cut(cfg.loginSession().Session, "@")
	return id
}

// ClaudeMark is how many of the chat's items that session has seen.
func (cfg Config) ClaudeMark() int {
	return cfg.loginSession().Mark
}

func cloneSessions(sessions map[string]LoginSession) map[string]LoginSession {
	if sessions == nil {
		return nil
	}
	clone := make(map[string]LoginSession, len(sessions)+1)
	for key, value := range sessions {
		clone[key] = value
	}
	return clone
}

// SetClaudeSession records the session a Claude Code turn named. A session
// of another login moves aside, not away: it comes back when that login does.
func (cfg *Config) SetClaudeSession(ref string) {
	if ref == "" {
		return
	}
	login := sessionLogin(ref)
	if cfg.Session == ref {
		return
	}
	cfg.OtherSessions = cloneSessions(cfg.OtherSessions)
	mark := 0
	if kept, ok := cfg.OtherSessions[login]; ok {
		if kept.Session == ref {
			mark = kept.Mark
		}
		delete(cfg.OtherSessions, login)
	}
	if cfg.Session != "" && sessionLogin(cfg.Session) != login {
		if cfg.OtherSessions == nil {
			cfg.OtherSessions = map[string]LoginSession{}
		}
		cfg.OtherSessions[sessionLogin(cfg.Session)] = LoginSession{cfg.Session, cfg.SessionMark}
	}
	cfg.Session, cfg.SessionMark = ref, mark
}

// SyncClaudeSession records that the configuration's login session has seen
// the first n items of the chat.
func (cfg *Config) SyncClaudeSession(n int) {
	login := cfg.claudeLogin()
	if cfg.Session != "" && sessionLogin(cfg.Session) == login {
		cfg.SessionMark = n
	} else if kept, ok := cfg.OtherSessions[login]; ok {
		cfg.OtherSessions = cloneSessions(cfg.OtherSessions) // the map may be shared with another copy of the config
		kept.Mark = n
		cfg.OtherSessions[login] = kept
	}
}

// InheritSession takes the chat's Claude Code sessions from another
// configuration of the same chat.
func (cfg *Config) InheritSession(from Config) {
	cfg.Session, cfg.SessionMark = from.Session, from.SessionMark
	cfg.OtherSessions = from.OtherSessions
}

// ClearSessions forgets every Claude Code session of the chat.
func (cfg *Config) ClearSessions() {
	cfg.Session, cfg.SessionMark, cfg.OtherSessions = "", 0, nil
}

// claudeHomes are the Claude Code folders on this computer: the system one
// and every connection's own, for chat import.
func claudeHomes() []string {
	homes := []string{""}
	entries, _ := os.ReadDir(ClaudeAccountsDir())
	for _, entry := range entries {
		if entry.IsDir() && claudeHomeKey.MatchString(entry.Name()) {
			homes = append(homes, filepath.Join(ClaudeAccountsDir(), entry.Name()))
		}
	}
	return homes
}
