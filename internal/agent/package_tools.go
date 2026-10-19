package agent

func packageToolDefinitions() []map[string]any {
	return []map[string]any{{"type": "function", "function": map[string]any{
		"name":        "install_packages",
		"description": "Install named Debian packages from the configured signed repositories on this phone. Only package names are accepted, not apt flags or commands. The installer can modify Debian's programs; it does not expose Android files or enable ADB. Use it only for packages needed for the user's task, under the current approval policy.",
		"parameters": map[string]any{"type": "object", "properties": map[string]any{
			"packages": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1, "maxItems": 30},
		}, "required": []string{"packages"}, "additionalProperties": false},
	}}}
}
