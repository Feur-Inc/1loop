package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// responsesInput converts stored messages into Responses API input items.
func responsesInput(messages []message) []any {
	var input []any
	for _, m := range messages {
		if m.Role == "system" {
			continue
		}
		if len(m.Responses) > 0 {
			for _, item := range m.Responses {
				input = append(input, item)
			}
			continue
		}
		if m.Role == "tool" {
			input = append(input, map[string]any{"type": "function_call_output", "call_id": m.ToolCallID, "output": m.Content})
			continue
		}
		if m.Role == "user" && m.Content == "" && len(m.Images) == 0 {
			continue // the Responses API rejects empty input items
		}
		if m.Role == "assistant" && m.Content == "" && len(m.Images) == 0 && len(m.ToolCalls) == 0 {
			continue
		}
		item := map[string]any{"role": m.Role}
		if len(m.Images) > 0 {
			var parts []any
			if m.Content != "" {
				parts = append(parts, map[string]any{"type": "input_text", "text": m.Content})
			}
			for _, im := range m.Images {
				parts = append(parts, map[string]any{"type": "input_image", "image_url": "data:" + im.Mime + ";base64," + im.Data})
			}
			item["content"] = parts
		} else if m.Content != "" {
			// Empty assistant content (pure tool call) is omitted: the API
			// rejects empty string content on some models.
			item["content"] = m.Content
		}
		input = append(input, item)
		for _, call := range m.ToolCalls {
			input = append(input, map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Function.Name, "arguments": call.Function.Arguments})
		}
	}
	return input
}

func responsesCompletion(ctx context.Context, cfg config, messages []message, toolsFlag bool, emit emitter) (message, error) {
	out := message{Role: "assistant"}
	input := responsesInput(messages)
	tools := make([]map[string]any, 0, 4)
	if toolsFlag {
		for _, definition := range toolDefinitions() {
			f := definition["function"].(map[string]any)
			tools = append(tools, map[string]any{"type": "function", "name": f["name"], "description": f["description"], "parameters": f["parameters"], "strict": true})
		}
	}
	body := mergeOptions(cfg.Parameters, map[string]any{"model": cfg.Model, "instructions": systemInstructions(cfg), "input": input, "store": false, "stream": true, "include": []string{"reasoning.encrypted_content"}})
	if toolsFlag {
		body["tools"] = tools
	}
	if cfg.Effort != "" && cfg.Effort != "enabled" {
		body["reasoning"] = map[string]any{"effort": cfg.Effort}
	}
	response, err := request(ctx, cfg, "POST", "/responses", body)
	if err != nil {
		return out, err
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 4096), 8<<20)
	var data []string
	completed := false
	status := &announcer{emit: emit}
	calls := map[string]*struct{ name, args string }{} // function calls being streamed, by item id
	consume := func() error {
		if len(data) == 0 {
			return nil
		}
		raw := strings.Join(data, "\n")
		data = nil
		var event struct {
			Type   string `json:"type"`
			Delta  string `json:"delta"`
			ItemID string `json:"item_id"`
			Item   struct {
				ID   string `json:"id"`
				Type string `json:"type"`
				Name string `json:"name"`
			} `json:"item"`
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
			Response struct {
				Status string            `json:"status"`
				Output []json.RawMessage `json:"output"`
				Error  struct {
					Message string `json:"message"`
				} `json:"error"`
				Usage struct {
					InputTokens int `json:"input_tokens"`
				} `json:"usage"`
			} `json:"response"`
		}
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			return fmt.Errorf("Invalid OpenAI event: %w", err)
		}
		switch event.Type {
		case "response.output_item.added":
			switch event.Item.Type {
			case "reasoning":
				status.set(activity{State: "think"})
			case "function_call":
				calls[event.Item.ID] = &struct{ name, args string }{name: event.Item.Name}
				status.set(activity{State: "call", Tool: event.Item.Name})
			}
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			status.set(activity{State: "think"})
		case "response.function_call_arguments.delta":
			if call := calls[event.ItemID]; call != nil && status.last.Detail == "" {
				call.args += event.Delta
				status.set(activity{State: "call", Tool: call.name, Detail: toolTarget(call.args)})
			}
		case "response.output_text.delta":
			out.Content += event.Delta
			status.last = activity{State: "write"} // the delta itself tells the interface
			emit("delta", event.Delta)
		case "response.failed", "response.incomplete":
			return fmt.Errorf("OpenAI response interrupted: %s %s", event.Response.Status, event.Response.Error.Message)
		case "error":
			return fmt.Errorf("OpenAI : %s", event.Error.Message)
		case "response.completed":
			if event.Response.Status != "completed" {
				return fmt.Errorf("Incomplete OpenAI response")
			}
			completed = true
			out.Responses = event.Response.Output
			out.Usage = event.Response.Usage.InputTokens
			for _, rawItem := range event.Response.Output {
				var item struct {
					Type      string `json:"type"`
					CallID    string `json:"call_id"`
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}
				if err := json.Unmarshal(rawItem, &item); err != nil {
					return err
				}
				if item.Type == "function_call" {
					if item.CallID == "" || item.Name == "" || !json.Valid([]byte(item.Arguments)) {
						return fmt.Errorf("Incomplete OpenAI tool call")
					}
					call := toolCall{ID: item.CallID, Type: "function"}
					call.Function.Name = item.Name
					call.Function.Arguments = item.Arguments
					out.ToolCalls = append(out.ToolCalls, call)
				}
			}
		}
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := consume(); err != nil {
				return out, err
			}
			if completed {
				return out, nil
			}
		} else if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err = scanner.Err(); err != nil {
		return out, err
	}
	if err = consume(); err != nil {
		return out, err
	}
	if !completed {
		return out, fmt.Errorf("OpenAI stream interrupted before the response completed")
	}
	return out, nil
}
