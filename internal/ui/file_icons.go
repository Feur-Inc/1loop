// SPDX-License-Identifier: MPL-2.0

package ui

// File-type icons for the Files and Changes tabs. The artwork is drawn
// in-house: a page (or folder) in the type's colour with a small mark inside.
// The colours are mid-tones that read on both the light and dark themes, so
// the icons are not tinted with the row's ink.

import (
	"path"
	"strings"

	"fyne.io/fyne/v2"
)

// fileIconKind names the icon of a file or folder from its name alone.
func fileIconKind(name string, dir bool) string {
	base := path.Base(strings.ReplaceAll(name, "\\", "/"))
	lower := strings.ToLower(base)
	if dir {
		switch lower {
		case ".github", ".gitlab", ".git":
			return "folder-github"
		case "docs", "doc", "documentation":
			return "folder-docs"
		case "assets", "images", "img", "icons", "static", "public":
			return "folder-assets"
		}
		return "folder"
	}
	switch lower {
	case "dockerfile", "containerfile", ".dockerignore":
		return "docker"
	case "makefile", "gnumakefile":
		return "make"
	case "go.mod", "go.work":
		return "go"
	case "go.sum", "go.work.sum", "package-lock.json", "yarn.lock", "pnpm-lock.yaml", "cargo.lock", "flake.lock", "gemfile.lock", "poetry.lock", "composer.lock":
		return "lock"
	}
	stem := strings.TrimSuffix(lower, path.Ext(lower))
	switch stem {
	case "license", "licence", "copying", "notice", "unlicense":
		return "license"
	}
	if strings.HasPrefix(lower, "dockerfile.") || strings.HasSuffix(lower, ".dockerfile") {
		return "docker"
	}
	for _, archive := range []string{".tar.gz", ".tar.xz", ".tar.bz2", ".tar.zst"} {
		if strings.HasSuffix(lower, archive) {
			return "archive"
		}
	}
	switch path.Ext(lower) {
	case ".go":
		return "go"
	case ".kt", ".kts":
		return "kotlin"
	case ".sh", ".bash", ".zsh", ".fish", ".ps1", ".bat", ".cmd":
		return "shell"
	case ".md", ".markdown", ".mdx":
		return "markdown"
	case ".json", ".jsonc", ".json5":
		return "json"
	case ".yml", ".yaml":
		return "yaml"
	case ".toml":
		return "toml"
	case ".nix":
		return "nix"
	case ".mk":
		return "make"
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".ico", ".bmp", ".avif", ".icns":
		return "image"
	case ".zip", ".tar", ".gz", ".tgz", ".xz", ".bz2", ".zst", ".7z", ".rar", ".jar", ".apk", ".aab", ".deb", ".rpm":
		return "archive"
	case ".lock":
		return "lock"
	case ".txt", ".log", ".csv", ".rst":
		return "text"
	}
	if strings.HasPrefix(lower, ".") {
		return "dotfile"
	}
	return "file"
}

// fileTypeIcon is the icon of a file or folder.
func fileTypeIcon(name string, dir bool) fyne.Resource {
	if icon, ok := fileTypeIcons[fileIconKind(name, dir)]; ok {
		return icon
	}
	return fileTypeIcons["file"]
}

const (
	pageShape   = `<path d="M3.6 1.9h5.5l3.3 3.3v8.9H3.6z" fill="C" fill-opacity=".16"/><path d="M9.1 1.9v3.3h3.3"/>`
	folderShape = `<path d="M1.8 4.2c0-.7.6-1.3 1.3-1.3h3l1.4 1.5h5.4c.7 0 1.3.6 1.3 1.3v6.6c0 .7-.6 1.3-1.3 1.3H3.1c-.7 0-1.3-.6-1.3-1.3z" fill="C" fill-opacity=".22"/>`
)

func typeIcon(kind, colour, shape, mark string) fyne.Resource {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="C" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round">` + shape + mark + `</svg>`
	return fyne.NewStaticResource("filetype-"+kind+".svg", []byte(strings.ReplaceAll(svg, `"C"`, `"`+colour+`"`)))
}

