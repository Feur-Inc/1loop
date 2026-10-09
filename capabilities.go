package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

var effortOrder = []string{"none", "enabled", "minimal", "low", "medium", "high", "xhigh", "max"}

func effortLabel(value string) string {
	switch value {
	case "none":
		return "Off"
	case "enabled":
		return "On"
	case "xhigh":
		return "Extra high"
	case "":
		return "Default"
	}
	return strings.ToUpper(value[:1]) + value[1:]
}
func stringsFrom(value any) []string {
	var result []string
	if list, ok := value.([]any); ok {
		for _, v := range list {
			if s, ok := v.(string); ok {
				result = append(result, s)
			} else if obj, ok := v.(map[string]any); ok {
				if s, ok := obj["reasoning_effort"].(string); ok {
					result = append(result, s)
				} else if s, ok := obj["effort"].(string); ok {
					result = append(result, s)
				}
			}
		}
	}
	return result
}
func sortedEfforts(values []string) []string {
	var out []string
	for _, effort := range effortOrder {
		if contains(values, effort) {
			out = append(out, effort)
		}
	}
	return out
}
func modelCapabilities(row map[string]any) (efforts []string, defaultEffort, mode string) {
	mandatory := false
	if reasoning, ok := row["reasoning"].(map[string]any); ok {
		raw, exposed := reasoning["supported_efforts"]
		efforts = stringsFrom(raw)
		if exposed && raw == nil {
			efforts = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}
		}
		defaultEffort, _ = reasoning["default_effort"].(string)
		mandatory = reasoning["mandatory"] == true
	}
	if len(efforts) == 0 {
		efforts = stringsFrom(row["supported_reasoning_efforts"])
	}
	if len(efforts) == 0 {
		efforts = stringsFrom(row["supported_reasoning_levels"])
	}
	if defaultEffort == "" {
		defaultEffort, _ = row["default_reasoning_effort"].(string)
	}
	if defaultEffort == "" {
		defaultEffort, _ = row["default_reasoning_level"].(string)
	}
	if len(efforts) == 0 {
		for _, v := range stringsFrom(row["supported_parameters"]) {
			if v == "reasoning" || v == "reasoning_effort" {
				efforts = []string{"none", "enabled"}
				defaultEffort = "enabled"
				if reasoning, ok := row["reasoning"].(map[string]any); ok && reasoning["default_enabled"] == false {
					defaultEffort = "none"
				}
				break
			}
		}
	}
	if mandatory {
		allowed := efforts[:0]
		for _, effort := range efforts {
			if effort != "none" {
				allowed = append(allowed, effort)
			}
		}
		efforts = allowed
	}
	efforts = sortedEfforts(efforts)
	if len(efforts) > 0 {
		mode = "effort"
		if !contains(efforts, defaultEffort) {
			defaultEffort = efforts[0]
			if contains(efforts, "medium") {
				defaultEffort = "medium"
			}
		}
	}
	return
}

var modelRepo = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
var templateEfforts = regexp.MustCompile(`reasoning_effort\s+not\s+in\s*\(([^)]+)\)`)
var quotedEffort = regexp.MustCompile(`['"](none|minimal|low|medium|high|xhigh|max)['"]`)
var templateDefault = regexp.MustCompile(`reasoning_effort\|default\(['"]([^'"]+)['"]\)`)

func templateCapabilities(template string) ([]string, string, string) {
	match := templateEfforts.FindStringSubmatch(template)
	var efforts []string
	if len(match) > 1 {
		for _, v := range quotedEffort.FindAllStringSubmatch(match[1], -1) {
			efforts = append(efforts, v[1])
		}
	}
	if len(efforts) == 0 {
		return nil, "", ""
	}
	if strings.Contains(template, "enable_thinking") {
		efforts = append(efforts, "none")
	}
	efforts = sortedEfforts(efforts)
	def := efforts[len(efforts)-1]
	if m := templateDefault.FindStringSubmatch(template); len(m) > 1 && contains(efforts, m[1]) {
		def = m[1]
	}
	return efforts, def, "template"
}

