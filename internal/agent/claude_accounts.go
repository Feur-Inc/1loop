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

// ClaudeSession is the session the configuration resumes in Claude Code,
// "" when it has none or the session belongs to another account's folder.
func (cfg Config) ClaudeSession() string {
	id, key, own := strings.Cut(cfg.Session, "@")
	if cfg.ClaudeHome == "" && !own || cfg.ClaudeHome != "" && own && key == filepath.Base(cfg.ClaudeHome) {
		return id
	}
	return ""
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
