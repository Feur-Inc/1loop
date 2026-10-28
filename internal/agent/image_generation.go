package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"1loop/internal/core"
)

func imageGenerationToolDefinition() map[string]any {
	return map[string]any{"type": "function", "function": map[string]any{
		"name":        "imagegen",
		"description": "Generate or edit an image using the connected ChatGPT subscription. Use for requested pictures, illustrations, diagrams and image edits. The image is shown in the conversation, saved as a PNG in generated-images/ in the workspace, and returned for you to inspect. For edits, provide up to five referenced_image_paths inside the workspace, or num_last_images_to_include (1–5) for recent conversation images; never both. Omit both for a new image. Preserve transparency when editing unless asked otherwise. Generation can take several minutes. Do not use another account or an API key if the subscription refuses access.",
		"parameters": map[string]any{"type": "object", "properties": map[string]any{
			"prompt":                     map[string]any{"type": "string"},
			"transparent_background":     map[string]any{"type": []string{"boolean", "null"}},
			"referenced_image_paths":     map[string]any{"type": []string{"array", "null"}, "items": map[string]string{"type": "string"}, "maxItems": 5},
			"num_last_images_to_include": map[string]any{"type": []string{"integer", "null"}, "minimum": 1, "maximum": 5},
		}, "required": []string{"prompt", "transparent_background", "referenced_image_paths", "num_last_images_to_include"}, "additionalProperties": false},
	}}
}

