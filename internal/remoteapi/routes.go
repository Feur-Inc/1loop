// SPDX-License-Identifier: MPL-2.0

package remoteapi

import (
	"net/http"
	"slices"
)

// PhoneRoutes are the routes every engine serves behind a paired device's
// token, the desktop's remote and a headless server alike, besides what
// NewMux adds (GET /api/info, pairing, GET /api/events): the phone sees the
// same API from both (#518). A route added to one engine goes here and to
// the other, or to ServerOnlyRoutes with its reason; TestRouteParity in
// internal/ui and internal/server holds each engine's Routes to the list.
var PhoneRoutes = []string{
	"GET /api/chats/{id}",
	"GET /api/chats/{id}/compactions",
	"GET /api/chats/{id}/context",
	"GET /api/chats/{id}/items/{index}/images/{n}",
	"GET /api/chats/{id}/panel",
	"GET /api/chats/{id}/panel/{key}",
	"GET /api/chats/{id}/panel/{key}/screen",
	"GET /api/chats/{id}/panel/{key}/stream",
	"GET /api/devices",
	"GET /api/extensions",
	"GET /api/files",
	"GET /api/files/content",
	"GET /api/files/download",
	"GET /api/git",
	"GET /api/git/github",
	"GET /api/git/github/item",
	"GET /api/github",
	"GET /api/hooks",
	"GET /api/limits",
	"GET /api/loopy",
	"GET /api/loopy/file",
	"GET /api/loopy/files",
	"GET /api/loopy/link",
	"GET /api/loopy/memory",
	"GET /api/loopy/messages",
	"GET /api/mentions",
	"GET /api/projects",
	"GET /api/projects/env",
	"GET /api/projects/memory",
	"GET /api/review",
	"GET /api/state",
	"GET /api/sync/base",
	"GET /api/sync/manifest",
	"GET /api/usage",
	"GET /api/usage/prices",
	"POST /api/answer",
	"POST /api/approval-mode",
	"POST /api/approve",
	"POST /api/attachments",
	"POST /api/auto-resume",
	"POST /api/browser/clear",
	"POST /api/chats/{id}/delete",
	"POST /api/chats/{id}/message",
	"POST /api/chats/{id}/moved",
	"POST /api/chats/{id}/panel",
	"POST /api/chats/{id}/panel/{key}/input",
	"POST /api/chats/{id}/pause",
	"POST /api/chats/{id}/rename",
	"POST /api/chats/{id}/resume",
	"POST /api/chats/{id}/tasks/{task}/stop",
	"POST /api/chats/{id}/version",
	"POST /api/devices/{id}/revoke",
	"POST /api/extensions/enable",
	"POST /api/extensions/install",
	"POST /api/extensions/mcp",
	"POST /api/extensions/mcp/remove",
	"POST /api/extensions/mcp/status",
	"POST /api/extensions/prepare",
	"POST /api/extensions/readme",
	"POST /api/extensions/remove",
	"POST /api/extensions/search",
	"POST /api/files/upload",
	"POST /api/git/worktrees",
	"POST /api/git/worktrees/remove",
	"POST /api/github/attachment-token",
	"POST /api/github/cancel",
	"POST /api/github/configure",
	"POST /api/github/connect",
	"POST /api/github/disconnect",
	"POST /api/github/enable",
	"POST /api/hooks",
	"POST /api/loopy/answer",
	"POST /api/loopy/clear",
	"POST /api/loopy/do",
	"POST /api/loopy/file",
	"POST /api/loopy/react",
	"POST /api/loopy/reveal",
	"POST /api/loopy/send",
	"POST /api/loopy/settings",
	"POST /api/loopy/stop",
	"POST /api/memory",
	"POST /api/model",
	"POST /api/model/compaction",
	"POST /api/model/defaults",
	"POST /api/naming-model",
	"POST /api/preferences",
	"POST /api/projects",
	"POST /api/projects/env",
	"POST /api/projects/memory",
	"POST /api/projects/{action}",
	"POST /api/send",
	"POST /api/stop",
	"POST /api/sync/base",
	"POST /api/sync/export",
	"POST /api/sync/import",
	"POST /api/usage/prices",
	"POST /api/wrap-up",
}

// ServerOnlyRoutes are a headless server's routes that the desktop's remote
// does not serve, on purpose, and why.
var ServerOnlyRoutes = map[string]string{
	"GET /api/update":          serverUpdates,
	"POST /api/update":         serverUpdates,
	"POST /api/install/{what}": "it installs Chromium or Claude Code on the server; a desktop's user installs them from its window",
	"POST /api/max-subchats":   "the server's concurrent subchat cap, configured from a paired desktop's Servers page",
	// Not served by a phone's own engine either (internal/server's local mode).
	"GET /api/pairings":                         serverApprovals,
	"POST /api/pairings/{id}":                   serverApprovals,
	"GET /api/connections":                      serverAccounts,
	"POST /api/connections":                     serverAccounts,
	"POST /api/connections/models":              serverAccounts,
	"GET /api/connections/{id}/models":          serverAccounts,
	"POST /api/connections/{id}/disconnect":     serverAccounts,
	"POST /api/connections/{id}/login":          serverAccounts,
	"POST /api/connections/{id}/login/complete": serverAccounts,
	"POST /api/connections/refresh":             serverAccounts,
}

const (
	serverUpdates   = "the server updates itself when a paired device asks; a desktop updates from its own window"
	serverApprovals = "a server's pairings wait for a paired device to approve them; a desktop shows the code on its own screen, where the user types or scans it"
	serverAccounts  = "the server's accounts, managed from its paired devices; a desktop's are managed in its window (Models & accounts)"
)

// RouteDifference is how an engine's routes differ from PhoneRoutes and
// the routes it alone may serve: missing ones it should serve, extra ones
// that are in neither list.
func RouteDifference(routes map[string]http.HandlerFunc, only map[string]string) (missing, extra []string) {
	for _, route := range PhoneRoutes {
		if routes[route] == nil {
			missing = append(missing, route)
		}
	}
	for route := range routes {
		if !slices.Contains(PhoneRoutes, route) && only[route] == "" {
			extra = append(extra, route)
		}
	}
	slices.Sort(extra)
	return missing, extra
}
