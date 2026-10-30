package extensions

// A Model Context Protocol client: 1loop connects to the user's MCP servers,
// lists their tools and calls them for the agent. Two transports: a command
// 1loop starts and talks to on stdin/stdout (newline-delimited JSON-RPC),
// and Streamable HTTP (each message POSTed; the answer as JSON or as an
// event stream).

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const mcpProtocol = "2025-06-18"

// MCPTool is a tool a server offers.
type MCPTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
	Annotations struct {
		ReadOnly bool `json:"readOnlyHint,omitempty"`
	} `json:"annotations,omitempty"`
}

// MCPContent is a part of a tool's result.
type MCPContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"` // base64 (images)
	MimeType string `json:"mimeType,omitempty"`
	Resource *struct {
		URI  string `json:"uri"`
		Text string `json:"text"`
	} `json:"resource,omitempty"`
}

// MCPResult is a tool call's result.
type MCPResult struct {
	Content []MCPContent `json:"content"`
	IsError bool         `json:"isError"`
}

// MCPClient is a connection to one server.
type MCPClient struct {
	server  MCPServer
	mu      sync.Mutex
	nextID  int
	pending map[int]chan rpcReply
	closed  chan struct{}
	err     error
	// stdio
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr *tailBuffer
	// http
	http    *http.Client
	session string
	Info    struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	Instructions string
}

type rpcReply struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// ConnectMCP starts or reaches a server and initializes the session. dir
// is where a command runs (the project).
func ConnectMCP(ctx context.Context, server MCPServer, dir string) (*MCPClient, error) {
	client := &MCPClient{server: server, pending: map[int]chan rpcReply{}, closed: make(chan struct{})}
	switch server.Type {
	case "stdio":
		if err := client.start(dir); err != nil {
			return nil, err
		}
	case "http":
		client.http = &http.Client{Timeout: 0}
	default:
		return nil, fmt.Errorf("Unknown MCP transport %q", server.Type)
	}
	var init struct {
		ServerInfo   json.RawMessage `json:"serverInfo"`
		Instructions string          `json:"instructions"`
	}
	params := map[string]any{
		"protocolVersion": mcpProtocol,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "1loop", "version": "1"},
	}
	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := client.call(initCtx, "initialize", params, &init); err != nil {
		client.Close()
		return nil, err
	}
	json.Unmarshal(init.ServerInfo, &client.Info)
	client.Instructions = init.Instructions
	client.notify(ctx, "notifications/initialized", map[string]any{})
	return client, nil
}

func (client *MCPClient) start(dir string) error {
	cmd := exec.Command(client.server.Command, client.server.Args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	for key, value := range client.server.Env {
		cmd.Env = append(cmd.Env, key+"="+os.ExpandEnv(value))
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	client.stderr = &tailBuffer{limit: 4096}
	cmd.Stderr = client.stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s: %w", client.server.Command, err)
	}
	client.cmd, client.stdin = cmd, stdin
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), 64<<20)
		for scanner.Scan() {
			client.dispatch(scanner.Bytes())
		}
		err := cmd.Wait()
		if err == nil {
			err = errors.New("the server stopped")
		}
		if tail := strings.TrimSpace(client.stderr.String()); tail != "" {
			err = fmt.Errorf("%w: %s", err, lastLine(tail))
		}
		client.fail(err)
	}()
	return nil
}

// dispatch hands a message to the call waiting for it; requests from the
// server (pings) get an empty answer.
func (client *MCPClient) dispatch(line []byte) {
	var message struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		rpcReply
	}
	if json.Unmarshal(line, &message) != nil {
		return
	}
	if message.Method != "" {
		if len(message.ID) > 0 && string(message.ID) != "null" {
			client.write(map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": map[string]any{}})
		}
		return
	}
	var id int
	if json.Unmarshal(message.ID, &id) != nil {
		return
	}
	client.mu.Lock()
	wait := client.pending[id]
	delete(client.pending, id)
	client.mu.Unlock()
	if wait != nil {
		wait <- message.rpcReply
	}
}

func (client *MCPClient) fail(err error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.err == nil {
		client.err = err
		close(client.closed)
	}
}

func (client *MCPClient) write(message any) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	_, err = client.stdin.Write(append(data, '\n'))
	return err
}

// Err is why the connection ended, nil while it works.
func (client *MCPClient) Err() error {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.err
}

