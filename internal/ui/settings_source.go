// SPDX-License-Identifier: MPL-2.0

package ui

// Where Settings → Extensions and Connectors read and change what they
// show: this computer's, or, while a server's chats are on screen (the
// desktop's remote mode, the browser client), the server's through its API,
// since its agent is the one using them.

import (
	"context"

	"1loop/internal/extensions"
	"1loop/internal/githubconnector"
)

type extensionSource interface {
	search(ctx context.Context, kind, query, order string, page int) (extensions.SearchResult, error)
	readme(ctx context.Context, repo string) (string, error)
	prepare(ctx context.Context, repo string) (*extensions.Plan, error)
	install(ctx context.Context, plan string, keep []string) error
	view(ctx context.Context) (extensions.Installed, error)
	enable(ctx context.Context, kind, id string, on bool) error
	remove(ctx context.Context, kind, id string) error
	saveMCP(ctx context.Context, server extensions.MCPServer, previous string) error
	removeMCP(ctx context.Context, name string) error
	mcpStatus(ctx context.Context, workspace string) ([]extensions.MCPStatus, error)
	// where says where the extensions are, for the page's notes.
	where() string
}

func (ui *desktopUI) extensionSource() extensionSource {
	if ui.server != nil {
		return serverExtensions{ui.server}
	}
	return localExtensions{ui}
}

// localExtensions are this computer's; a change is told to the phone.
type localExtensions struct{ ui *desktopUI }

func (local localExtensions) changed() {
	local.ui.remote.broadcast("extensions", map[string]bool{"changed": true})
}

func (localExtensions) search(ctx context.Context, kind, query, order string, page int) (extensions.SearchResult, error) {
	return extensions.Search(ctx, kind, query, order, page)
}

func (localExtensions) readme(ctx context.Context, repo string) (string, error) {
	return extensions.Readme(ctx, repo)
}

func (localExtensions) prepare(ctx context.Context, repo string) (*extensions.Plan, error) {
	return extensions.Prepare(ctx, repo)
}

func (local localExtensions) install(_ context.Context, plan string, keep []string) error {
	_, err := extensions.Install(plan, keep)
	if err == nil {
		local.changed()
	}
	return err
}

func (localExtensions) view(context.Context) (extensions.Installed, error) { return extensions.View() }

func (local localExtensions) enable(_ context.Context, kind, id string, on bool) error {
	err := extensions.SetEnabled(kind, id, on)
	if kind == "mcp" {
		extensions.Servers.Forget(id)
	}
	local.changed()
	return err
}

func (local localExtensions) remove(_ context.Context, kind, id string) error {
	err := extensions.Remove(kind, id)
	local.changed()
	return err
}

func (local localExtensions) saveMCP(_ context.Context, server extensions.MCPServer, previous string) error {
	if err := extensions.SaveMCP(server, previous); err != nil {
		return err
	}
	extensions.Servers.Forget(server.Name)
	if previous != "" {
		extensions.Servers.Forget(previous)
	}
	local.changed()
	return nil
}

func (local localExtensions) removeMCP(_ context.Context, name string) error {
	extensions.Servers.Forget(name)
	err := extensions.RemoveMCP(name)
	local.changed()
	return err
}

func (localExtensions) mcpStatus(ctx context.Context, workspace string) ([]extensions.MCPStatus, error) {
	return extensions.Servers.Status(ctx, extensions.Enabled().MCP, workspace), nil
}

func (localExtensions) where() string { return "" }

// serverExtensions are the server's (internal/extensions/http.go). Its
// answers hide the MCP servers' environment and header values.
type serverExtensions struct{ remote *remoteDesktop }

func (server serverExtensions) call(ctx context.Context, path string, body, result any) error {
	method := "POST"
	if body == nil {
		method = "GET"
	}
	return server.remote.client.json(ctx, method, path, body, result)
}

func (server serverExtensions) search(ctx context.Context, kind, query, order string, page int) (extensions.SearchResult, error) {
	var result extensions.SearchResult
	err := server.call(ctx, "/api/extensions/search", map[string]any{"kind": kind, "query": query, "sort": order, "page": page}, &result)
	return result, err
}

func (server serverExtensions) readme(ctx context.Context, repo string) (string, error) {
	var result struct{ Readme string }
	err := server.call(ctx, "/api/extensions/readme", map[string]string{"repo": repo}, &result)
	return result.Readme, err
}

