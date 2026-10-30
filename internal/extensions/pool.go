package extensions

// The connections to the MCP servers, kept open between the agent's turns:
// a command starts once per server and project, and its tool list is read
// once per connection.

import (
	"context"
	"encoding/json"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type poolEntry struct {
	server MCPServer
	client *MCPClient
	tools  []MCPTool
	err    error
	tried  time.Time
}

// Pool holds the open connections.
type Pool struct {
	mu      sync.Mutex
	entries map[string]*poolEntry
	connect sync.Map // key → *sync.Mutex: one connection attempt at a time
}

// Servers is the pool every agent and settings page share.
var Servers = &Pool{entries: map[string]*poolEntry{}}

func poolKey(server MCPServer, dir string) string {
	if server.Type == "http" {
		dir = "" // a remote server is the same whatever the project
	}
	return server.Name + "\x00" + server.Plugin + "\x00" + dir
}

// Tools connects to a server when needed and gives its tools. A failed
// attempt is not repeated for 30 seconds.
func (pool *Pool) Tools(ctx context.Context, server MCPServer, dir string) ([]MCPTool, error) {
	entry, err := pool.entry(ctx, server, dir)
	if err != nil {
		return nil, err
	}
	return entry.tools, nil
}

func (pool *Pool) entry(ctx context.Context, server MCPServer, dir string) (*poolEntry, error) {
	key := poolKey(server, dir)
	lock, _ := pool.connect.LoadOrStore(key, &sync.Mutex{})
	lock.(*sync.Mutex).Lock()
	defer lock.(*sync.Mutex).Unlock()
	pool.mu.Lock()
	entry := pool.entries[key]
	pool.mu.Unlock()
	if entry != nil && reflect.DeepEqual(entry.server, server) {
		if entry.client != nil && entry.client.Err() == nil {
			return entry, nil
		}
		if entry.client == nil && time.Since(entry.tried) < 30*time.Second {
			return nil, entry.err
		}
	}
	if entry != nil && entry.client != nil {
		entry.client.Close()
	}
	entry = &poolEntry{server: server, tried: time.Now()}
	connectCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
	defer cancel()
	client, err := ConnectMCP(connectCtx, server, dir)
	if err == nil {
		entry.tools, err = client.Tools(connectCtx)
		if err != nil {
			client.Close()
		} else {
			entry.client = client
		}
	}
	entry.err = err
	pool.mu.Lock()
	pool.entries[key] = entry
	pool.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return entry, nil
}

// Call runs a tool of a server.
func (pool *Pool) Call(ctx context.Context, server MCPServer, dir, tool string, arguments json.RawMessage) (MCPResult, error) {
	entry, err := pool.entry(ctx, server, dir)
	if err != nil {
		return MCPResult{}, err
	}
	return entry.client.CallTool(ctx, tool, arguments)
}

// Forget closes a server's connections (it changed or was removed), so
// the next use connects again.
func (pool *Pool) Forget(name string) {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	for key, entry := range pool.entries {
		if entry.server.Name == name {
			if entry.client != nil {
				entry.client.Close()
			}
			delete(pool.entries, key)
		}
	}
}

// CloseAll ends every connection (1loop quits).
func (pool *Pool) CloseAll() {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	for key, entry := range pool.entries {
		if entry.client != nil {
			entry.client.Close()
		}
		delete(pool.entries, key)
	}
}

// MCPStatus is a server as the settings show it.
type MCPStatus struct {
	MCPServer
	Connected bool     `json:"connected"`
	Tools     []string `json:"tools"`
	Error     string   `json:"error,omitempty"`
}

// Status connects to each enabled server (dir is the project a command
// starts in) and says how it went; disabled ones are listed as they are.
func (pool *Pool) Status(ctx context.Context, servers []MCPServer, dir string) []MCPStatus {
	out := make([]MCPStatus, len(servers))
	var wait sync.WaitGroup
	for i, server := range servers {
		out[i] = MCPStatus{MCPServer: server, Tools: []string{}}
		if !server.Enabled {
			continue
		}
		wait.Add(1)
		go func(i int, server MCPServer) {
			defer wait.Done()
			tools, err := pool.Tools(ctx, server, dir)
			if err != nil {
				out[i].Error = err.Error()
				return
			}
			out[i].Connected = true
			for _, tool := range tools {
				out[i].Tools = append(out[i].Tools, tool.Name)
			}
			sort.Strings(out[i].Tools)
		}(i, server)
	}
	wait.Wait()
	return out
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// ToolName is how the agent calls a server's tool, as Claude Code names
// them: mcp__server__tool, within the 64 characters the APIs allow.
func ToolName(server, tool string) string {
	name := "mcp__" + unsafeName.ReplaceAllString(server, "_") + "__" + unsafeName.ReplaceAllString(tool, "_")
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}

// IsToolName tells whether a tool name is an MCP server's.
func IsToolName(name string) bool { return strings.HasPrefix(name, "mcp__") }
