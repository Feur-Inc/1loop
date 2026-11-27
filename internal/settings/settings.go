// SPDX-License-Identifier: MPL-2.0

// Package settings is the one list of 1loop's settings: their sections,
// titles, explanations and the words people search them by. The desktop's
// Settings page and the phone's are laid out from it, its search finds in
// it, and the model's settings tool reads it to know what it may touch.
//
// The phone gets a copy of the list when it is built (phone.json, kept in
// step by a test), since its screen is built for its own version's list.
package settings

import "strings"

// Section is one page of the settings, in the order they are shown.
type Section struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Intro string `json:"intro"`
}

// Kind is what a setting's control is.
type Kind string

const (
	Toggle Kind = "toggle" // on or off
	Choice Kind = "choice" // one of Values
	Number Kind = "number" // a whole number from Min to Max
	Text   Kind = "text"   // free text
	Action Kind = "action" // a button, a list or a page: no value of its own
	// A keyboard shortcut: a key with its modifiers, as ShortcutValue gives
	// it ("Ctrl+Shift+F"), or "off".
	Shortcut Kind = "shortcut"
)

// Value is one of a choice's values, with how it is shown.
type Value struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Where says which settings page shows a setting.
type Where string

const (
	OnBoth    Where = "both"
	OnDesktop Where = "desktop"
	OnPhone   Where = "phone"
)

