package agent

// The real size of each model's context window, as its catalog lists it
// (vLLM's max_model_len, ChatGPT's context_window, OpenRouter's
// context_length), as Claude Code reports it or as an overflow error says.
// It is kept on disk: a guess from the model's name is only the last resort.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

var loadWindows sync.Once

func windowsPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "1loop", "context-windows.json")
}

// learnWindow records a model's window and saves the list when it changed.
func learnWindow(connection, model string, tokens int) {
	if tokens <= 0 {
		return
	}
	loadWindows.Do(readWindows)
	learnedMu.Lock()
	key := connection + "|" + model
	if learnedWindows[key] == tokens {
		learnedMu.Unlock()
		return
	}
	learnedWindows[key] = tokens
	defer learnedMu.Unlock()
	data, _ := json.Marshal(learnedWindows)
	if path := windowsPath(); path != "" && os.MkdirAll(filepath.Dir(path), 0700) == nil {
		if os.WriteFile(path+".tmp", data, 0600) == nil {
			os.Rename(path+".tmp", path)
		}
	}
}

func readWindows() {
	path := windowsPath()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	saved := map[string]int{}
	if json.Unmarshal(data, &saved) != nil {
		return
	}
	learnedMu.Lock()
	for key, tokens := range saved {
		if _, known := learnedWindows[key]; !known && tokens > 0 {
			learnedWindows[key] = tokens
		}
	}
	learnedMu.Unlock()
}

// catalogWindow is the window a model catalog entry announces, 0 if none.
func catalogWindow(row map[string]any) int {
	for _, field := range []string{"max_model_len", "context_window", "context_length", "max_context_length"} {
		if n, ok := row[field].(float64); ok && n > 0 {
			return int(n)
		}
	}
	if top, ok := row["top_provider"].(map[string]any); ok {
		if n, ok := top["context_length"].(float64); ok && n > 0 {
			return int(n)
		}
	}
	return 0
}
