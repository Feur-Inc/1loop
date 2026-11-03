package agent

import (
	"encoding/base64"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"1loop/internal/core"
)

// Claude Code saves each picture a tool returns under its project folder's
// tool-results/ and adds a line naming the file to the result's text.
var imageSourceLine = regexp.MustCompile(`(?m)^\[Image: source: ([^\]\n]+)\][ \t]*(?:\n|$)`)

// maxImageSource bounds what one such file may cost to read.
const maxImageSource = 20 << 20

// splitImageSources takes the "[Image: source: …]" lines out of a tool
// result's text and returns the paths they named, in order.
func splitImageSources(text string) (string, []string) {
	var sources []string
	for _, m := range imageSourceLine.FindAllStringSubmatch(text, -1) {
		sources = append(sources, strings.TrimSpace(m[1]))
	}
	if sources == nil {
		return text, nil
	}
	return strings.TrimRight(imageSourceLine.ReplaceAllString(text, ""), "\n"), sources
}

// ToolText is a tool row's output as the user reads it: without the paths of
// the pictures, which the row shows instead.
func ToolText(item ChatItem) string {
	if item.Kind != "tool" {
		return item.Text
	}
	text, _ := splitImageSources(item.Text)
	return text
}

// ToolImageCount is how many pictures a row shows: those kept with it, or,
// for a tool row saved before 1loop kept Claude Code's screenshots, one per
// file its result named. Nothing is read.
func ToolImageCount(item ChatItem) int {
	if len(item.Images) > 0 || item.Kind != "tool" {
		return len(item.Images)
	}
	_, sources := splitImageSources(item.Text)
	return len(sources)
}

// ToolImages are the pictures a row shows, read from Claude Code's files
// when the row only names them. A file that is gone (or not a picture) is an
// empty part, so the row can say that picture is no longer available.
func ToolImages(item ChatItem) []core.ImagePart {
	if len(item.Images) > 0 || item.Kind != "tool" {
		return item.Images
	}
	_, sources := splitImageSources(item.Text)
	var parts []core.ImagePart
	for _, source := range sources {
		part, _ := readImageSource(source)
		parts = append(parts, part)
	}
	return parts
}

// readImageSource reads a picture Claude Code saved. Only a file inside a
// tool-results folder is read: the line is text a tool returned, and any
// other path it names is not one of Claude Code's own pictures.
func readImageSource(path string) (core.ImagePart, bool) {
	if !filepath.IsAbs(path) || !slices.Contains(strings.Split(filepath.Dir(filepath.Clean(path)), string(filepath.Separator)), "tool-results") {
		return core.ImagePart{}, false
	}
	file, err := os.Open(path)
	if err != nil {
		return core.ImagePart{}, false
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxImageSource+1))
	if err != nil || len(data) > maxImageSource {
		return core.ImagePart{}, false
	}
	mime := http.DetectContentType(data)
	if !strings.HasPrefix(mime, "image/") {
		return core.ImagePart{}, false
	}
	return core.ImagePart{Mime: mime, Data: base64.StdEncoding.EncodeToString(data)}, true
}
