// SPDX-License-Identifier: MPL-2.0

package agent

// theme_write of the settings tool (issue #488): the model writes a theme
// file (and its shader) into the folder 1loop reads themes from, after
// 1loop has checked it and the user has approved. The format is read on
// demand with theme_format, not carried in every chat.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"1loop/internal/palette"
)

func (backend *App) writeTheme(ctx context.Context, name, text, shader string, begin func(), emit emitter) (string, error) {
	id := strings.Trim(palette.ThemeNameUnsafe.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if id == "" {
		return "", fmt.Errorf("Give the theme's name in key, for example \"midnight\"")
	}
	dir := palette.ThemesDir()
	if dir == "" {
		return "", fmt.Errorf("This system has no folder for 1loop themes")
	}
	theme, err := palette.CheckThemeFile(id, text, shader)
	if err != nil {
		return "", fmt.Errorf("The theme was not saved: %v. Call theme_format for the format.", err)
	}
	path := filepath.Join(dir, id+".toml")
	_, existing := os.Stat(path)
	verb := "Create"
	if existing == nil {
		verb = "Replace"
	}
	preview := fmt.Sprintf("%s the theme %q in %s\n\n%s", verb, theme.Name, dir, text)
	if shader != "" {
		preview += "\n\nShader " + theme.Shader + ":\n" + shader
	}
	if !backend.askApproval(ctx, emit, "Save a 1loop theme?", preview) {
		return "", fmt.Errorf("Theme not saved: the user denied it")
	}
	begin()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if shader != "" {
		if err := os.WriteFile(filepath.Join(dir, theme.Shader), []byte(shader), 0o644); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("Saved %s. 1loop reads it within a second; then settings set general.theme to file:%s applies it (check the key with get). A shader that fails to compile is reported to the user in Settings → General, not here.", path, id), nil
}
