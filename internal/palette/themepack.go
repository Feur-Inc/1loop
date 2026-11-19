// SPDX-License-Identifier: MPL-2.0

package palette

// Theme packages (issue #499): a theme as one .zip file, or as the same
// layout unpacked in a folder, beside the loose .toml files of #464. Inside
// there is the theme TOML (theme.toml), its shaders, fonts, licence and an
// optional preview, named by relative paths. A zip is read in memory and
// never extracted to disk; everything in it is checked first: no absolute
// or ".." paths (zip slip), a cap on entries and size, and only theme files.

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Limits of a theme package.
const (
	MaxThemeEntries = 200      // files and folders in one zip
	MaxThemeBytes   = 64 << 20 // unpacked size of one theme
	maxThemeToml    = 1 << 20  // the theme file itself
	// ThemeFileName is the theme file inside a zip or a theme folder.
	ThemeFileName = "theme.toml"
	// zipSep joins a zip's path to an entry inside it: "a/b.zip!/fonts/x.ttf".
	zipSep = ".zip!/"
)

var packedExtensions = map[string]bool{
	".toml": true, ".frag": true, ".glsl": true, ".ttf": true, ".otf": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".txt": true, ".md": true,
}

// licenceName is a licence file without an extension, like LICENSE or OFL.
func licenceName(name string) bool {
	lower := strings.ToLower(name)
	for _, word := range []string{"licen", "copying", "ofl", "notice", "readme"} {
		if strings.HasPrefix(lower, word) {
			return true
		}
	}
	return false
}

