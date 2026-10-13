package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"time"
)

// Claude Code keeps its own harness. Expose the same TODO store through
// its MCP transport rather than maintaining a second native task list.
func startTodoMCP(ctx context.Context, state *todoState, emit emitter) (string, func(), error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	token := identifier()
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&request); err != nil {
			http.Error(w, "Invalid JSON-RPC request", http.StatusBadRequest)
			return
		}
		if len(request.ID) == 0 {
			w.WriteHeader(http.StatusAccepted)
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
			f := todoToolDefinition()["function"].(map[string]any)
			response["result"] = map[string]any{"tools": []any{map[string]any{"name": f["name"], "description": f["description"], "inputSchema": f["parameters"]}}}
		case "tools/call":
			var call struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if err := json.Unmarshal(request.Params, &call); err != nil || call.Name != "update_todo" {
				response["error"] = map[string]any{"code": -32602, "message": "Expected update_todo arguments"}
				break
			}
			if ctx.Err() != nil {
				response["error"] = map[string]any{"code": -32603, "message": "This run has ended"}
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
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	})}
	go func() { _ = server.Serve(listener) }()
	config, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"1loop_todo": map[string]any{
		"type": "http", "url": "http://" + listener.Addr().String() + "/mcp", "headers": map[string]string{"Authorization": "Bearer " + token},
	}}})
	return string(config), func() { _ = server.Close() }, nil
}