var fileTypeIcons = map[string]fyne.Resource{
	"folder":        typeIcon("folder", "#d9a53f", folderShape, ``),
	"folder-github": typeIcon("folder-github", "#8b8fa0", folderShape, `<circle cx="6.6" cy="10.4" r=".9"/><circle cx="10.2" cy="8.2" r=".9"/><path d="M6.6 9.5V7.2M6.6 9.4c0-1 .9-1.2 2.7-1.2"/>`),
	"folder-docs":   typeIcon("folder-docs", "#4f8fd8", folderShape, `<path d="M5.6 7.9h4.8M5.6 10.2h3.4"/>`),
	"folder-assets": typeIcon("folder-assets", "#c164b3", folderShape, `<path d="M5 11.2l2-2.2 1.4 1.4 1-1 1.6 1.8"/><circle cx="9.9" cy="7.5" r=".5"/>`),
	"go":            typeIcon("go", "#1fa8c9", pageShape, `<path d="M6.6 8.1 5.2 9.7l1.4 1.6M9.4 8.1l1.4 1.6-1.4 1.6"/>`),
	"kotlin":        typeIcon("kotlin", "#9b6cf0", pageShape, `<path d="M5.4 8v4M10.4 8 7.6 10l2.8 2"/>`),
	"shell":         typeIcon("shell", "#45a85a", pageShape, `<path d="M5.4 8.3l1.6 1.4-1.6 1.4M8.2 11.4h2.4"/>`),
	"markdown":      typeIcon("markdown", "#5b8ad0", pageShape, `<path d="M5.1 11.6V8.2l1.3 1.6 1.3-1.6v3.4M10 8.2v3.3M9 10.6l1 1 1-1"/>`),
	"json":          typeIcon("json", "#d6a326", pageShape, `<path d="M6.9 7.9c-.8 0-.8.4-.8 1 0 .5-.3.9-.8.9.5 0 .8.4.8.9 0 .6 0 1 .8 1M9.1 7.9c.8 0 .8.4.8 1 0 .5.3.9.8.9-.5 0-.8.4-.8.9 0 .6 0 1-.8 1"/>`),
	"yaml":          typeIcon("yaml", "#d0614f", pageShape, `<path d="M5.4 8.4h.01M5.4 10.8h.01M7.3 8.4h3.4M7.3 10.8h2.6"/>`),
	"toml":          typeIcon("toml", "#a8774f", pageShape, `<path d="M5.4 8.2h5.2M8 8.2v3.6"/>`),
	"nix":           typeIcon("nix", "#6c93d6", pageShape, `<path d="M8 7.6v4.4M6.1 8.7l3.8 2.2M6.1 10.9l3.8-2.2"/>`),
	"docker":        typeIcon("docker", "#2496ed", pageShape, `<path d="M5 9.4h1.8v1.6H5zM6.8 9.4h1.8v1.6H6.8zM8.6 9.4h1.8v1.6H8.6zM6.8 7.8h1.8v1.6H6.8z" stroke-width="1"/>`),
	"make":          typeIcon("make", "#c97b3a", pageShape, `<path d="M5.6 11.8l2.6-2.6"/><path d="M8.1 7.6l1.9 1.9 1-1-1.9-1.9z"/>`),
	"image":         typeIcon("image", "#c164b3", `<rect x="2.2" y="3" width="11.6" height="10" rx="1.6" fill="C" fill-opacity=".16"/>`, `<path d="M3.6 11.4l3.1-3.3 2.2 2.2 1.4-1.4 2.2 2.4"/><circle cx="10.4" cy="6" r=".8"/>`),
	"archive":       typeIcon("archive", "#a8814d", pageShape, `<path d="M7.2 3v1.2M8.4 4.6v1.2M7.2 6.2v1.2M8.4 7.8v1.2"/><rect x="6.6" y="9.6" width="2.4" height="2.4" rx=".5"/>`),
	"license":       typeIcon("license", "#d4a72c", pageShape, `<circle cx="8" cy="9" r="1.7"/><path d="M7 10.4l-.6 2.2L8 12l1.6.6-.6-2.2"/>`),
	"lock":          typeIcon("lock", "#8b8fa0", `<rect x="3.4" y="7" width="9.2" height="7" rx="1.6" fill="C" fill-opacity=".16"/>`, `<path d="M5.4 7V5.2a2.6 2.6 0 0 1 5.2 0V7M8 9.6v1.8"/>`),
	"dotfile":       typeIcon("dotfile", "#8b8fa0", pageShape, `<circle cx="8" cy="9.8" r="1.4"/><path d="M8 7.3v.6M8 11.7v.6M5.5 9.8h.6M9.9 9.8h.6"/>`),
	"text":          typeIcon("text", "#8b8fa0", pageShape, `<path d="M5.6 8.2h4.8M5.6 10.4h4.8M5.6 12.4h2.8"/>`),
	"file":          typeIcon("file", "#8b8fa0", pageShape, ``),
}
