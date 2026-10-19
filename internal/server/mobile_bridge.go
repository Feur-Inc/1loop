package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"1loop/internal/agent"
	"1loop/internal/core"
)

type mobileBridge struct {
	Socket string `json:"socket"`
	Token  string `json:"token"`
}

func loadMobileBridge(path string) (*mobileBridge, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("The mobile bridge configuration must be an owner-only regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var bridge mobileBridge
	if err := json.Unmarshal(data, &bridge); err != nil {
		return nil, err
	}
	if bridge.Socket == "" || len(bridge.Socket) > 100 || len(bridge.Token) != 64 {
		return nil, fmt.Errorf("Invalid mobile bridge configuration")
	}
	return &bridge, nil
}

func (bridge *mobileBridge) call(ctx context.Context, method string, arguments json.RawMessage, result any) error {
	request, err := json.Marshal(struct {
		Token     string          `json:"token"`
		Method    string          `json:"method"`
		Arguments json.RawMessage `json:"arguments"`
	}{bridge.Token, method, arguments})
	if err != nil {
		return err
	}
	connection, err := (&net.Dialer{}).DialContext(ctx, "unix", "\x00"+bridge.Socket)
	if err != nil {
		return err
	}
	defer connection.Close()
	stop := context.AfterFunc(ctx, func() { connection.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		connection.SetDeadline(deadline)
	}
	if len(request) > 256<<10 {
		return fmt.Errorf("Local request is too large")
	}
	if err := binary.Write(connection, binary.BigEndian, uint32(len(request))); err != nil {
		return err
	}
	if _, err := io.Copy(connection, bytes.NewReader(request)); err != nil {
		return err
	}
	var length uint32
	if err := binary.Read(connection, binary.BigEndian, &length); err != nil {
		return err
	}
	if length == 0 || length > 64<<20 {
		return fmt.Errorf("Invalid local response size")
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(connection, data); err != nil {
		return err
	}
	var failure struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(data, &failure); err != nil {
		return err
	}
	if failure.Error != "" {
		return fmt.Errorf("%s", failure.Error)
	}
	return json.Unmarshal(data, result)
}

func (bridge *mobileBridge) available(parent context.Context, capability string) bool {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	var state map[string]bool
	return bridge.call(ctx, "state", nil, &state) == nil && state[capability]
}

func (bridge *mobileBridge) phoneTools(ctx context.Context, name string, raw json.RawMessage) (agent.PanelToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var result struct {
		Text   string           `json:"text"`
		Images []core.ImagePart `json:"images"`
	}
	err := bridge.call(ctx, name, raw, &result)
	return agent.PanelToolResult{Text: result.Text, Images: result.Images}, err
}

func (bridge *mobileBridge) packageTools(ctx context.Context, name string, raw json.RawMessage) (agent.PanelToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	var result struct {
		Text string `json:"text"`
	}
	err := bridge.call(ctx, name, raw, &result)
	return agent.PanelToolResult{Text: result.Text}, err
}
