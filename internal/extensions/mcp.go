// SPDX-License-Identifier: MPL-2.0

package extensions

// A Model Context Protocol client: 1loop connects to the user's MCP servers,
// lists their tools and calls them for the agent. The protocol itself is the
// official Go SDK (github.com/modelcontextprotocol/go-sdk); this file maps
// 1loop's server settings onto its two transports (a command 1loop starts,
// spoken to on stdin/stdout, and Streamable HTTP) and its results back onto
// the small types the agent uses.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

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

// errSignIn is what a server answering HTTP 401 gives. agent.githubUnauthorized
// looks for this text to refresh the GitHub connection's token.
var errSignIn = errors.New("The server asks to sign in (HTTP 401): add its token as a header")

// MCPClient is a connection to one server.
type MCPClient struct {
	session *mcp.ClientSession
	stderr  *tailBuffer // a command's last words, for the error when it stops
	mu      sync.Mutex
	err     error
}

// ConnectMCP starts or reaches a server and initializes the session. dir
// is where a command runs (the project).
func ConnectMCP(ctx context.Context, server MCPServer, dir string) (*MCPClient, error) {
	client := &MCPClient{}
	var transport mcp.Transport
	switch server.Type {
	case "stdio":
		cmd := exec.Command(server.Command, server.Args...)
		cmd.Dir = dir
		cmd.Env = os.Environ()
		for key, value := range server.Env {
			cmd.Env = append(cmd.Env, key+"="+os.ExpandEnv(value))
		}
		client.stderr = &tailBuffer{limit: 4096}
		cmd.Stderr = client.stderr
		transport = &mcp.CommandTransport{Command: cmd, TerminateDuration: 2 * time.Second}
	case "http":
		transport = &mcp.StreamableClientTransport{
			Endpoint:   server.URL,
			HTTPClient: &http.Client{Transport: headerTransport{headers: server.Headers, base: http.DefaultTransport}},
		}
	default:
		return nil, fmt.Errorf("Unknown MCP transport %q", server.Type)
	}
	sdk := mcp.NewClient(&mcp.Implementation{Name: "1loop", Version: "1"}, &mcp.ClientOptions{
		// No roots, sampling or elicitation: 1loop only uses the tools.
		Capabilities: &mcp.ClientCapabilities{},
	})
	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	session, err := sdk.Connect(initCtx, transport, nil)
	if err != nil {
		return nil, client.explain(err)
	}
	client.session = session
	go func() {
		err := session.Wait()
		if err == nil {
			err = errors.New("the server stopped")
		}
		client.fail(client.explain(err))
	}()
	return client, nil
}

// explain adds what a command last wrote on stderr to an error.
func (client *MCPClient) explain(err error) error {
	if client.stderr == nil {
		return err
	}
	if tail := lastLine(client.stderr.String()); tail != "" {
		return fmt.Errorf("%w: %s", err, tail)
	}
	return err
}

func (client *MCPClient) fail(err error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.err == nil {
		client.err = err
	}
}

// Err is why the connection ended, nil while it works.
func (client *MCPClient) Err() error {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.err
}

// Tools lists the server's tools, every page (at most 20).
func (client *MCPClient) Tools(ctx context.Context) ([]MCPTool, error) {
	var tools []MCPTool
	params := &mcp.ListToolsParams{}
	for page := 0; page < 20; page++ {
		result, err := client.session.ListTools(ctx, params)
		if err != nil {
			return nil, client.explain(err)
		}
		for _, tool := range result.Tools {
			if tool == nil {
				continue
			}
			item := MCPTool{Name: tool.Name, Description: tool.Description}
			if tool.InputSchema != nil {
				item.InputSchema, _ = json.Marshal(tool.InputSchema)
			}
			if tool.Annotations != nil {
				item.Annotations.ReadOnly = tool.Annotations.ReadOnlyHint
			}
			tools = append(tools, item)
		}
		if result.NextCursor == "" {
			break
		}
		params.Cursor = result.NextCursor
	}
	return tools, nil
}

// CallTool runs one of the server's tools.
func (client *MCPClient) CallTool(ctx context.Context, name string, arguments json.RawMessage) (MCPResult, error) {
	var args any = map[string]any{}
	if len(arguments) > 0 && json.Valid(arguments) {
		args = arguments
	}
	result, err := client.session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return MCPResult{}, client.explain(err)
	}
	out := MCPResult{IsError: result.IsError}
	for _, content := range result.Content {
		// The SDK's content marshals to the protocol's JSON, which MCPContent reads.
		data, err := json.Marshal(content)
		if err != nil {
			continue
		}
		var part MCPContent
		if json.Unmarshal(data, &part) == nil {
			out.Content = append(out.Content, part)
		}
	}
	return out, nil
}

// Close ends the session: a command is asked to stop, then stopped.
func (client *MCPClient) Close() {
	client.fail(errors.New("closed"))
	if client.session != nil {
		client.session.Close()
	}
}

// headerTransport adds a server's headers (environment variables expanded)
// to every request, and turns a 401 into errSignIn.
type headerTransport struct {
	headers map[string]string
	base    http.RoundTripper
}

func (transport headerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if len(transport.headers) > 0 {
		request = request.Clone(request.Context())
		for key, value := range transport.headers {
			request.Header.Set(key, os.ExpandEnv(value))
		}
	}
	response, err := transport.base.RoundTrip(request)
	if err == nil && response.StatusCode == http.StatusUnauthorized {
		response.Body.Close()
		return nil, errSignIn
	}
	return response, err
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
