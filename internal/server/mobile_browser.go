package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"

	"1loop/internal/browser"
	"1loop/internal/core"
	"github.com/chromedp/cdproto/target"
)

// Only the engine knows the unguessable CDP route. The socket itself is owned
// by Android WebView, outside Debian and its process tracer.
type mobileBrowser struct {
	bridge  *mobileBridge
	address string
	client  *http.Client
	// profile, for a ChatGPT (Web) account: its WebViews keep their own
	// cookies, apart from Browser Use's.
	profile string
}

func (host *mobileBrowser) call(ctx context.Context, action, id string, values any, result any) error {
	data, _ := json.Marshal(values)
	args := map[string]any{}
	if values != nil {
		_ = json.Unmarshal(data, &args)
	}
	args["action"], args["id"] = action, id
	method := "browser_native"
	if host.profile != "" {
		method, args["profile"] = "chatgpt_native", host.profile
	}
	raw, _ := json.Marshal(args)
	return host.bridge.call(ctx, method, raw, result)
}

func (host *mobileBrowser) Connect(ctx context.Context) (string, target.ID, error) {
	id := "controller-" + core.Identifier()[:16]
	var opened struct{ Socket, Marker string }
	if err := host.call(ctx, "open", id, map[string]int{"width": 240, "height": 200}, &opened); err != nil {
		return "", "", err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", "", err
	}
	route := "/" + core.Identifier() + "/"
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", "\x00"+opened.Socket)
	}}
	proxy := &httputil.ReverseProxy{Director: func(request *http.Request) {
		request.URL.Scheme, request.URL.Host = "http", "localhost"
		request.URL.Path = "/" + strings.TrimPrefix(request.URL.Path, route)
	}, Transport: transport}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, route) {
			http.NotFound(w, r)
			return
		}
		proxy.ServeHTTP(w, r)
	}), ReadHeaderTimeout: 5 * time.Second}
	go server.Serve(listener)
	context.AfterFunc(ctx, func() { server.Close(); transport.CloseIdleConnections() })
	host.address = "http://" + listener.Addr().String() + route
	host.client = &http.Client{Timeout: 5 * time.Second}
	idTarget, err := host.find(ctx, opened.Marker)
	if err != nil {
		return "", "", err
	}
	var version struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := host.get(ctx, "json/version", &version); err != nil {
		return "", "", err
	}
	position := strings.Index(version.WebSocketDebuggerURL, "/devtools/")
	if position < 0 {
		return "", "", fmt.Errorf("Android WebView has no browser debugging endpoint")
	}
	return "ws" + strings.TrimPrefix(host.address, "http") + strings.TrimPrefix(version.WebSocketDebuggerURL[position:], "/"), idTarget, nil
}

func (host *mobileBrowser) get(ctx context.Context, path string, result any) error {
	request, err := http.NewRequestWithContext(ctx, "GET", host.address+path, nil)
	if err != nil {
		return err
	}
	response, err := host.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("Android WebView debugger answered %d", response.StatusCode)
	}
	return json.NewDecoder(response.Body).Decode(result)
}

func (host *mobileBrowser) find(parent context.Context, marker string) (target.ID, error) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	for {
		var pages []struct {
			ID  target.ID
			URL string
		}
		if err := host.get(ctx, "json/list", &pages); err == nil {
			for _, page := range pages {
				if page.URL == marker {
					return page.ID, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("Android WebView tab was not published: %w", ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (host *mobileBrowser) Open(ctx context.Context, info browser.Info) (target.ID, error) {
	var opened struct{ Marker string }
	if err := host.call(ctx, "open", info.ID, info, &opened); err != nil {
		return "", err
	}
	return host.find(ctx, opened.Marker)
}
func (host *mobileBrowser) Resize(ctx context.Context, info browser.Info) error {
	return host.call(ctx, "resize", info.ID, info, &struct{}{})
}
func (host *mobileBrowser) Capture(ctx context.Context, id string, full bool) (core.ImagePart, error) {
	var image core.ImagePart
	err := host.call(ctx, "capture", id, map[string]bool{"full": full}, &image)
	return image, err
}
func (host *mobileBrowser) Close(ctx context.Context, id string) error {
	return host.call(ctx, "close", id, nil, &struct{}{})
}

func (host *mobileBrowser) Stop(ctx context.Context) error {
	return host.call(ctx, "close_all", "controller", nil, &struct{}{})
}
