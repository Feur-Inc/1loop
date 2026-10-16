package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"1loop/internal/platform"
)

// responsesInput converts stored messages into Responses API input items.
func responsesInput(messages []Message) []any {
	var input []any
	for _, messageValue := range messages {
		if messageValue.Role == "system" {
			continue
		}
		if len(messageValue.Responses) > 0 {
			for _, item := range messageValue.Responses {
				input = append(input, item)
			}
			continue
		}
		if messageValue.Role == "tool" {
			input = append(input, map[string]any{"type": "function_call_output", "call_id": messageValue.ToolCallID, "output": messageValue.Content})
			continue
		}
		if messageValue.Role == "user" && messageValue.Content == "" && len(messageValue.Images) == 0 {
			continue // the Responses API rejects empty input items
		}
		if messageValue.Role == "assistant" && messageValue.Content == "" && len(messageValue.Images) == 0 && len(messageValue.ToolCalls) == 0 {
			continue
		}
		item := map[string]any{"role": messageValue.Role}
		if len(messageValue.Images) > 0 {
			var parts []any
			if messageValue.Content != "" {
				parts = append(parts, map[string]any{"type": "input_text", "text": messageValue.Content})
			}
			for _, imagePart := range messageValue.Images {
				parts = append(parts, map[string]any{"type": "input_image", "image_url": "data:" + imagePart.Mime + ";base64," + imagePart.Data})
			}
			item["content"] = parts
		} else if messageValue.Content != "" {
			item["content"] = messageValue.Content
		}
		// A pure tool call becomes function_call items below, not a message
		// without content (which the Responses API rejects).
		if messageValue.Content != "" || len(messageValue.Images) > 0 {
			input = append(input, item)
		}
		for _, call := range messageValue.ToolCalls {
			item := map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Function.Name, "arguments": call.Function.Arguments}
			if group := groupOfTool(call.Function.Name); group != "" {
				item["namespace"] = group // a deferred tool is called through its namespace
			}
			input = append(input, item)
		}
	}
	return input
}

func responsesCompletion(ctx context.Context, cfg Config, messages []Message, toolsFlag bool, emit emitter) (Message, error) {
	out := Message{Role: "assistant"}
	input := responsesInput(messages)
	// The instructions are the system message leading the conversation
	// (the agent's, or a side call's own, as the auto-review's).
	instructions := systemInstructions(cfg)
	if len(messages) > 0 && messages[0].Role == "system" {
		instructions = messages[0].Content
	}
	body := MergeOptions(cfg.Parameters, map[string]any{
		"model":        cfg.Model,
		"instructions": instructions,
		"input":        input,
		"store":        false,
		"stream":       true,
		"include":      []string{"reasoning.encrypted_content"},
	})
	if toolsFlag {
		body["tools"] = responsesTools(cfg)
		if cfg.toolSet != nil {
			body["tools"] = responsesFunctions(cfg.toolSet)
		}
		body["parallel_tool_calls"] = true
	}
	if cfg.ServiceTier != "" {
		body["service_tier"] = cfg.ServiceTier
	}
	if key := promptCacheKey(cfg); key != "" {
		body["prompt_cache_key"] = key
	}
	if cfg.Effort != "" && cfg.Effort != "enabled" {
		body["reasoning"] = map[string]any{"effort": cfg.Effort}
	}
	response, err := request(ctx, cfg, "POST", "/responses", body)
	if err != nil {
		return out, err
	}
	defer response.Body.Close()
	return readResponses(response.Body, emit)
}

