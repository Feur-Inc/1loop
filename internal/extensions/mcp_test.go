// SPDX-License-Identifier: MPL-2.0

package extensions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestMain runs the test binary as an MCP server on stdin/stdout when
// asked to, so the stdio transport is tested against a real server.
func TestMain(m *testing.M) {
	if os.Getenv("ONELOOP_TEST_MCP_SERVER") == "1" {
		if err := testServer().Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func testServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "echo",
		Description: "Says it back",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args struct{ Text string }
		json.Unmarshal(request.Params.Arguments, &args)
		return &mcp.CallToolResult{Content: []mcp.Content{
			&mcp.TextContent{Text: "echo: " + args.Text},
			&mcp.ImageContent{Data: []byte{0x89, 'P', 'N', 'G'}, MIMEType: "image/png"},
			&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "file:///a.txt", Text: "a"}},
		}}, nil
	})
	server.AddTool(&mcp.Tool{Name: "fail", InputSchema: json.RawMessage(`{"type":"object"}`)},
		func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "no"}}}, nil
		})
	return server
}

func checkClient(t *testing.T, client *MCPClient) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	tools, err := client.Tools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 || tools[0].Name != "echo" || !tools[0].Annotations.ReadOnly || tools[0].Description != "Says it back" {
		t.Fatalf("tools = %+v", tools)
	}
	if !strings.Contains(string(tools[0].InputSchema), `"text"`) {
		t.Errorf("schema = %s", tools[0].InputSchema)
	}
	result, err := client.CallTool(ctx, "echo", json.RawMessage(`{"text":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) != 3 || result.IsError {
		t.Fatalf("result = %+v", result)
	}
	if got := result.Content[0]; got.Type != "text" || got.Text != "echo: hi" {
		t.Errorf("text = %+v", got)
	}
	if got := result.Content[1]; got.Type != "image" || got.Data != "iVBORw==" || got.MimeType != "image/png" {
		t.Errorf("image = %+v", got)
	}
	if got := result.Content[2]; got.Type != "resource" || got.Resource == nil || got.Resource.URI != "file:///a.txt" || got.Resource.Text != "a" {
		t.Errorf("resource = %+v", got)
	}
	result, err = client.CallTool(ctx, "fail", nil)
	if err != nil || !result.IsError {
		t.Errorf("fail = %+v, %v", result, err)
	}
	if err := client.Err(); err != nil {
		t.Errorf("Err() = %v", err)
	}
}

func TestMCPStdio(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	server := MCPServer{Name: "test", Type: "stdio", Command: exe, Env: map[string]string{"ONELOOP_TEST_MCP_SERVER": "1"}}
	client, err := ConnectMCP(context.Background(), server, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	checkClient(t, client)
	client.Close()
	if client.Err() == nil {
		t.Error("Err() is nil after Close")
	}
}

func TestMCPHTTP(t *testing.T) {
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return testServer() }, nil)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "sign in", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer httpServer.Close()

	t.Setenv("ONELOOP_TEST_TOKEN", "secret")
	server := MCPServer{Name: "test", Type: "http", URL: httpServer.URL, Headers: map[string]string{"Authorization": "Bearer ${ONELOOP_TEST_TOKEN}"}}
	client, err := ConnectMCP(context.Background(), server, "")
	if err != nil {
		t.Fatal(err)
	}
	checkClient(t, client)
	client.Close()

	server.Headers = nil
	_, err = ConnectMCP(context.Background(), server, "")
	// agent.githubUnauthorized depends on this text.
	if err == nil || !strings.Contains(err.Error(), "The server asks to sign in (HTTP 401)") {
		t.Errorf("without a token: %v", err)
	}
}

func TestMCPStdioMissingCommand(t *testing.T) {
	server := MCPServer{Name: "test", Type: "stdio", Command: "1loop-no-such-mcp-server"}
	if _, err := ConnectMCP(context.Background(), server, t.TempDir()); err == nil || !strings.Contains(err.Error(), "1loop-no-such-mcp-server") {
		t.Errorf("err = %v", err)
	}
}
