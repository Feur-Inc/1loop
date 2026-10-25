package agent

// ChatGPT (Web): the model runs in a ChatGPT conversation of the user's
// subscription, in 1loop's own browser, and calls 1loop's tools through the
// "1loop" connector, an MCP server reached through the user's Secure MCP
// Tunnel. Every message 1loop sends carries a hidden turn id; the model
// passes it with each tool call, so a call runs in the chat, workspace and
// approval mode it belongs to, without the user ever pairing anything.

import (
	"1loop/internal/browser"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"1loop/internal/core"
)

// webTurnField is the argument added to every connector tool.
const webTurnField = "turn"

// WebTurn is one 1loop turn waiting for ChatGPT's tool calls.
type WebTurn struct {
	callMu  sync.Mutex
	running atomic.Int32
	flush   func() error
	ctx     context.Context // carries the run's Config (toolContext)
	root    string
	emit    emitter
	// called runs after each call, in order, so the run records it in the
	// chat's history between the model's messages.
	called func(ToolCall, ToolOutcome)
}

// ChatGPTWeb is the connector's MCP server, alive as long as the tunnel.
type ChatGPTWeb struct {
	backend        *App
	mu             sync.Mutex
	turns          map[string]*WebTurn
	engine         *browser.Engine
	pages          map[string]*browser.Page
	parent         context.Context
	stopTunnel     context.CancelFunc
	tunnelSettings WebSettings
	tunnelError    error
	tunnelReady    bool
	pageStreams    map[*browser.Page]*webStreams
}

func NewChatGPTWeb(backend *App) *ChatGPTWeb {
	return &ChatGPTWeb{backend: backend, turns: map[string]*WebTurn{}}
}

// begin registers a turn under a new id and returns it with its removal.
func (bridge *ChatGPTWeb) begin(turn *WebTurn) (string, func()) {
	id := core.Identifier()[:12]
	bridge.mu.Lock()
	bridge.turns[id] = turn
	bridge.mu.Unlock()
	return id, func() {
		bridge.mu.Lock()
		delete(bridge.turns, id)
		bridge.mu.Unlock()
	}
}

// webToolDefinitions are the connector's tools. ChatGPT reads them when the
// connector is created and keeps them, so the list does not depend on a
// chat: the same tools as the other accounts on the desktop, without the
// phone's. A tool a chat cannot use answers so when called.
func webToolDefinitions() []map[string]any {
	var definitions []map[string]any
	for _, definition := range coreToolDefinitions() {
		if definition["function"].(map[string]any)["name"] != "checkpoint" {
			definitions = append(definitions, definition)
		}
	}
	for _, group := range toolGroups() {
		if group.name != "local_phone" && group.name != "local_debian" {
			definitions = append(definitions, group.tools()...)
		}
	}
	return definitions
}

func webMCPTools() []any {
	tools := []any{}
	for _, definition := range webToolDefinitions() {
		function := definition["function"].(map[string]any)
		schema := map[string]any{}
		for key, value := range function["parameters"].(map[string]any) {
			schema[key] = value
		}
		properties := map[string]any{webTurnField: map[string]any{"type": "string",
			"description": "The turn id written at the end of the user's latest message, copied exactly."}}
		for key, value := range schema["properties"].(map[string]any) {
			properties[key] = value
		}
		schema["properties"] = properties
		required := []any{webTurnField}
		switch list := schema["required"].(type) {
		case []string:
			for _, name := range list {
				required = append(required, name)
			}
		case []any:
			required = append(required, list...)
		}
		schema["required"] = required
		tools = append(tools, map[string]any{"name": function["name"], "description": function["description"], "inputSchema": schema})
	}
	return tools
}

// HandleMCP answers one JSON-RPC message from the tunnel.
func (bridge *ChatGPTWeb) HandleMCP(ctx context.Context, message json.RawMessage, _ http.Header) (json.RawMessage, http.Header, int) {
	var request struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(message, &request); err != nil {
		return rpcError(nil, -32700, "Invalid JSON-RPC message"), nil, http.StatusOK
	}
	if len(request.ID) == 0 { // a notification: acknowledged
		return nil, nil, http.StatusAccepted
	}
	var result any
	switch request.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(request.Params, &params)
		version := params.ProtocolVersion
		if version == "" {
			version = "2025-06-18"
		}
		result = map[string]any{"protocolVersion": version, "capabilities": map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]string{"name": "1loop", "version": "1.0"}}
	case "ping":
		result = map[string]any{}
	case "tools/list":
		result = map[string]any{"tools": webMCPTools()}
	case "tools/call":
		result = bridge.call(ctx, request.Params)
	default:
		return rpcError(request.ID, -32601, "Unknown method"), nil, http.StatusOK
	}
	data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	return data, http.Header{"Content-Type": {"application/json"}}, http.StatusOK
}

func rpcError(id json.RawMessage, code int, text string) json.RawMessage {
	data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": text}})
	return data
}

func toolText(text string, failed bool) map[string]any {
	return map[string]any{"isError": failed, "content": []any{map[string]any{"type": "text", "text": text}}}
}

func (bridge *ChatGPTWeb) call(ctx context.Context, raw json.RawMessage) map[string]any {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return toolText("Invalid tool arguments", true)
	}
	id, _ := params.Arguments[webTurnField].(string)
	bridge.mu.Lock()
	turn := bridge.turns[id]
	bridge.mu.Unlock()
	if turn == nil {
		return toolText("This 1loop turn has ended or its turn id is wrong: copy the turn id from the user's latest message, or answer without tools.", true)
	}
	turn.running.Add(1)
	defer turn.running.Add(-1)
	turn.callMu.Lock()
	defer turn.callMu.Unlock()
	if turn.flush != nil {
		if err := turn.flush(); err != nil {
			return toolText(err.Error(), true)
		}
	}
	delete(params.Arguments, webTurnField)
	arguments, _ := json.Marshal(params.Arguments)
	call := ToolCall{ID: core.Identifier()}
	call.Function.Name, call.Function.Arguments = params.Name, string(arguments)
	// The tunnel's deadline and the turn's end both stop the tool.
	operation, cancel := context.WithCancel(turn.ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	outcome := bridge.backend.executeTool(operation, turn.root, call, turn.emit)
	turn.emit("tool_result", outcome)
	if turn.called != nil {
		turn.called(call, outcome)
	}
	content := []any{map[string]any{"type": "text", "text": outcome.modelText()}}
	for _, part := range outcome.Images {
		part = compactToolImage(part)
		content = append(content, map[string]any{"type": "image", "data": part.Data, "mimeType": part.Mime})
	}
	return map[string]any{"isError": outcome.Err != nil, "content": content}
}

// webTurnNote is appended to each message sent to ChatGPT: the id its tool
// calls carry.
func webTurnNote(id string) string {
	return fmt.Sprintf("\n\n[1loop turn: %s]", id)
}
