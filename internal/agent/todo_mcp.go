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
	"time"

	"1loop/internal/core"
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

// Claude Code keeps its own harness. Its MCP transport exposes the same
// TODO store, memory, browser tabs and terminals used by 1loop's other
// providers.
// The server lives as long as one Claude Code run of 1loop and answers only
// that run (a random token); it returns the path of its --mcp-config file.
func (backend *App) startTodoMCP(ctx context.Context, cfg Config, emit emitter) (string, func(), error) {
	state := cfg.Todo
	browserTools, terminalTools, computerTools := cfg.Browser, cfg.Terminal, cfg.Computer
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	token := core.Identifier()
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(writer http.ResponseWriter, requestValue *http.Request) {
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
			definitions := projectMCPTools(cfg)
			if cfg.ProjectTools == nil && cfg.toolAvailable("update_todo") {
				definitions = append(definitions, todoToolDefinition())
			}
			if cfg.ProjectTools == nil && cfg.toolAvailable("memory") {
				definitions = append(definitions, memoryToolDefinition())
			}
			for _, group := range availableToolGroups(cfg) {
				definitions = append(definitions, group.tools()...)
			}
			tools := []any{}
			for _, definition := range definitions {
				f := definition["function"].(map[string]any)
				tools = append(tools, map[string]any{"name": f["name"], "description": f["description"], "inputSchema": f["parameters"]})
			}
			response["result"] = map[string]any{"tools": tools}
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
				if err != nil {
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
