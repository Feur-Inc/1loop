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
	// own (SSH machines, Extensions, Connectors).
	Page        string   `json:"page,omitempty"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Keywords    []string `json:"keywords,omitempty"`
	Kind        Kind     `json:"kind"`
	Values      []Value  `json:"values,omitempty"`
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
	{Models, "Models & accounts", "The services and accounts the models come from, and what happens at a rate limit."},
	{Agent, "Agent & approvals", "What the agent may do without asking, what it is told in every chat and what it remembers."},
	{Tools, "Tools & extensions", "What extends the agent, and its browser and screen."},
	{Phone, "Phone", "The 1loop phone app: pairing, sharing and what it may do."},
	{Updates, "Updates", "This version of 1loop, and the newer ones."},
	{Advanced, "Advanced", "Commands around the agent's work."},
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
	KeyAutoResume        = "models.auto_resume"
	KeyWorktreeSort      = "general.worktree_sort"
)

var list = []Setting{
	// General.
	{Key: "general.workspace", Section: General, Title: "Project folder", Where: OnDesktop, Kind: Action, Protected: true,
		Description: "The folder the agent works in, with its files, terminals and git.",
		Keywords:    []string{"workspace", "directory", "project", "folder", "path", "cwd"}},
	{Key: "general.target", Section: General, Title: "Execution target", Where: OnPhone, Kind: Action, Protected: true,
		Description: "The computer, server or this phone the agent runs on.",
		Keywords:    []string{"server", "computer", "desktop", "switch", "machine", "local", "connect"}},
	{Key: "general.remote_server", Section: General, Title: "HTTPS server", Where: OnDesktop, Kind: Action, Protected: true,
		Description: "Work on a headless 1loop server with its own models, files, browser and terminals.",
		Keywords:    []string{"remote", "server", "headless", "connect", "pair", "https"}},
	{Key: "general.ssh", Section: General, Page: "SSH machines", Title: "SSH machines", Where: OnDesktop, Kind: Action, Protected: true,
		Description: "Other computers the agent can work on over SSH.",
		Keywords:    []string{"ssh", "remote", "machine", "host", "server", "computer"}},
	{Key: KeyWorktreeSort, Section: General, Title: "Worktree order", Where: OnDesktop, Kind: Choice,
		Description: "How the sidebar and the Repository window order a project's git worktrees.",
		Keywords:    []string{"worktree", "sort", "order", "branch", "git", "sidebar"},
		Values:      []Value{{"recent", "Latest commit"}, {"name", "Name"}, {"pull", "Pull requests first"}}},
	{Key: "general.import", Section: General, Title: "Import chats", Where: OnDesktop, Kind: Action,
		Description: "Bring conversations over from Claude Code, Codex and other apps.",
		Keywords:    []string{"import", "history", "migrate", "conversations", "codex", "claude"}},

	// Models & accounts.
	{Key: "models.connections", Section: Models, Title: "Connections", Where: OnBoth, Kind: Action, Protected: true,
		Description: "The services and accounts the models come from, with their keys and sign-ins.",
		Keywords:    []string{"accounts", "api key", "provider", "openai", "chatgpt", "claude", "openrouter", "vllm", "login", "sign in", "models"}},
	{Key: "models.default_preset", Section: Models, Title: "Default model preset", Where: OnPhone, Kind: Action,
		Description: "For each account, a model per level, used when Default is selected in the composer.",
		Keywords:    []string{"default", "preset", "model", "level"}},
	{Key: "models.context", Section: Models, Title: "Model context", Where: OnPhone, Kind: Action,
		Description: "For each model, the size in tokens at which 1loop compacts a conversation.",
		Keywords:    []string{"context", "tokens", "compact", "window", "limit"}},
	{Key: KeyAutoResume, Section: Models, Title: "Automatic resumption", Where: OnBoth, Kind: Toggle,
		Description: "At a rate limit, wait for the reset or go on with another model, in the order you choose.",
		Keywords:    []string{"rate limit", "quota", "resume", "fallback", "wait", "retry", "switch model", "usage"}},

	// Agent & approvals.
	{Key: KeyApprovalMode, Section: Agent, Title: "Tool permissions", Where: OnBoth, Kind: Choice, Protected: true,
		Description: "Ask before each command and edit, run safe ones on their own (Auto), or run everything (YOLO).",
		Keywords:    []string{"approval", "approve", "yolo", "auto", "sudo", "permissions", "confirm", "ask", "security", "safe"},
		Values:      []Value{{"approval", "Approval required"}, {"auto", "Auto mode"}, {"yolo", "YOLO mode"}}},
	{Key: "agent.preferences", Section: Agent, Title: "Your preferences", Where: OnBoth, Kind: Text,
		Description: "Added to 1loop's system prompt in every chat: language, tone, conventions.",
		Keywords:    []string{"instructions", "system prompt", "personalization", "custom", "tone", "language", "rules", "claude.md"}},
	{Key: KeyMemory, Section: Agent, Title: "Memory", Where: OnBoth, Kind: Toggle,
		Description: "The agent adds what it learns about you to your preferences, each change approved by you.",
		Keywords:    []string{"memory", "remember", "learn", "preferences"}},
	{Key: "agent.project_memory", Section: Agent, Title: "Project memory", Where: OnDesktop, Kind: Action,
		Description: "Each project's AGENTS.md, which every agent reads there.",
		Keywords:    []string{"agents.md", "project", "instructions", "memory", "claude.md"}},
	{Key: KeyDynamicCompaction, Section: Agent, Title: "Dynamic compaction", Where: OnDesktop, Kind: Toggle,
		Description: "Compact the context at natural breaks in the work instead of once when it is nearly full.",
		Keywords:    []string{"context", "compact", "summary", "summarize", "tokens", "window"}},

	// Tools & extensions.
	{Key: "tools.extensions", Section: Tools, Page: "Extensions", Title: "Skills, plugins and MCP servers", Where: OnBoth, Kind: Action,
		Description: "Skills and plugins from GitHub, and the MCP servers the agent uses.",
		Keywords:    []string{"extensions", "skills", "plugins", "mcp", "servers", "tools"}},
	{Key: "tools.github", Section: Tools, Page: "Connectors", Title: "GitHub connector", Where: OnBoth, Kind: Action, Protected: true,
		Description: "Lets the agent read and act on your GitHub repositories, issues and pull requests.",
		Keywords:    []string{"github", "git", "connector", "token", "oauth", "pull request", "issues"}},
	{Key: "tools.browser_data", Section: Tools, Title: "Browsing data", Where: OnBoth, Kind: Action,
		Description: "The agent's browser keeps its cookies, logins and history. Clear erases all of it.",
		Keywords:    []string{"browser", "cookies", "cache", "history", "clear", "logins", "web"}},
	{Key: "tools.import_cookies", Section: Tools, Title: "Import cookies", Where: OnDesktop, Kind: Action, Protected: true,
		Description: "Import a cookie export or a local browser profile into the agent's browser.",
		Keywords:    []string{"cookies", "browser", "login", "import", "session"}},
	{Key: "tools.computer_screen", Section: Tools, Title: "Shared screen", Where: OnDesktop, Kind: Action, Protected: true,
		Description: "The screen Computer Use sees, remembered between restarts.",
		Keywords:    []string{"computer use", "screen", "display", "monitor", "screenshot", "share"}},
	{Key: "tools.linux", Section: Tools, Title: "Linux", Where: OnPhone, Kind: Action,
		Description: "The Debian the local agent works in: storage, packages, a terminal and a reset.",
		Keywords:    []string{"linux", "debian", "terminal", "packages", "proot", "storage"}},

	// Phone.
	{Key: "phone.remote", Section: Phone, Title: "Phone app on this network", Where: OnDesktop, Kind: Toggle, Protected: true,
		Description: "Lets the 1loop phone app find and reach this computer, once paired.",
		Keywords:    []string{"phone", "mobile", "android", "remote", "network", "lan"}},
	{Key: "phone.pairing", Section: Phone, Title: "Paired phones", Where: OnDesktop, Kind: Action, Protected: true,
		Description: "Pair a phone with a QR code, or forget one.",
		Keywords:    []string{"pair", "qr", "code", "phone", "devices", "forget"}},
	{Key: "phone.status", Section: Phone, Title: "While a model runs", Where: OnPhone, Kind: Choice,
		Description: "What shows over your other apps while the agent works: a small badge, a Live Update or nothing.",
		Keywords:    []string{"badge", "status", "overlay", "notification", "live update", "bubble", "floating"},
		Values:      []Value{{"badge", "Badge"}, {"live", "Live Update"}, {"off", "Off"}}},
	{Key: "phone.adb", Section: Phone, Title: "Phone access", Where: OnPhone, Kind: Action, Protected: true,
		Description: "Lets the local agent use this phone through Shizuku and local ADB.",
		Keywords:    []string{"shizuku", "adb", "phone use", "permissions", "control"}},
	{Key: "phone.work_profile", Section: Phone, Title: "Work profile for the agent", Where: OnPhone, Kind: Action, Protected: true,
		Description: "A separate Android profile where the agent installs and uses its apps.",
		Keywords:    []string{"work profile", "profile", "apps", "isolation", "sandbox"}},
	{Key: "phone.background", Section: Phone, Title: "Background execution", Where: OnPhone, Kind: Action,
		Description: "Keeps long local tasks running when 1loop is not on screen.",
		Keywords:    []string{"battery", "background", "optimization", "doze", "sleep", "unrestricted"}},
	{Key: "phone.sharing", Section: Phone, Title: "Share this phone", Where: OnPhone, Kind: Action, Protected: true,
		Description: "Lets the desktop or another phone on this network use this phone's agent.",
		Keywords:    []string{"share", "pair", "remote", "network", "devices", "fingerprint"}},

	// Updates.
	{Key: "updates.check", Section: Updates, Title: "1loop updates", Where: OnBoth, Kind: Action,
		Description: "Check the release page now, and update to the newest version.",
		Keywords:    []string{"update", "upgrade", "version", "release", "new", "download"}},

	// Advanced.
	{Key: "advanced.hooks", Section: Advanced, Title: "Hook commands", Where: OnBoth, Kind: Action,
		Description: "Your commands around the agent's work, in Claude Code's format: before and after tools, on a prompt, when it stops.",
		Keywords:    []string{"hooks", "pretooluse", "posttooluse", "scripts", "commands", "automation"}},
}

// All lists every setting, in the order they are shown.
func All() []Setting { return append([]Setting(nil), list...) }

// On lists the settings a platform's page shows.
func On(platform Where) []Setting {
	var shown []Setting
	for _, setting := range list {
		if setting.On(platform) {
			shown = append(shown, setting)
		}
	}
	return shown
}

// Lookup finds a setting by its key.
func Lookup(key string) (Setting, bool) {
	key = strings.TrimSpace(key)
	for _, setting := range list {
		if setting.Key == key {
			return setting, true
		}
	}
	return Setting{}, false
}
