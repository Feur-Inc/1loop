// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"1loop/internal/core"
	"1loop/internal/githubconnector"
)

func projectMCPTools(cfg Config) []map[string]any {
	var definitions []map[string]any
	if cfg.ProjectTools != nil {
		for _, definition := range availableCoreTools(cfg) {
			if definition["function"].(map[string]any)["name"] != "checkpoint" {
				definitions = append(definitions, definition)
			}
		}
	}
	return definitions
}

func hasTool(definitions []map[string]any, name string) bool {
	for _, definition := range definitions {
		if definition["function"].(map[string]any)["name"] == name {
			return true
		}
	}
	return false
}

// Claude Code keeps its own harness. Its MCP transport exposes the same
// TODO store, memory, browser tabs and terminals used by 1loop's other
// providers.
// The server lives as long as one Claude Code run of 1loop and answers only
// that run (a random token); it returns the path of its --mcp-config file.
// todoMCPBinding is the turn a Claude Code process's MCP server works for:
// a process kept between turns (claudeProcess) serves each turn in its own
// context, with that turn's configuration and events.
type todoMCPBinding struct {
	mu     sync.Mutex
	ctx    context.Context
	cfg    Config
	emit   emitter
	github string // the GitHub connection of the turn
}

func newTodoMCPBinding(ctx context.Context, cfg Config, emit emitter) *todoMCPBinding {
	binding := &todoMCPBinding{}
	binding.set(ctx, cfg, emit)
	return binding
}

func (binding *todoMCPBinding) set(ctx context.Context, cfg Config, emit emitter) {
	binding.mu.Lock()
	defer binding.mu.Unlock()
	binding.ctx, binding.cfg, binding.emit, binding.github = ctx, cfg, emit, githubconnector.Default.ConnectionID()
}

func (binding *todoMCPBinding) get() (context.Context, Config, emitter, string) {
	binding.mu.Lock()
	defer binding.mu.Unlock()
	return binding.ctx, binding.cfg, binding.emit, binding.github
}

