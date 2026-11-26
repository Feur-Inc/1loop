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
	Page        string   `json:"page,omitempty"`
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
	Phone    = "phone"
	Updates  = "updates"
	Advanced = "advanced"
)

var sections = []Section{
	{General, "General", "Where the agent works, and what comes from elsewhere."},
	{Models, "Models & accounts", "Services and accounts the models come from."},
	{Agent, "Agent & approvals", "What the agent may do, is told and remembers."},
	{Tools, "Tools & extensions", "What extends the agent, and its browser and screen."},
	{Phone, "Phone", "The 1loop phone app: pairing, sharing and what it may do."},
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
	KeyShaderFPS         = "general.shader_fps"
	KeyCrashReports      = "advanced.crash_reports"
)

var list = []Setting{
	// General.
	{Key: "general.workspace", Section: General, Title: "Project folder", Where: OnDesktop, Kind: Action, Protected: true,
		Description: "The folder the agent works in, with its files and git.",
		Keywords:    []string{"workspace", "directory", "project", "folder", "path", "cwd"}},
	{Key: "general.target", Section: General, Title: "Execution target", Where: OnBoth, Kind: Action, Protected: true,
		Description: "Where the agent runs: this computer, SSH, a server or phone.",
		Keywords:    []string{"server", "computer", "desktop", "switch", "machine", "local", "connect", "this computer", "phone", "ssh"}},
	{Key: "general.remote_server", Section: General, Title: "Connect a server", Where: OnDesktop, Kind: Action, Protected: true,
		Description: "Pair with a headless 1loop server, then work on it there.",
		Keywords:    []string{"remote", "server", "headless", "connect", "pair", "pairing", "https", "fingerprint", "certificate", "docker"}},
	{Key: "general.ssh", Section: General, Title: "SSH machines", Where: OnDesktop, Kind: Action, Protected: true,
		Description: "Other computers the agent can work on over SSH.",
		Keywords:    []string{"ssh", "remote", "machine", "host", "server", "computer", "vm"}},
	{Key: KeyWorktreeSort, Section: General, Title: "Worktree order", Where: OnDesktop, Kind: Choice,
		Description: "How a project's git worktrees are ordered in the sidebar.",
		Keywords:    []string{"worktree", "sort", "order", "branch", "git", "sidebar"},
		Values:      []Value{{"recent", "Latest commit"}, {"name", "Name"}, {"pull", "Pull requests first"}}},
	{Key: KeyTheme, Section: General, Title: "Theme", Where: OnBoth, Kind: Choice,
		Description: "Dark, light or the system's. Also your own theme files.",
		Keywords:    []string{"theme", "dark", "light", "mode", "appearance", "colors", "colours", "night", "system", "theme file", "custom theme", "matugen", "roundness", "corners"},
		Values:      []Value{{"dark", "Dark"}, {"light", "Light"}, {"system", "System"}}},
	{Key: KeyAccent, Section: General, Title: "Accent colour", Where: OnBoth, Kind: Choice,
		Description: "The colour of buttons, links and highlights.",
		Keywords:    []string{"accent", "color", "colour", "highlight", "amber", "theme", "appearance", "custom"},
		Values: []Value{{"amber", "Amber"}, {"orange", "Orange"}, {"pink", "Pink"}, {"purple", "Purple"}, {"blue", "Blue"},
			{"teal", "Teal"}, {"green", "Green"}, {"custom", "Custom colour"}}},
	{Key: "general.close_to_tray", Section: General, Title: "Close to tray", Where: OnDesktop, Kind: Action,
		Description: "Closing the window keeps 1loop in the tray. Quit: Ctrl+Q.",
		Keywords:    []string{"tray", "close", "quit", "background", "minimize", "window"}},
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
		Description: "Short motion as things change. Off with reduced motion.",
		Keywords:    []string{"animation", "motion", "reduce motion", "reduced motion", "transitions", "effects", "accessibility", "fade"}},
	{Key: "general.usage", Section: General, Page: "Usage", Title: "Usage & spend", Where: OnBoth, Kind: Action,
		Description: "Tokens and money spent, by model, project, chat and more.",
		Keywords:    []string{"usage", "spend", "cost", "costs", "tokens", "price", "prices", "money", "billing", "budget", "dollars"}},
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
		Description: "Names new chats from their first message, never its files.",
		Keywords:    []string{"name", "naming", "title", "rename", "auto name", "auto title", "summarize", "summary", "chat name", "files", "privacy"}},

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
	{Key: "agent.project_memory", Section: Agent, Title: "Project memory", Where: OnDesktop, Kind: Action,
		Description: "Each project's AGENTS.md, read by every agent.",
		Keywords:    []string{"agents.md", "project", "instructions", "memory", "claude.md"}},
	{Key: KeyDynamicCompaction, Section: Agent, Title: "Dynamic compaction", Where: OnDesktop, Kind: Toggle,
		Description: "Compacts at natural breaks in the work, not when nearly full.",
		Keywords:    []string{"context", "compact", "summary", "summarize", "tokens", "window"}},

	// Tools & extensions.
	{Key: "tools.extensions", Section: Tools, Page: "Extensions", Title: "Skills, plugins and MCP servers", Where: OnBoth, Kind: Action,
		Description: "Skills, plugins and the MCP servers the agent uses.",
		Keywords:    []string{"extensions", "skills", "plugins", "mcp", "servers", "tools"}},
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

	// Phone.
	{Key: "phone.remote", Section: Phone, Title: "Phone app on this network", Where: OnDesktop, Kind: Toggle, Protected: true,
		Description: "Lets a paired phone find and reach this computer.",
		Keywords:    []string{"phone", "mobile", "android", "remote", "network", "lan"}},
	{Key: "phone.pairing", Section: Phone, Title: "Paired phones", Where: OnDesktop, Kind: Action, Protected: true,
		Description: "Pair a phone with a QR code, or forget one.",
		Keywords:    []string{"pair", "qr", "code", "phone", "devices", "forget"}},
	{Key: "phone.status", Section: Phone, Title: "While a model runs", Where: OnPhone, Kind: Choice,
		Description: "What shows over other apps while the agent works.",
		Keywords:    []string{"badge", "status", "overlay", "notification", "live update", "bubble", "floating"},
		Values:      []Value{{"badge", "Badge"}, {"live", "Live Update"}, {"off", "Off"}}},
	{Key: "phone.adb", Section: Phone, Title: "Phone access", Where: OnPhone, Kind: Action, Protected: true,
		Description: "Lets the local agent use this phone, via Shizuku or ADB.",
		Keywords:    []string{"shizuku", "adb", "phone use", "permissions", "control"}},
	{Key: "phone.work_profile", Section: Phone, Title: "Work profile for the agent", Where: OnPhone, Kind: Action, Protected: true,
		Description: "A separate Android profile for the agent's apps.",
		Keywords:    []string{"work profile", "profile", "apps", "isolation", "sandbox"}},
	{Key: "phone.background", Section: Phone, Title: "Background execution", Where: OnPhone, Kind: Action,
		Description: "Keeps long local tasks running when 1loop is not on screen.",
		Keywords:    []string{"battery", "background", "optimization", "doze", "sleep", "unrestricted"}},
	{Key: "phone.sharing", Section: Phone, Title: "Share this phone", Where: OnPhone, Kind: Action, Protected: true,
		Description: "Lets the desktop or another phone use this phone's agent.",
		Keywords:    []string{"share", "pair", "remote", "network", "devices", "fingerprint"}},

	// Updates.
	{Key: "updates.check", Section: Updates, Title: "1loop updates", Where: OnBoth, Kind: Action,
		Description: "Check for a newer version now.",
		Keywords:    []string{"update", "upgrade", "version", "release", "new", "download"}},

	// Advanced.
	{Key: "advanced.hooks", Section: Advanced, Title: "Hook commands", Where: OnBoth, Kind: Action,
		Description: "Your commands around the agent's work, as in Claude Code.",
		Keywords:    []string{"hooks", "pretooluse", "posttooluse", "scripts", "commands", "automation"}},
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