func (backend *App) generateImage(ctx context.Context, cfg Config, arguments string, begin func()) (string, []core.ImagePart, error) {
	if cfg.Provider != "openai" {
		return "", nil, fmt.Errorf("Image generation requires a ChatGPT subscription connection")
	}
	var args struct {
		Prompt      string   `json:"prompt"`
		Transparent bool     `json:"transparent_background"`
		Paths       []string `json:"referenced_image_paths"`
		Recent      *int     `json:"num_last_images_to_include"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", nil, err
	}
	if strings.TrimSpace(args.Prompt) == "" {
		return "", nil, fmt.Errorf("Describe the image to generate")
	}
	if len(args.Paths) > 5 || args.Recent != nil && (*args.Recent < 1 || *args.Recent > 5 || len(args.Paths) > 0) {
		return "", nil, fmt.Errorf("Use up to five file paths or 1–5 recent images, not both")
	}
	var inputs []core.ImagePart
	for _, name := range args.Paths {
		data, err := workspaceImage(ctx, cfg.Workspace, name)
		if err != nil {
			return "", nil, err
		}
		if len(data) > 32<<20 {
			return "", nil, fmt.Errorf("Referenced images must be at most 32 MiB")
		}
		if _, _, err = image.DecodeConfig(bytes.NewReader(data)); err != nil {
			return "", nil, fmt.Errorf("Cannot read the referenced image %s: %w", name, err)
		}
		inputs = append(inputs, core.ImagePart{Mime: http.DetectContentType(data), Data: base64.StdEncoding.EncodeToString(data)})
	}
	if args.Recent != nil {
		if cfg.Recall == nil {
			return "", nil, fmt.Errorf("Conversation images are unavailable; use a workspace image path")
		}
		items, err := cfg.Recall(ctx)
		if err != nil {
			return "", nil, err
		}
		for i := len(items) - 1; i >= 0 && len(inputs) < *args.Recent; i-- {
			for j := len(items[i].Images) - 1; j >= 0 && len(inputs) < *args.Recent; j-- {
				inputs = append(inputs, items[i].Images[j])
			}
		}
		if len(inputs) < *args.Recent {
			return "", nil, fmt.Errorf("The conversation does not contain %d images", *args.Recent)
		}
		for i, j := 0, len(inputs)-1; i < j; i, j = i+1, j-1 {
			inputs[i], inputs[j] = inputs[j], inputs[i]
		}
	}
	cfg, err := backend.openAIConfig(ctx, cfg)
	if err != nil {
		return "", nil, err
	}
	background := "opaque"
	if args.Transparent {
		background = "transparent"
	}
	body := map[string]any{"prompt": args.Prompt, "model": "gpt-image-2.5-sunburst", "background": background, "quality": "auto", "size": "auto"}
	endpoint := "/images/generations"
	if len(inputs) > 0 {
		endpoint = "/images/edits"
		images := make([]map[string]string, 0, len(inputs))
		for _, part := range inputs {
			images = append(images, map[string]string{"image_url": "data:" + part.Mime + ";base64," + part.Data})
		}
		body["images"] = images
	}
	begin()
	response, err := request(ctx, cfg, "POST", endpoint, body)
	if err != nil {
		return "", nil, err
	}
	defer response.Body.Close()
	encoded, err := readGeneratedImage(response)
	if err != nil {
		return "", nil, err
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", nil, fmt.Errorf("Invalid generated image: %w", err)
	}
	if _, format, err := image.DecodeConfig(bytes.NewReader(data)); err != nil || format != "png" {
		return "", nil, fmt.Errorf("The image service returned an invalid PNG")
	}
	if machine := machineOf(ctx); machine != nil {
		dir, err := MachinePath(ctx, machine, cfg.Workspace, "generated-images")
		if err != nil {
			return "", nil, err
		}
		name := path.Join(dir, core.Identifier()+".png")
		if err := machineNewFile(ctx, machine, name, string(data)); err != nil {
			return "", nil, err
		}
		return imageSaved(name), []core.ImagePart{{Mime: "image/png", Data: encoded}}, nil
	}
	dir, err := WorkspacePath(cfg.Workspace, "generated-images")
	if err != nil {
		return "", nil, err
	}
	if err = os.MkdirAll(dir, 0755); err != nil {
		return "", nil, err
	}
	path := filepath.Join(dir, core.Identifier()+".png")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", nil, err
	}
	_, err = file.Write(data)
	closeErr := file.Close()
	if err != nil {
		return "", nil, err
	}
	if closeErr != nil {
		return "", nil, closeErr
	}
	return imageSaved(path), []core.ImagePart{{Mime: "image/png", Data: encoded}}, nil
}

func imageSaved(path string) string {
	return "Generated image saved to " + path + " (PNG). It is already displayed to the user and returned below for you to inspect. Use this file for further edits or exports; do not regenerate it just to view it."
}

// workspaceImage reads a referenced image of the workspace, here or on
// the run's machine (32 MiB at most, the caller checks).
func workspaceImage(ctx context.Context, workspace, name string) ([]byte, error) {
	if machine := machineOf(ctx); machine != nil {
		target, err := MachinePath(ctx, machine, workspace, name)
		if err != nil {
			return nil, err
		}
		info, err := machine.Lstat(ctx, target)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("Referenced images must be regular files")
		}
		if info.Size() > 32<<20 {
			return nil, fmt.Errorf("Referenced images must be at most 32 MiB")
		}
		return machine.ReadFile(ctx, target)
	}
	path, err := WorkspacePath(workspace, name)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("Referenced images must be regular files")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, (32<<20)+1))
}

// Subscription image requests can return either a JSON result or an SSE
// stream of keepalives and a final image. Neither is a Responses stream.
func readGeneratedImage(response *http.Response) (string, error) {
	var result struct {
		Type  string `json:"type"`
		Image string `json:"b64_json"`
		Data  []struct {
			Image string `json:"b64_json"`
		} `json:"data"`
		Error json.RawMessage `json:"error"`
	}
	if strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 4096), 48<<20)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			result.Type, result.Image, result.Error = "", "", nil
			if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &result); err != nil {
				return "", err
			}
			if result.Type == "image_generation.completed" && result.Image != "" {
				return result.Image, nil
			}
			if result.Type == "error" || result.Type == "image_generation.failed" {
				return "", fmt.Errorf("Image generation failed: %s", result.Error)
			}
		}
		if err := scanner.Err(); err != nil {
			return "", err
		}
	} else {
		if err := json.NewDecoder(io.LimitReader(response.Body, 48<<20)).Decode(&result); err != nil {
			return "", err
		}
		if len(result.Data) > 0 && result.Data[0].Image != "" {
			return result.Data[0].Image, nil
		}
	}
	return "", fmt.Errorf("Image generation ended without a completed image")
}