func (backend *App) startTodoMCP(binding *todoMCPBinding) (string, func(), error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	token := core.Identifier()
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(writer http.ResponseWriter, requestValue *http.Request) {
		ctx, cfg, emit, githubConnection := binding.get()
		state := cfg.Todo
		browserTools, terminalTools, computerTools := modelBrowser(cfg.Browser), cfg.Terminal, cfg.Computer
		if requestValue.Header.Get("Authorization") != "Bearer "+token {
			http.Error(writer, "Unauthorized", http.StatusUnauthorized)
			return
		}
		if requestValue.Method != http.MethodPost {
			writer.Header().Set("Allow", "POST")
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(io.LimitReader(requestValue.Body, 1<<20)).Decode(&request); err != nil {
			http.Error(writer, "Invalid JSON-RPC request", http.StatusBadRequest)
			return
		}
		if len(request.ID) == 0 {
			writer.WriteHeader(http.StatusAccepted)
			return
		}
		response := map[string]any{"jsonrpc": "2.0", "id": request.ID}
		switch request.Method {
		case "initialize":
			response["result"] = map[string]any{"protocolVersion": "2025-03-26",
				"serverInfo": map[string]string{"name": "1loop-todo", "version": "1.0"}, "capabilities": map[string]any{"tools": map[string]any{}}}
		case "ping":
			response["result"] = map[string]any{}
		case "tools/list":
			response["result"] = map[string]any{"tools": claudeMCPToolList(claudeMCPWithRules(cfg, claudeMCPDefinitions(cfg)))}
		case "tools/call":
			var call struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if err := json.Unmarshal(request.Params, &call); err != nil {
				response["error"] = map[string]any{"code": -32602, "message": "Invalid tool arguments"}
				break
			}
			if ctx.Err() != nil {
				response["error"] = map[string]any{"code": -32603, "message": "This run has ended"}
				break
			}
			if !cfg.toolAvailable(call.Name) {
				response["error"] = map[string]any{"code": -32602, "message": "This tool is unavailable or its permission was revoked"}
				break
			}
			if call.Name == "memory" {
				// Claude Code lets the call through: it asks the user itself,
				// with the change as a diff.
				operation, cancel := context.WithCancel(ctx)
				stop := context.AfterFunc(requestValue.Context(), cancel)
				text, _, err := backend.editMemory(operation, string(call.Arguments), func() {}, emit)
				stop()
				cancel()
				if err != nil {
					text = err.Error()
				}
				response["result"] = map[string]any{"isError": err != nil, "content": []any{map[string]string{"type": "text", "text": text}}}
				break
			}
			if call.Name == "settings" || call.Name == "subchat" || call.Name == "chats" {
				// Like memory: let through by Claude Code, asked here.
				operation, cancel := context.WithCancel(ctx)
				stop := context.AfterFunc(requestValue.Context(), cancel)
				answer := backend.changeSettings
				if call.Name == "subchat" {
					answer = backend.subchatTool
				} else if call.Name == "chats" {
					answer = backend.chatsTool
				}
				text, err := answer(context.WithValue(operation, configContextKey{}, cfg), string(call.Arguments), func() {}, emit)
				stop()
				cancel()
				if err != nil {
					text = err.Error()
				}
				response["result"] = map[string]any{"isError": err != nil, "content": []any{map[string]string{"type": "text", "text": text}}}
				break
			}
			if call.Name == "wrap_up" {
				text, err := wrapUp(state, string(call.Arguments), emit)
				if err != nil {
					text = err.Error()
				}
				response["result"] = map[string]any{"isError": err != nil, "content": []any{map[string]string{"type": "text", "text": text}}}
				break
			}
			if call.Name != "update_todo" {
				var handler PanelToolHandler
				switch {
				case strings.HasPrefix(call.Name, "browser_"):
					handler = browserTools
				case strings.HasPrefix(call.Name, "terminal_"):
					handler = terminalTools
				case call.Name == "computer_use":
					handler = computerTools
				case isPhoneTool(call.Name):
					handler = cfg.Phone
				case call.Name == "install_packages":
					handler = cfg.Packages
				case call.Name == "change_directory" || call.Name == "request_directory":
					handler = func(operation context.Context, name string, arguments json.RawMessage) (PanelToolResult, error) {
						call := ToolCall{ID: core.Identifier()}
						call.Function.Name, call.Function.Arguments = name, string(arguments)
						result := backend.executeTool(operation, cfg.Workspace, call, func(event string, value any) {
							if event != "tool" && event != "tool_exec" && event != "tool_output" {
								emit(event, value)
							}
						})
						return PanelToolResult{Text: result.Output}, result.Err
					}
				case isGitHubTool(call.Name):
					handler = func(operation context.Context, name string, arguments json.RawMessage) (PanelToolResult, error) {
						// Keep Claude's approved call bound to the connection of this run.
						operation = context.WithValue(operation, approvalContextKey{}, approvalPolicy{yolo: true})
						operation = context.WithValue(operation, githubConnectionKey{}, githubConnection)
						call := ToolCall{ID: core.Identifier()}
						call.Function.Name, call.Function.Arguments = name, string(arguments)
						result := backend.executeTool(operation, cfg.Workspace, call, func(event string, value any) {
							if event != "tool" && event != "tool_exec" && event != "tool_output" {
								emit(event, value)
							}
						})
						return PanelToolResult{Text: result.Output, Images: result.Images}, result.Err
					}
				default:
					for _, definition := range projectMCPTools(cfg) {
						if definition["function"].(map[string]any)["name"] == call.Name {
							handler = cfg.ProjectTools
							break
						}
					}
				}
				if handler == nil {
					response["error"] = map[string]any{"code": -32602, "message": "Unknown 1loop tool"}
					break
				}
				operation, cancel := context.WithCancel(ctx)
				stop := context.AfterFunc(requestValue.Context(), cancel)
				result, err := handler(operation, call.Name, call.Arguments)
				stop()
				cancel()
				content := []any{map[string]any{"type": "text", "text": result.Text}}
				structuredCommand := completeResult(call.Name, result.Text)
				if err != nil && !structuredCommand {
					content = []any{map[string]any{"type": "text", "text": err.Error()}}
				}
				for _, imagePart := range result.Images {
					imagePart = compactToolImage(imagePart)
					content = append(content, map[string]any{"type": "image", "data": imagePart.Data, "mimeType": imagePart.Mime})
				}
				response["result"] = map[string]any{"isError": err != nil, "content": content}
				break
			}
			items, err := state.update(call.Arguments)
			data, _ := json.Marshal(items)
			text := string(data)
			if err != nil {
				text = err.Error()
			} else {
				emit("todo", nil)
			}
			response["result"] = map[string]any{"isError": err != nil, "content": []any{map[string]string{"type": "text", "text": text}}}
		default:
			response["error"] = map[string]any{"code": -32601, "message": "Unknown method"}
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(response)
	})}
	go func() { _ = server.Serve(listener) }()
	config, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"1loop_todo": map[string]any{
		"type": "http", "url": "http://" + listener.Addr().String() + "/mcp", "headers": map[string]string{"Authorization": "Bearer " + token},
	}}})
	// The token reaches Claude Code in a file only its user can read, not on
	// its command line (which every process of the user can read): only this
	// Claude Code session can use the server. Never in the user's Claude
	// settings, so the claude they run themselves does not see it.
	dir, err := os.MkdirTemp("", "1loop-mcp-")
	if err != nil {
		_ = server.Close()
		return "", nil, err
	}
	path := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(path, config, 0600); err != nil {
		_ = server.Close()
		_ = os.RemoveAll(dir)
		return "", nil, err
	}
	return path, func() { _ = server.Close(); _ = os.RemoveAll(dir) }, nil
}

