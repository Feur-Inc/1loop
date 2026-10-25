package agent

// ChatGPT (Web): the model runs in a ChatGPT conversation of the user's
// subscription, in 1loop's own browser, and calls 1loop's tools through the
// "1loop" connector, an MCP server reached through the user's Secure MCP
// Tunnel. ChatGPT sends each tool call with its conversation's id
// (_meta "openai/session"): the first call of a new ChatGPT chat binds that
// id to the 1loop turn waiting for one (only one waits at a time), so every
// call runs in the chat, workspace and approval mode it belongs to, without
// the user pairing anything or a token in the model's arguments (which
// ChatGPT's own review flags as suspicious).

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"

	"1loop/internal/browser"
	"1loop/internal/core"
)

// WebTurn is one 1loop turn waiting for ChatGPT's tool calls.
type WebTurn struct {
	session string // ChatGPT's id of the conversation, once known
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
	turns          map[*WebTurn]bool
	unbound        chan struct{} // held by the turn whose session is not known yet
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
	return &ChatGPTWeb{backend: backend, turns: map[*WebTurn]bool{}, unbound: make(chan struct{}, 1)}
}

// begin registers a turn and returns its removal. A turn of a chat whose
// session is known is found by it; otherwise it waits until no other turn
// waits for its session, so the next unknown session is its own.
func (bridge *ChatGPTWeb) begin(ctx context.Context, turn *WebTurn) (func(), error) {
	if turn.session == "" {
		select {
		case bridge.unbound <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	bridge.mu.Lock()
	bridge.turns[turn] = true
	bridge.mu.Unlock()
	return func() {
		bridge.mu.Lock()
		delete(bridge.turns, turn)
		bound := turn.session != ""
		bridge.mu.Unlock()
		if !bound {
			<-bridge.unbound
		}
	}, nil
}

// turnFor finds the turn of a call from its session, binding the waiting
// turn to an unknown one.
func (bridge *ChatGPTWeb) turnFor(session string) *WebTurn {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	var waiting *WebTurn
	for turn := range bridge.turns {
		if session != "" && turn.session == session {
			return turn
		}
		if turn.session == "" {
			waiting = turn
		}
	}
	if waiting == nil || session == "" {
		return nil
	}
	waiting.session = session
	<-bridge.unbound // the next new chat can send
	return waiting
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
		tools = append(tools, map[string]any{"name": function["name"], "description": function["description"], "inputSchema": function["parameters"]})
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
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		Meta      struct {
			Session string `json:"openai/session"`
		} `json:"_meta"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return toolText("Invalid tool arguments", true)
	}
	turn := bridge.turnFor(params.Meta.Session)
	if turn == nil {
		return toolText("No 1loop turn is waiting for this chat: send the message from 1loop (and run one 1loop per tunnel).", true)
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
	arguments := string(params.Arguments)
	if arguments == "" || arguments == "null" {
		arguments = "{}"
	}
	call := ToolCall{ID: core.Identifier()}
	call.Function.Name, call.Function.Arguments = params.Name, arguments
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