// Setting is one entry of the list.
type Setting struct {
	Key     string `json:"key"`
	Section string `json:"section"`
	// Page is the desktop page that holds it, when it is not the section's
	// own (Extensions, Connectors).
	Page string `json:"page,omitempty"`
	// Group is the part of its section's page it is in, under a heading of
	// that name, when the section has parts (Remote).
	Group       string   `json:"group,omitempty"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Keywords    []string `json:"keywords,omitempty"`
	Kind        Kind     `json:"kind"`
	Values      []Value  `json:"values,omitempty"`
	Default     string   `json:"default,omitempty"` // a shortcut's own key
	Min         int      `json:"min,omitempty"`
	Max         int      `json:"max,omitempty"`
	Where       Where    `json:"where"`
	// Protected settings are the user's alone: permissions, accounts and
	// keys, sharing and pairing. The model can neither read nor change them.
	Protected bool `json:"protected,omitempty"`
}

// On says whether the page of a platform ("desktop" or "phone") shows it.
func (setting Setting) On(platform Where) bool {
	return setting.Where == OnBoth || setting.Where == platform
}

// Label is how a value of a choice is shown, the value itself otherwise.
func (setting Setting) Label(value string) string {
	for _, option := range setting.Values {
		if option.ID == value {
			return option.Label
		}
	}
	return value
}

// The sections, the same on the desktop and the phone.
const (
	General  = "general"
	Models   = "models"
	Agent    = "agent"
	Tools    = "tools"
	Remote   = "remote"
	Updates  = "updates"
	Advanced = "advanced"
)

var sections = []Section{
	{General, "General", "The project folder, how 1loop looks, and what comes from elsewhere."},
	{Remote, "Remote", "Where the agent runs, the devices that control this 1loop, and the phone app."},
	{Models, "Models & accounts", "Services and accounts the models come from."},
	{Agent, "Agent & approvals", "What the agent may do, is told and remembers."},
	{Tools, "Tools & extensions", "What extends the agent, and its browser and screen."},
	{Updates, "Updates", "This version of 1loop, and the newer ones."},
	{Advanced, "Advanced", "Commands around the agent's work, and crash reports."},
}

// Sections lists the sections in order.
func Sections() []Section { return append([]Section(nil), sections...) }

// SectionTitle is a section's title, "" for an unknown one.
func SectionTitle(id string) string {
	for _, section := range sections {
		if section.ID == id {
			return section.Title
		}
	}
	return ""
}

// The parts of the Remote section, in order.
const (
	GroupTargets = "Where the agent runs"
	GroupDevices = "Devices that control this 1loop"
	GroupPhone   = "Phone app"
)

// Keys of the settings 1loop itself reads and writes by key.
const (
	KeyApprovalMode      = "agent.approval_mode"
	KeyDynamicCompaction = "agent.dynamic_compaction"
	KeyMemory            = "agent.memory"
	KeyWrapUp            = "agent.wrap_up"
	KeyAutoResume        = "models.auto_resume"
	KeyNamingModel       = "models.naming_model"
	KeyWorktreeSort      = "general.worktree_sort"
	KeyDiscordPresence   = "general.discord_presence"
	KeyDiscordDetails    = "general.discord_details"
	KeyTheme             = "general.theme"
	KeyAccent            = "general.accent"
	KeyAnimations        = "general.animations"
	KeyNotifications     = "general.notifications"
	KeyShaderFPS         = "general.shader_fps"
	KeyCrashReports      = "advanced.crash_reports"
)

var list = []Setting{
	// General.
	{Key: "general.workspace", Section: General, Title: "Project folder", Where: OnDesktop, Kind: Action, Protected: true,
		Keywords: []string{"workspace", "directory", "project", "folder", "path", "cwd"}},
	{Key: KeyWorktreeSort, Section: General, Title: "Worktree order", Where: OnDesktop, Kind: Choice,
		Keywords: []string{"worktree", "sort", "order", "branch", "git", "sidebar"},
		Values:   []Value{{"recent", "Latest commit"}, {"name", "Name"}, {"pull", "Pull requests first"}}},
	{Key: KeyTheme, Section: General, Title: "Theme", Where: OnBoth, Kind: Choice,
		Keywords: []string{"theme", "dark", "light", "mode", "appearance", "colors", "colours", "night", "system", "theme file", "custom theme", "matugen", "roundness", "corners"},
		Values:   []Value{{"dark", "Dark"}, {"light", "Light"}, {"system", "System"}}},
	{Key: KeyAccent, Section: General, Title: "Accent colour", Where: OnBoth, Kind: Choice,
		Keywords: []string{"accent", "color", "colour", "highlight", "amber", "theme", "appearance", "custom"},
		Values: []Value{{"amber", "Amber"}, {"orange", "Orange"}, {"pink", "Pink"}, {"purple", "Purple"}, {"blue", "Blue"},
			{"teal", "Teal"}, {"green", "Green"}, {"custom", "Custom colour"}}},
	{Key: "general.close_to_tray", Section: General, Title: "Close to tray", Where: OnDesktop, Kind: Action,
		Keywords: []string{"tray", "close", "quit", "background", "minimize", "window"}},
	{Key: KeyNotifications, Section: General, Title: "Notifications", Where: OnBoth, Kind: Toggle,
		Description: "When a chat finishes or waits for you, with 1loop in the background.",
		Keywords:    []string{"notification", "notify", "alert", "popup", "toast", "mute", "silence", "do not disturb", "done", "background"}},
	{Key: KeyDiscordPresence, Section: General, Title: "Discord status", Where: OnDesktop, Kind: Toggle, Protected: true,
		Description: "Shows on your Discord profile what the agent is doing.",
		Keywords:    []string{"discord", "rich presence", "status", "activity", "profile", "playing", "rpc"}},
	{Key: KeyDiscordDetails, Section: General, Title: "Share the chat on Discord", Where: OnDesktop, Kind: Toggle, Protected: true,
		Description: "Adds the chat's title and project to the Discord status.",
		Keywords:    []string{"discord", "rich presence", "privacy", "project", "chat", "details", "share"}},
	{Key: KeyShaderFPS, Section: General, Title: "Shader background", Where: OnDesktop, Kind: Choice,
		Description: "Redraws of a theme's shader background. Off saves battery.",
		Keywords:    []string{"shader", "background", "glsl", "animation", "fps", "frame rate", "theme", "wallpaper", "battery"},
		Values:      []Value{{"off", "Off"}, {"15", "15 fps"}, {"30", "30 fps"}, {"60", "60 fps"}}},
	{Key: KeyAnimations, Section: General, Title: "Animations", Where: OnBoth, Kind: Toggle,
		Keywords: []string{"animation", "motion", "reduce motion", "reduced motion", "transitions", "effects", "accessibility", "fade"}},
	{Key: "general.usage", Section: General, Page: "Usage", Title: "Usage & spend", Where: OnBoth, Kind: Action,
		Keywords: []string{"usage", "spend", "cost", "costs", "tokens", "price", "prices", "money", "billing", "budget", "dollars"}},
	{Key: "general.import", Section: General, Title: "Import chats", Where: OnDesktop, Kind: Action,
		Description: "Bring chats over from Claude Code, Codex and other apps.",
		Keywords:    []string{"import", "history", "migrate", "conversations", "codex", "claude"}},

	// Models & accounts.
	{Key: "models.connections", Section: Models, Title: "Connections", Where: OnBoth, Kind: Action, Protected: true,
		Description: "Services and accounts the models come from, with their keys.",
		Keywords:    []string{"accounts", "api key", "provider", "openai", "chatgpt", "claude", "openrouter", "vllm", "login", "sign in", "models"}},
	{Key: "models.default_preset", Section: Models, Title: "Default model preset", Where: OnPhone, Kind: Action,
		Description: "A model per level for each account, used by Default.",
		Keywords:    []string{"default", "preset", "model", "level"}},
	{Key: "models.context", Section: Models, Title: "Model context", Where: OnPhone, Kind: Action,
		Description: "Tokens at which each model's chats are compacted.",
		Keywords:    []string{"context", "tokens", "compact", "window", "limit"}},
	{Key: KeyAutoResume, Section: Models, Title: "Automatic resumption", Where: OnBoth, Kind: Toggle,
		Description: "At a rate limit, wait for the reset or switch model.",
		Keywords:    []string{"rate limit", "quota", "resume", "fallback", "wait", "retry", "switch model", "usage"}},
	{Key: KeyNamingModel, Section: Models, Title: "Chat naming model", Where: OnDesktop, Kind: Action,
		Keywords: []string{"name", "naming", "title", "rename", "auto name", "auto title", "summarize", "summary", "chat name", "files", "privacy"}},

	// Agent & approvals.
	{Key: KeyApprovalMode, Section: Agent, Title: "Tool permissions", Where: OnBoth, Kind: Choice, Protected: true,
		Description: "Asks before commands and edits, or runs them at once (YOLO).",
		Keywords:    []string{"approval", "approve", "yolo", "auto", "sudo", "permissions", "confirm", "ask", "security", "safe"},
		Values:      []Value{{"approval", "Approval required"}, {"auto", "Auto mode"}, {"yolo", "YOLO mode"}}},
	{Key: "agent.preferences", Section: Agent, Title: "Your preferences", Where: OnBoth, Kind: Text,
		Description: "Added to every chat: language, tone, conventions.",
		Keywords:    []string{"instructions", "system prompt", "personalization", "custom", "tone", "language", "rules", "claude.md", "conventions"}},
	{Key: KeyMemory, Section: Agent, Title: "Memory", Where: OnBoth, Kind: Toggle,
		Description: "The agent proposes what it learns; you approve each change.",
		Keywords:    []string{"memory", "remember", "learn", "preferences"}},
	{Key: KeyWrapUp, Section: Agent, Title: "Next-step card", Where: OnBoth, Kind: Toggle,
		Description: "A recap at the end of each turn, with buttons for your reply.",
		Keywords:    []string{"wrap up", "next step", "recap", "card", "buttons", "end of turn"}},
	{Key: "agent.project_memory", Section: Agent, Title: "Project memory", Where: OnBoth, Kind: Action,
		Description: "Each project's AGENTS.md, read by every agent.",
		Keywords:    []string{"agents.md", "project", "instructions", "memory", "claude.md"}},
	{Key: "loopy.home", Section: Agent, Page: "Loopy", Title: "Where Loopy lives", Where: OnDesktop, Kind: Action, Protected: true,
		Description: "This computer, or a server where Loopy's routines go on when this computer is off.",
		Keywords:    []string{"loopy", "assistant", "personal agent", "server", "host", "always on"}},
	{Key: "loopy.model", Section: Agent, Page: "Loopy", Title: "Loopy's model", Where: OnBoth, Kind: Action,
		Description: "The model Loopy, the personal agent, works with: its own, or the one chosen for your projects.",
		Keywords:    []string{"loopy", "assistant", "personal agent", "model"}},
	{Key: "loopy.summary", Section: Agent, Page: "Loopy", Title: "Morning summary", Where: OnBoth, Kind: Action,
		Description: "When Loopy sums up each morning what needs you and what is in progress, or never; and its night.",
		Keywords:    []string{"loopy", "morning", "summary", "brief", "night", "quiet"}},
	{Key: "loopy.memory", Section: Agent, Page: "Loopy", Title: "Loopy's memory", Where: OnBoth, Kind: Action,
		Description: "What Loopy remembers of you: rules, how you like answers, facts, its journal; whether it learns on its own.",
		Keywords:    []string{"loopy", "memory", "remember", "journal", "forget", "habits", "style", "learn"}},
	{Key: "loopy.accounts", Section: Agent, Page: "Loopy", Title: "Loopy's accounts", Where: OnBoth, Kind: Action, Protected: true,
		Description: "The mail and calendar accounts Loopy reads. It sends and adds only from a card you press.",
		Keywords:    []string{"loopy", "mail", "email", "imap", "calendar", "caldav", "account"}},
	{Key: "loopy.vault", Section: Agent, Page: "Loopy", Title: "Loopy's vault", Where: OnBoth, Kind: Action, Protected: true,
		Description: "Your password manager, behind a master password: sign-ins, keys and two-step codes 1loop types in a page or hands to an AutoLink, which Loopy never sees.",
		Keywords:    []string{"loopy", "vault", "password", "manager", "master", "sign in", "login", "secret", "totp", "two-step", "api key"}},
	{Key: "loopy.autolinks", Section: Agent, Page: "Loopy", Title: "Loopy's AutoLinks", Where: OnBoth, Kind: Action, Protected: true,
		Description: "The scripts Loopy made to reach a service or watch something: what each may do, when it runs, its history, its script.",
		Keywords:    []string{"loopy", "autolink", "connector", "script", "python", "automation", "automate", "watch", "trigger"}},
	{Key: KeyDynamicCompaction, Section: Agent, Title: "Dynamic compaction", Where: OnDesktop, Kind: Toggle,
		Description: "Compacts at natural breaks in the work, not when nearly full.",
		Keywords:    []string{"context", "compact", "summary", "summarize", "tokens", "window"}},

	// Tools & extensions.
	{Key: "tools.extensions", Section: Tools, Page: "Extensions", Title: "Skills, plugins and MCP servers", Where: OnBoth, Kind: Action,
		Keywords: []string{"extensions", "skills", "plugins", "mcp", "servers", "tools"}},
	{Key: "tools.github", Section: Tools, Page: "Connectors", Title: "GitHub connector", Where: OnBoth, Kind: Action, Protected: true,
		Description: "Lets the agent work on your GitHub repositories and issues.",
		Keywords:    []string{"github", "git", "connector", "token", "oauth", "pull request", "issues", "repositories"}},
	{Key: "tools.browser_data", Section: Tools, Title: "Browsing data", Where: OnBoth, Kind: Action,
		Description: "Cookies, logins and history the agent's browser keeps.",
		Keywords:    []string{"browser", "cookies", "cache", "history", "clear", "logins", "web"}},
	{Key: "tools.import_cookies", Section: Tools, Title: "Import cookies", Where: OnDesktop, Kind: Action, Protected: true,
		Description: "Cookies from an export or a browser profile, for the agent.",
		Keywords:    []string{"cookies", "browser", "login", "import", "session", "export", "profile"}},
	{Key: "tools.computer_screen", Section: Tools, Title: "Shared screen", Where: OnDesktop, Kind: Action, Protected: true,
		Description: "The screen Computer Use sees, remembered between restarts.",
		Keywords:    []string{"computer use", "screen", "display", "monitor", "screenshot", "share"}},
	{Key: "tools.linux", Section: Tools, Title: "Linux", Where: OnPhone, Kind: Action,
		Description: "Debian for the local agent: packages, terminal and reset.",
		Keywords:    []string{"linux", "debian", "terminal", "packages", "proot", "storage"}},

	// Remote.
	{Key: "general.target", Section: Remote, Group: GroupTargets, Title: "Execution target", Where: OnBoth, Kind: Action, Protected: true,
		Description: "Where the agent runs: this computer, SSH, a server or phone.",
		Keywords:    []string{"server", "computer", "desktop", "switch", "machine", "local", "connect", "this computer", "phone", "ssh", "link", "unlink", "forget"}},
	{Key: "general.ssh", Section: Remote, Group: GroupTargets, Title: "SSH machines", Where: OnDesktop, Kind: Action, Protected: true,
		Keywords: []string{"ssh", "remote", "machine", "host", "server", "computer", "vm"}},
	{Key: "general.remote_server", Section: Remote, Group: GroupTargets, Title: "Connect a server", Where: OnDesktop, Kind: Action, Protected: true,
		Keywords: []string{"remote", "server", "headless", "connect", "pair", "pairing", "https", "fingerprint", "certificate", "docker", "forget"}},
	{Key: "phone.pairing", Section: Remote, Group: GroupDevices, Title: "Paired devices", Where: OnBoth, Kind: Action, Protected: true,
		Description: "Phones and browsers paired with this computer or the server in use.",
		Keywords:    []string{"pair", "paired", "devices", "phone", "browser", "forget", "remove", "unpair", "revoke", "phones"}},
	{Key: "phone.sharing", Section: Remote, Group: GroupDevices, Title: "Share this phone", Where: OnPhone, Kind: Action, Protected: true,
		Description: "Lets the desktop or another phone use this phone's agent.",
		Keywords:    []string{"share", "pair", "remote", "network", "devices", "fingerprint", "remove", "unpair"}},
	{Key: "phone.remote", Section: Remote, Group: GroupPhone, Title: "Phone app on this network", Where: OnDesktop, Kind: Toggle, Protected: true,
		Keywords: []string{"phone", "mobile", "android", "remote", "network", "lan", "pair", "qr", "code"}},
	{Key: "phone.status", Section: Remote, Group: GroupPhone, Title: "While a model runs", Where: OnPhone, Kind: Choice,
		Description: "What shows over other apps while the agent works.",
		Keywords:    []string{"badge", "status", "overlay", "notification", "live update", "bubble", "floating"},
		Values:      []Value{{"badge", "Badge"}, {"live", "Live Update"}, {"off", "Off"}}},
	{Key: "phone.adb", Section: Remote, Group: GroupPhone, Title: "Phone access", Where: OnPhone, Kind: Action, Protected: true,
		Description: "Lets the local agent use this phone, via Shizuku or ADB.",
		Keywords:    []string{"shizuku", "adb", "phone use", "permissions", "control"}},
	{Key: "phone.work_profile", Section: Remote, Group: GroupPhone, Title: "Work profile for the agent", Where: OnPhone, Kind: Action, Protected: true,
		Description: "A separate Android profile for the agent's apps.",
		Keywords:    []string{"work profile", "profile", "apps", "isolation", "sandbox"}},
	{Key: "phone.background", Section: Remote, Group: GroupPhone, Title: "Background execution", Where: OnPhone, Kind: Action,
		Keywords: []string{"battery", "background", "optimization", "doze", "sleep", "unrestricted"}},

	// Updates.
	{Key: "updates.check", Section: Updates, Title: "1loop updates", Where: OnBoth, Kind: Action,
		Keywords: []string{"update", "upgrade", "version", "release", "new", "download"}},

	// Advanced.
	{Key: "advanced.hooks", Section: Advanced, Title: "Hook commands", Where: OnBoth, Kind: Action,
		Keywords: []string{"hooks", "pretooluse", "posttooluse", "scripts", "commands", "automation"}},
	{Key: KeyCrashReports, Section: Advanced, Title: "Offer to report crashes", Where: OnBoth, Kind: Toggle,
		Description: "Offer an anonymous crash report. You file it on GitHub.",
		Keywords:    []string{"crash", "report", "bug", "issue", "github", "closed unexpectedly", "don't ask again", "feedback", "anonymous", "privacy"}},
}

// All lists every setting, in the order they are shown.
func All() []Setting { return append(append([]Setting(nil), list...), shortcuts...) }

// On lists the settings a platform's page shows.
func On(platform Where) []Setting {
	var shown []Setting
	for _, setting := range All() {
		if setting.On(platform) {
			shown = append(shown, setting)
		}
	}
	return shown
}

// Lookup finds a setting by its key.
func Lookup(key string) (Setting, bool) {
	key = strings.TrimSpace(key)
	for _, setting := range All() {
		if setting.Key == key {
			return setting, true
		}
	}
	return Setting{}, false
}