func (client *MCPClient) call(ctx context.Context, method string, params any, out any) error {
	if err := client.Err(); err != nil {
		return err
	}
	client.mu.Lock()
	client.nextID++
	id := client.nextID
	client.mu.Unlock()
	message := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	var reply rpcReply
	if client.http != nil {
		var err error
		reply, err = client.post(ctx, message, id)
		if err != nil {
			return err
		}
	} else {
		wait := make(chan rpcReply, 1)
		client.mu.Lock()
		client.pending[id] = wait
		client.mu.Unlock()
		if err := client.write(message); err != nil {
			return err
		}
		select {
		case reply = <-wait:
		case <-client.closed:
			return client.Err()
		case <-ctx.Done():
			client.mu.Lock()
			delete(client.pending, id)
			client.mu.Unlock()
			return ctx.Err()
		}
	}
	if reply.Error != nil {
		return fmt.Errorf("%s: %s", method, reply.Error.Message)
	}
	if out != nil && len(reply.Result) > 0 {
		return json.Unmarshal(reply.Result, out)
	}
	return nil
}

func (client *MCPClient) notify(ctx context.Context, method string, params any) {
	message := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	if client.http != nil {
		client.post(ctx, message, 0)
		return
	}
	client.write(message)
}

// post sends a message over Streamable HTTP; the answer to id comes as JSON
// or in an event stream.
func (client *MCPClient) post(ctx context.Context, message any, id int) (rpcReply, error) {
	var reply rpcReply
	data, _ := json.Marshal(message)
	request, err := http.NewRequestWithContext(ctx, "POST", client.server.URL, bytes.NewReader(data))
	if err != nil {
		return reply, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", mcpProtocol)
	for key, value := range client.server.Headers {
		request.Header.Set(key, os.ExpandEnv(value))
	}
	client.mu.Lock()
	if client.session != "" {
		request.Header.Set("Mcp-Session-Id", client.session)
	}
	client.mu.Unlock()
	response, err := client.http.Do(request)
	if err != nil {
		return reply, err
	}
	defer response.Body.Close()
	if session := response.Header.Get("Mcp-Session-Id"); session != "" {
		client.mu.Lock()
		client.session = session
		client.mu.Unlock()
	}
	if id == 0 { // a notification: 202 Accepted
		io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		return reply, nil
	}
	if response.StatusCode == http.StatusUnauthorized {
		return reply, fmt.Errorf("The server asks to sign in (HTTP 401): add its token as a header")
	}
	if response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return reply, fmt.Errorf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(lastLine(string(body))))
	}
	if strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 64*1024), 64<<20)
		var event []string
		for scanner.Scan() {
			line := scanner.Text()
			if line != "" {
				if strings.HasPrefix(line, "data:") {
					event = append(event, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
				}
				continue
			}
			var message struct {
				ID json.RawMessage `json:"id"`
				rpcReply
			}
			if json.Unmarshal([]byte(strings.Join(event, "\n")), &message) == nil && string(message.ID) == fmt.Sprint(id) {
				return message.rpcReply, nil
			}
			event = nil
		}
		return reply, fmt.Errorf("The server's stream ended without an answer")
	}
	err = json.NewDecoder(io.LimitReader(response.Body, 64<<20)).Decode(&reply)
	return reply, err
}

// Tools lists the server's tools, every page.
func (client *MCPClient) Tools(ctx context.Context) ([]MCPTool, error) {
	var tools []MCPTool
	cursor := ""
	for page := 0; page < 20; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var result struct {
			Tools      []MCPTool `json:"tools"`
			NextCursor string    `json:"nextCursor"`
		}
		if err := client.call(ctx, "tools/list", params, &result); err != nil {
			return nil, err
		}
		tools = append(tools, result.Tools...)
		if result.NextCursor == "" {
			break
		}
		cursor = result.NextCursor
	}
	return tools, nil
}

// CallTool runs one of the server's tools.
func (client *MCPClient) CallTool(ctx context.Context, name string, arguments json.RawMessage) (MCPResult, error) {
	var args any = map[string]any{}
	if len(arguments) > 0 {
		json.Unmarshal(arguments, &args)
	}
	var result MCPResult
	err := client.call(ctx, "tools/call", map[string]any{"name": name, "arguments": args}, &result)
	return result, err
}

// Close ends the session: a command is stopped.
func (client *MCPClient) Close() {
	client.fail(errors.New("closed"))
	if client.cmd != nil && client.cmd.Process != nil {
		client.stdin.Close()
		done := make(chan struct{})
		go func() { client.cmd.Process.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			client.cmd.Process.Kill()
		}
	}
}

// tailBuffer keeps the end of what a server writes on stderr.
type tailBuffer struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

func (buffer *tailBuffer) Write(p []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	buffer.data = append(buffer.data, p...)
	if len(buffer.data) > buffer.limit {
		buffer.data = buffer.data[len(buffer.data)-buffer.limit:]
	}
	return len(p), nil
}

func (buffer *tailBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return string(buffer.data)
}

func lastLine(text string) string {
	text = strings.TrimSpace(text)
	if at := strings.LastIndex(text, "\n"); at >= 0 {
		return strings.TrimSpace(text[at+1:])
	}
	return text
}