// checkEntryName says in plain words why a path inside a theme package is
// refused, or nil when it is a safe relative path to an allowed file type.
func checkEntryName(name string, dir bool) error {
	switch {
	case name == "":
		return errors.New("an entry has no name")
	case strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\`) || (len(name) > 1 && name[1] == ':'):
		return fmt.Errorf("%q is an absolute path", name)
	case strings.Contains(name, `\`):
		return fmt.Errorf("%q uses a backslash in its path", name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return fmt.Errorf("%q goes up out of the theme with ..", name)
		}
	}
	if dir {
		return nil
	}
	ext := strings.ToLower(path.Ext(name))
	if ext == "" && licenceName(path.Base(name)) || packedExtensions[ext] {
		return nil
	}
	return fmt.Errorf("%q is not a file a theme may carry (only .toml, shaders, fonts, images and text files)", name)
}

// checkZip validates a zip's entries before anything is read from it.
func checkZip(files []*zip.File) error {
	if len(files) > MaxThemeEntries {
		return fmt.Errorf("the zip has %d entries; a theme may have at most %d", len(files), MaxThemeEntries)
	}
	var total uint64
	for _, file := range files {
		name := strings.TrimSuffix(file.Name, "/")
		if err := checkEntryName(name, file.FileInfo().IsDir()); err != nil {
			return err
		}
		if file.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%q is a link, which a theme may not contain", file.Name)
		}
		total += file.UncompressedSize64
		if total > MaxThemeBytes {
			return fmt.Errorf("the zip unpacks to more than %d MB; a theme may not be bigger", MaxThemeBytes>>20)
		}
	}
	return nil
}

// readZipEntry reads a file of a zip with the size read, not the size
// declared, capped.
func readZipEntry(file *zip.File, limit int64) ([]byte, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%q is bigger than allowed", file.Name)
	}
	return data, nil
}

// ReadThemeFile reads a file a theme names: a plain path, or an entry of a
// zip ("themes/a.zip!/fonts/x.ttf"), which is read in memory.
func ReadThemeFile(name string) ([]byte, error) {
	i := strings.Index(name, zipSep)
	if i < 0 {
		return os.ReadFile(name)
	}
	archive, entry := name[:i+len(".zip")], name[i+len(zipSep):]
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return nil, errors.New("the theme's zip file cannot be read")
	}
	defer reader.Close()
	for _, file := range reader.File {
		if file.Name == entry {
			return readZipEntry(file, MaxThemeBytes)
		}
	}
	return nil, fmt.Errorf("%s is not in the theme's zip file", entry)
}

// relativeInside cleans a path a theme file names and refuses one that is
// absolute or leaves the theme.
func relativeInside(field, rel string) (string, error) {
	if err := checkEntryName(rel, true); err != nil {
		return "", fmt.Errorf("%s: %v", field, err)
	}
	return path.Clean(rel), nil
}

// loadPacked reads a theme package: root is what its files' paths start
// with (a folder, or "x.zip!/"), has says whether a file exists in it.
func loadPacked(id, root string, text []byte, has func(string) bool) (ThemeFile, error) {
	theme, err := ParseTheme(id, string(text))
	if err != nil {
		return theme, err
	}
	resolve := func(field string, value *string, need bool) error {
		if *value == "" || (!need && !IsFontFile(*value)) {
			return nil
		}
		rel, err := relativeInside(field, *value)
		if err != nil {
			return err
		}
		if !has(rel) {
			return fmt.Errorf("%s: %s is not in the theme", field, rel)
		}
		*value = root + rel
		return nil
	}
	steps := []struct {
		field string
		value *string
		need  bool
	}{{"shader", &theme.Shader, true}, {"background_image", &theme.Image, true},
		{"font", &theme.Fonts.Regular, false}, {"font_bold", &theme.Fonts.Bold, false},
		{"font_italic", &theme.Fonts.Italic, false}, {"font_mono", &theme.Fonts.Mono, false}}
	for _, step := range steps {
		if err := resolve(step.field, step.value, step.need); err != nil {
			return theme, err
		}
	}
	return theme, nil
}

// pickToml finds the theme file at the root of a package.
func pickToml(names []string) (string, error) {
	var tomls []string
	for _, name := range names {
		if !strings.Contains(name, "/") && strings.EqualFold(path.Ext(name), ".toml") {
			if name == ThemeFileName {
				return name, nil
			}
			tomls = append(tomls, name)
		}
	}
	if len(tomls) == 1 {
		return tomls[0], nil
	}
	if len(tomls) == 0 {
		return "", errors.New("there is no " + ThemeFileName + " in it")
	}
	return "", errors.New("there are several .toml files in it: name the theme's " + ThemeFileName)
}

// loadZip reads a theme from a zip, in memory.
func loadZip(zipPath string) (ThemeFile, error) {
	id := strings.TrimSuffix(filepath.Base(zipPath), filepath.Ext(zipPath))
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return ThemeFile{}, errors.New("this is not a zip file 1loop can read")
	}
	defer reader.Close()
	if err := checkZip(reader.File); err != nil {
		return ThemeFile{}, err
	}
	var names []string
	files := map[string]*zip.File{}
	for _, file := range reader.File {
		if !file.FileInfo().IsDir() {
			names = append(names, file.Name)
			files[file.Name] = file
		}
	}
	name, err := pickToml(names)
	if err != nil {
		return ThemeFile{}, err
	}
	text, err := readZipEntry(files[name], maxThemeToml)
	if err != nil {
		return ThemeFile{}, err
	}
	theme, err := loadPacked(id, zipPath+"!/", text, func(rel string) bool { return files[rel] != nil })
	theme.Path = zipPath + "!/" + name
	return theme, err
}

// loadFolder reads a theme from a folder laid out like the zip.
func loadFolder(dir string) (ThemeFile, error) {
	id := filepath.Base(dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ThemeFile{}, err
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	name, err := pickToml(names)
	if err != nil {
		return ThemeFile{}, err
	}
	text, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ThemeFile{}, err
	}
	theme, err := loadPacked(id, dir+string(filepath.Separator), text, func(rel string) bool {
		info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
		return err == nil && !info.IsDir()
	})
	theme.Path = filepath.Join(dir, name)
	return theme, err
}

// WriteThemeZip writes a zip of files (name to content) at zipPath, after
// the same checks a zip is read with, through a temporary file so a theme
// being read never sees half of it.
func WriteThemeZip(zipPath string, files map[string][]byte) error {
	if len(files) > MaxThemeEntries {
		return fmt.Errorf("too many files for one theme (at most %d)", MaxThemeEntries)
	}
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var total int
	for _, name := range names {
		if err := checkEntryName(name, false); err != nil {
			return err
		}
		if total += len(files[name]); total > MaxThemeBytes {
			return fmt.Errorf("the theme is bigger than %d MB", MaxThemeBytes>>20)
		}
		entry, err := writer.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
		if err == nil {
			_, err = entry.Write(files[name])
		}
		if err != nil {
			return err
		}
	}
	if err := writer.Close(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(zipPath), 0o755); err != nil {
		return err
	}
	temp := zipPath + ".tmp"
	if err := os.WriteFile(temp, buffer.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(temp, zipPath)
}

// PackTheme packs the loose theme id of dir into dir/id.zip: its file as
// theme.toml, with the shader, picture and fonts it names and the licence
// files that go with those fonts. It then removes the loose theme file, the
// shader and the picture; fonts stay, other themes may use them.
func PackTheme(dir, id string) (string, error) {
	tomlPath := filepath.Join(dir, id+".toml")
	text, err := os.ReadFile(tomlPath)
	if err != nil {
		return "", err
	}
	theme, err := ParseTheme(id, string(text))
	if err != nil {
		return "", err
	}
	zipPath := filepath.Join(dir, id+".zip")
	if _, err := os.Stat(zipPath); err == nil {
		return "", fmt.Errorf("%s.zip already exists", id)
	}
	files := map[string][]byte{ThemeFileName: text}
	var move []string
	add := func(rel string) error {
		clean, err := relativeInside("file", rel)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(clean)))
		if err != nil {
			return fmt.Errorf("%s: %v", rel, errors.Unwrap(err))
		}
		files[clean] = data
		return nil
	}
	for _, rel := range []string{theme.Shader, theme.Image} {
		if rel != "" {
			if err := add(rel); err != nil {
				return "", err
			}
			move = append(move, rel)
		}
	}
	for _, font := range []string{theme.Fonts.Regular, theme.Fonts.Bold, theme.Fonts.Italic, theme.Fonts.Mono} {
		if !IsFontFile(font) {
			continue
		}
		if filepath.IsAbs(font) {
			return "", fmt.Errorf("font %q is outside the themes folder: copy it there first", font)
		}
		if err := add(font); err != nil {
			return "", err
		}
		stem := strings.SplitN(strings.TrimSuffix(filepath.Base(font), filepath.Ext(font)), "-", 2)[0]
		siblings, _ := filepath.Glob(filepath.Join(filepath.Dir(filepath.Join(dir, font)), stem+"*"))
		for _, sibling := range siblings {
			base := filepath.Base(sibling)
			if lower := strings.ToLower(base); strings.Contains(lower, "licen") || strings.Contains(lower, "ofl") {
				if rel, err := filepath.Rel(dir, sibling); err == nil {
					_ = add(filepath.ToSlash(rel))
				}
			}
		}
	}
	if err := WriteThemeZip(zipPath, files); err != nil {
		return "", err
	}
	for _, rel := range move {
		os.Remove(filepath.Join(dir, filepath.FromSlash(rel)))
	}
	os.Remove(tomlPath)
	return zipPath, nil
}

// UnpackTheme writes a theme zip or folder (src) into the new folder dest,
// to be edited by hand, with the same checks as reading it.
func UnpackTheme(src, dest string) error {
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("%s already exists", filepath.Base(dest))
	}
	var fsys fs.FS
	if strings.EqualFold(filepath.Ext(src), ".zip") {
		reader, err := zip.OpenReader(src)
		if err != nil {
			return errors.New("this is not a zip file 1loop can read")
		}
		defer reader.Close()
		if err := checkZip(reader.File); err != nil {
			return err
		}
		fsys = reader
	} else {
		fsys = os.DirFS(src)
	}
	var total, count int64
	return fs.WalkDir(fsys, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || name == "." {
			return err
		}
		if count++; count > MaxThemeEntries {
			return fmt.Errorf("a theme may have at most %d files", MaxThemeEntries)
		}
		if err := checkEntryName(name, entry.IsDir()); err != nil {
			return err
		}
		target := filepath.Join(dest, filepath.FromSlash(name))
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("%q is not a plain file", name)
		}
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		if total += int64(len(data)); total > MaxThemeBytes {
			return fmt.Errorf("the theme is bigger than %d MB", MaxThemeBytes>>20)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// Packaging says how a theme is stored: "zip", "folder" or "loose".
func (theme ThemeFile) Packaging() string {
	switch {
	case strings.Contains(theme.Path, zipSep):
		return "zip"
	case filepath.Base(theme.Path) != theme.ID+".toml":
		return "folder"
	}
	return "loose"
}

// PackagePath is the zip or folder of a packaged theme, or the file of a
// loose one.
func (theme ThemeFile) PackagePath() string {
	if i := strings.Index(theme.Path, zipSep); i >= 0 {
		return theme.Path[:i+len(".zip")]
	}
	if theme.Packaging() == "folder" {
		return filepath.Dir(theme.Path)
	}
	return theme.Path
}