func (server serverExtensions) prepare(ctx context.Context, repo string) (*extensions.Plan, error) {
	var plan extensions.Plan
	if err := server.call(ctx, "/api/extensions/prepare", map[string]string{"repo": repo}, &plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

func (server serverExtensions) install(ctx context.Context, plan string, keep []string) error {
	return server.call(ctx, "/api/extensions/install", map[string]any{"plan": plan, "keep": keep}, nil)
}

func (server serverExtensions) view(ctx context.Context) (extensions.Installed, error) {
	var view extensions.Installed
	err := server.call(ctx, "/api/extensions", nil, &view)
	return view, err
}

func (server serverExtensions) enable(ctx context.Context, kind, id string, on bool) error {
	return server.call(ctx, "/api/extensions/enable", map[string]any{"kind": kind, "id": id, "enabled": on}, nil)
}

func (server serverExtensions) remove(ctx context.Context, kind, id string) error {
	return server.call(ctx, "/api/extensions/remove", map[string]string{"kind": kind, "id": id}, nil)
}

func (server serverExtensions) saveMCP(ctx context.Context, mcp extensions.MCPServer, previous string) error {
	return server.call(ctx, "/api/extensions/mcp", map[string]any{"server": mcp, "previous": previous}, nil)
}

func (server serverExtensions) removeMCP(ctx context.Context, name string) error {
	return server.call(ctx, "/api/extensions/mcp/remove", map[string]string{"name": name}, nil)
}

func (server serverExtensions) mcpStatus(ctx context.Context, workspace string) ([]extensions.MCPStatus, error) {
	var result struct{ Servers []extensions.MCPStatus }
	err := server.call(ctx, "/api/extensions/mcp/status", map[string]string{"workspace": workspace}, &result)
	return result.Servers, err
}

func (server serverExtensions) where() string { return "the server " + server.remote.name }

// githubSource is the GitHub connector of this computer or of the server.
type githubSource interface {
	status(ctx context.Context) (githubconnector.Status, error)
	configure(ctx context.Context, clientID string) error
	start(ctx context.Context) error
	cancel(ctx context.Context)
	enable(ctx context.Context, on bool) error
	setAttachmentToken(ctx context.Context, token string) error
	disconnect(ctx context.Context) error
}

func (ui *desktopUI) githubSource() githubSource {
	if ui.server != nil {
		return serverGitHub{ui.server}
	}
	return localGitHub{}
}

type localGitHub struct{}

func (localGitHub) status(context.Context) (githubconnector.Status, error) {
	return githubconnector.Default.Status(), nil
}

func (localGitHub) configure(_ context.Context, clientID string) error {
	return githubconnector.Default.Configure(clientID)
}

func (localGitHub) start(ctx context.Context) error { return githubconnector.Default.Start(ctx) }
func (localGitHub) cancel(context.Context)          { githubconnector.Default.Cancel() }

func (localGitHub) enable(_ context.Context, on bool) error {
	err := githubconnector.Default.Enable(on)
	if !on {
		extensions.Servers.Forget(githubconnector.ServerName)
	}
	return err
}

func (localGitHub) setAttachmentToken(ctx context.Context, token string) error {
	return githubconnector.Default.SetAttachmentToken(ctx, token)
}

func (localGitHub) disconnect(context.Context) error {
	err := githubconnector.Default.Disconnect()
	if err == nil {
		extensions.Servers.Forget(githubconnector.ServerName)
	}
	return err
}

// serverGitHub is the server's (internal/githubconnector/http.go): its
// device sign-in shows the code here, and the token stays on the server.
type serverGitHub struct{ remote *remoteDesktop }

func (server serverGitHub) post(ctx context.Context, path string, body any) error {
	if body == nil {
		body = map[string]any{}
	}
	return server.remote.client.json(ctx, "POST", path, body, nil)
}

func (server serverGitHub) status(ctx context.Context) (githubconnector.Status, error) {
	var state githubconnector.Status
	err := server.remote.client.json(ctx, "GET", "/api/github", nil, &state)
	return state, err
}

func (server serverGitHub) configure(ctx context.Context, clientID string) error {
	return server.post(ctx, "/api/github/configure", map[string]string{"clientId": clientID})
}

func (server serverGitHub) start(ctx context.Context) error {
	return server.post(ctx, "/api/github/connect", nil)
}

func (server serverGitHub) cancel(ctx context.Context) { server.post(ctx, "/api/github/cancel", nil) }

func (server serverGitHub) enable(ctx context.Context, on bool) error {
	return server.post(ctx, "/api/github/enable", map[string]bool{"enabled": on})
}

func (server serverGitHub) setAttachmentToken(ctx context.Context, token string) error {
	return server.post(ctx, "/api/github/attachment-token", map[string]string{"token": token})
}

func (server serverGitHub) disconnect(ctx context.Context) error {
	return server.post(ctx, "/api/github/disconnect", nil)
}