// readResponses reads one streamed answer of the Responses API.
//
// The answer's items (reasoning, text, tool calls) each end with their own
// event, and the final event repeats them all. Some servers leave that
// final list empty (ChatGPT's backend does at times): the items are
// therefore kept as they arrive, and used when the final list has none.
// Reading only the final list made such an answer look empty: the model's
// tool call or text was dropped without a word.
func readResponses(stream io.Reader, emit emitter) (Message, error) {
	out := Message{Role: "assistant"}
	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 4096), 8<<20)
	var data []string
	var arrived []json.RawMessage // the items, as each was completed
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
			Type    string          `json:"type"`
			Delta   string          `json:"delta"`
			ItemID  string          `json:"item_id"`
			RawItem json.RawMessage `json:"item"`
			Error   struct {
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
					Details     struct {
						CachedTokens *int `json:"cached_tokens"`
					} `json:"input_tokens_details"`
				} `json:"usage"`
			} `json:"response"`
		}
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			return fmt.Errorf("Invalid OpenAI event: %w", err)
		}
		if platform.Dev != nil && !strings.HasSuffix(event.Type, ".delta") {
			platform.Devf("api", "responses event %s, %d bytes", event.Type, len(raw))
		}
		var item struct {
			ID   string `json:"id"`
			Type string `json:"type"`
			Name string `json:"name"`
		}
		if len(event.RawItem) > 0 {
			_ = json.Unmarshal(event.RawItem, &item)
		}
		switch event.Type {
		case "response.output_item.added":
			switch item.Type {
			case "reasoning":
				status.set(Activity{State: "think"})
			case "function_call":
				calls[item.ID] = &struct{ name, args string }{name: item.Name}
				status.set(Activity{State: "call", Tool: item.Name})
			case "web_search_call", "tool_search_call":
				status.set(Activity{State: "call", Tool: strings.TrimSuffix(item.Type, "_call")})
			}
		case "response.output_item.done":
			if len(event.RawItem) > 0 {
				arrived = append(arrived, append(json.RawMessage(nil), event.RawItem...))
			}
			// OpenAI runs these itself: the chat keeps a line about them.
			switch item.Type {
			case "web_search_call":
				var search struct {
					Action struct {
						Type, Query, URL string
						Queries          []string
					}
				}
				_ = json.Unmarshal(event.RawItem, &search)
				what := search.Action.Query
				if what == "" && len(search.Action.Queries) > 0 {
					what = strings.Join(search.Action.Queries, " · ")
				}
				if what == "" {
					what = search.Action.URL
				}
				emit("note", strings.TrimSpace("Searched the web · "+what))
			case "tool_search_output":
				var loaded struct {
					Tools []struct{ Name string }
				}
				_ = json.Unmarshal(event.RawItem, &loaded)
				var names []string
				for _, tool := range loaded.Tools {
					names = append(names, tool.Name)
				}
				if len(names) > 0 {
					emit("note", "Loaded tools · "+strings.Join(names, ", "))
				}
			}
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			status.set(Activity{State: "think"})
		case "response.function_call_arguments.delta":
			if call := calls[event.ItemID]; call != nil && status.last.Detail == "" {
				call.args += event.Delta
				status.set(Activity{State: "call", Tool: call.name, Detail: toolTarget(call.args)})
			}
		case "response.output_text.delta":
			out.Content += event.Delta
			status.last = Activity{State: "write"} // the delta itself tells the interface
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
			output := event.Response.Output
			if len(output) == 0 {
				if len(arrived) > 0 {
					platform.Devf("api", "the final event lists no item: using the %d that arrived", len(arrived))
				}
				output = arrived
			}
			out.Responses = output
			out.Usage = event.Response.Usage.InputTokens
			out.CachedTokens = event.Response.Usage.Details.CachedTokens
			for _, rawItem := range output {
				var item struct {
					Type      string          `json:"type"`
					CallID    string          `json:"call_id"`
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"` // a string for functions, an object for tool_search
				}
				if err := json.Unmarshal(rawItem, &item); err != nil {
					return err
				}
				if item.Type == "function_call" {
					var arguments string
					if json.Unmarshal(item.Arguments, &arguments) != nil {
						return fmt.Errorf("Incomplete OpenAI tool call")
					}
					if item.CallID == "" || item.Name == "" || !json.Valid([]byte(arguments)) {
						return fmt.Errorf("Incomplete OpenAI tool call")
					}
					call := ToolCall{ID: item.CallID, Type: "function"}
					call.Function.Name = item.Name
					call.Function.Arguments = arguments
					out.ToolCalls = append(out.ToolCalls, call)
				}
			}
			if out.Content == "" && len(out.ToolCalls) == 0 {
				// Nothing to show and nothing to run: say so rather than
				// end the turn in silence.
				return fmt.Errorf("The model answered with nothing. Send your message again.")
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
	if err := scanner.Err(); err != nil {
		return out, err
	}
	if err := consume(); err != nil {
		return out, err
	}
	if !completed {
		return out, fmt.Errorf("OpenAI stream interrupted before the response completed")
	}
	return out, nil
}
