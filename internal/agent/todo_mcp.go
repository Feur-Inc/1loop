package agent

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"1loop/internal/core"
)

// Claude Code keeps its own harness. Its MCP transport exposes the same
// TODO store, browser tabs and terminals used by 1loop's other providers.
func startTodoMCP(ctx context.Context, state *TodoState, emit emitter, handlers ...PanelToolHandler) (string, func(), error) {
	var browserTools, terminalTools, computerTools PanelToolHandler
	if len(handlers) > 0 {
		browserTools = handlers[0]
	}
	if len(handlers) > 1 {
		terminalTools = handlers[1]
	}
	if len(handlers) > 2 {
		computerTools = handlers[2]
	}
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
			definitions := []map[string]any{todoToolDefinition()}
			if browserTools != nil {
				definitions = append(definitions, browserToolDefinitions()...)
			}
			if terminalTools != nil {
				definitions = append(definitions, terminalToolDefinitions()...)
			}
			if computerTools != nil {
				definitions = append(definitions, computerToolDefinition())
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
			if call.Name != "update_todo" {
				handler := browserTools
				if strings.HasPrefix(call.Name, "terminal_") {
					handler = terminalTools
				}
				if call.Name == "computer_use" {
					handler = computerTools
				}
				if handler == nil || (!strings.HasPrefix(call.Name, "browser_") && !strings.HasPrefix(call.Name, "terminal_") && call.Name != "computer_use") {
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
	return string(config), func() { _ = server.Close() }, nil
}