// claudeMCPDefinitions are the tools 1loop's MCP server gives Claude Code.
func claudeMCPDefinitions(cfg Config) []map[string]any {
	definitions := projectMCPTools(cfg)
	if cfg.ProjectTools == nil && cfg.toolAvailable("update_todo") {
		definitions = append(definitions, todoToolDefinition())
	}
	if cfg.toolAvailable("wrap_up") && !hasTool(definitions, "wrap_up") {
		definitions = append(definitions, wrapUpToolDefinition())
	}
	if cfg.ProjectTools == nil && cfg.toolAvailable("memory") {
		definitions = append(definitions, memoryToolDefinition())
	}
	// Moving a chat without a folder; 1loop asks the user itself.
	if cfg.ProjectTools == nil && cfg.toolAvailable("change_directory") {
		definitions = append(definitions, changeDirectoryToolDefinition())
	}
	if cfg.ProjectTools == nil && cfg.toolAvailable("request_directory") {
		definitions = append(definitions, requestDirectoryToolDefinition())
	}
	if cfg.ProjectTools == nil && cfg.toolAvailable("settings") {
		definitions = append(definitions, settingsToolDefinition())
	}
	if cfg.ProjectTools == nil && cfg.toolAvailable("subchat") {
		definitions = append(definitions, subchatToolDefinition())
	}
	if cfg.ProjectTools == nil && cfg.toolAvailable("chats") {
		definitions = append(definitions, chatsToolDefinition())
	}
	for _, group := range builtinToolGroups(cfg) {
		definitions = append(definitions, group.tools()...)
	}
	return definitions
}

// claudeMCPWithRules adds to each deferred tool's description the rules
// 1loop's instructions give for it, which other providers read in their
// prompt or with tool_search: under Claude Code they arrive only when the
// tool is loaded, instead of in every request.
func claudeMCPWithRules(cfg Config, definitions []map[string]any) []map[string]any {
	rules := map[string]string{
		"memory":   memoryInstructions(cfg),
		"settings": settingsInstructions(cfg),
		"subchat":  subchatStartInstructions(cfg),
		"chats":    chatsInstructions(cfg),
	}
	for _, group := range builtinToolGroups(cfg) {
		text := group.instructions(cfg)
		for _, definition := range group.tools() {
			rules[definition["function"].(map[string]any)["name"].(string)] += text
		}
	}
	result := make([]map[string]any, 0, len(definitions))
	for _, definition := range definitions {
		function := definition["function"].(map[string]any)
		rule := strings.TrimSpace(rules[function["name"].(string)])
		if rule == "" {
			result = append(result, definition)
			continue
		}
		copied := make(map[string]any, len(function))
		for key, value := range function {
			copied[key] = value
		}
		copied["description"] = function["description"].(string) + "\n\n" + rule
		result = append(result, map[string]any{"type": definition["type"], "function": copied})
	}
	return result
}

// claudeMCPToolList is the tools/list answer for definitions.
func claudeMCPToolList(definitions []map[string]any) []any {
	tools := []any{}
	for _, definition := range definitions {
		f := definition["function"].(map[string]any)
		tool := map[string]any{"name": f["name"], "description": f["description"], "inputSchema": f["parameters"]}
		if f["name"] == "wrap_up" || f["name"] == "update_todo" {
			// Called on most turns: in the prompt, not deferred
			// behind Claude Code's tool search (a round trip the
			// model tends to skip, and the call with it).
			tool["_meta"] = map[string]any{"anthropic/alwaysLoad": true}
		}
		tools = append(tools, tool)
	}
	return tools
}