func (a *app) localCapabilities(ctx context.Context, cfg config, row map[string]any) ([]string, string, string, string) {
	repo, _ := row["root"].(string)
	cacheKey := cfg.BaseURL + "/" + repo
	a.mu.Lock()
	cached, ok := a.capabilityCache[cacheKey]
	a.mu.Unlock()
	if ok {
		return cached.Efforts, cached.DefaultEffort, cached.ReasoningMode, cached.Source
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	// Prefer the actual server template when its optional metadata endpoint is enabled.
	base, _, err := endpoint(cfg)
	if err != nil {
		return nil, "", "", ""
	}
	parsed, _ := url.Parse(base)
	parsed.Path = strings.TrimSuffix(parsed.Path, "/v1") + "/tokenizer_info"
	fetch := func(address string, bearer bool) map[string]any {
		req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
		if err != nil {
			return nil
		}
		if bearer && cfg.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
		}
		response, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
		if err != nil {
			return nil
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			return nil
		}
		var data map[string]any
		_ = json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&data)
		return data
	}
	info := fetch(parsed.String(), true)
	source := "Server tokenizer template"
	if info == nil && modelRepo.MatchString(repo) {
		info = fetch("https://huggingface.co/"+repo+"/raw/main/tokenizer_config.json", false)
		source = "Published model template"
	}
	template, _ := info["chat_template"].(string)
	efforts, def, mode := templateCapabilities(template)
	result := modelChoice{Efforts: efforts, DefaultEffort: def, ReasoningMode: mode, Source: source}
	if len(efforts) > 0 {
		a.mu.Lock()
		if a.capabilityCache == nil {
			a.capabilityCache = map[string]modelChoice{}
		}
		a.capabilityCache[cacheKey] = result
		a.mu.Unlock()
	}
	return efforts, def, mode, source
}

func (a *app) connectionModels(ctx context.Context, c connection, key string) ([]modelChoice, error) {
	cfg := c.config("")
	cfg.APIKey = key
	result, err := a.models(ctx, cfg)
	if err != nil {
		return nil, err
	}
	var choices []modelChoice
	rows, _ := result["data"].([]any)
	for _, raw := range rows {
		row, ok := raw.(map[string]any)
		if !ok || row["hidden"] == true {
			continue
		}
		id, _ := row["id"].(string)
		if id == "" {
			id, _ = row["model"].(string)
		}
		if id == "" {
			continue
		}
		name, _ := row["displayName"].(string)
		if name == "" {
			name, _ = row["name"].(string)
		}
		if name == "" {
			name = id
		}
		efforts, def, mode := modelCapabilities(row)
		source := "Provider model catalog"
		if c.Provider != "openrouter" && contains(efforts, "enabled") {
			efforts, def, mode = nil, "", ""
		}
		if c.Provider == "vllm" && len(efforts) == 0 {
			efforts, def, mode, source = a.localCapabilities(ctx, cfg, row)
		}
		choices = append(choices, modelChoice{Connection: c, ID: id, Name: name, Efforts: efforts, DefaultEffort: def, ReasoningMode: mode, Source: source})
	}
	return choices, nil
}
func (a *app) upstreamProviders(ctx context.Context, c connection, key, model string) ([]string, error) {
	cfg := c.config("")
	cfg.APIKey = key
	result, err := apiRequest(ctx, cfg, "GET", "/models/"+model+"/endpoints", nil)
	if err != nil {
		return nil, err
	}
	data, _ := result["data"].(map[string]any)
	endpoints, _ := data["endpoints"].([]any)
	set := map[string]bool{}
	for _, raw := range endpoints {
		entry, _ := raw.(map[string]any)
		slug, _ := entry["tag"].(string)
		if slug == "" {
			slug, _ = entry["provider_name"].(string)
			slug = strings.ToLower(strings.ReplaceAll(slug, " ", "-"))
		}
		if slug != "" {
			set[slug] = true
		}
	}
	var names []string
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}
