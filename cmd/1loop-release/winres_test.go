// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"image"
	"image/color"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestVersionNumbers(t *testing.T) {
	for version, want := range map[string][4]uint16{
		"0.3.2":                   {0, 3, 2, 0},
		"0.3.2-alpha.518.0e20e7b": {0, 3, 2, 518},
		"v1.20.3":                 {1, 20, 3, 0},
	} {
		got, err := versionNumbers(version)
		if err != nil || got != want {
			t.Errorf("versionNumbers(%q) = %v, %v; want %v", version, got, err, want)
		}
	}
	if _, err := versionNumbers("dev"); err == nil {
		t.Error("versionNumbers(dev): no error")
	}
}

// The object is a COFF file the linker reads: one .rsrc section whose
// directory leads to the icons, their group and the version, each data
// entry relocated.
func TestWinresObject(t *testing.T) {
	var icons []image.Image
	for _, size := range []int{16, 32, 256} {
		icon := image.NewNRGBA(image.Rect(0, 0, size, size))
		icon.SetNRGBA(1, 1, color.NRGBA{R: 255, A: 128})
		icons = append(icons, icon)
	}
	data, err := winresObject(icons, winresInfo{Version: "0.3.2-alpha.518.0e20e7b", Description: "1loop", File: "1loop.exe"})
	if err != nil {
		t.Fatal(err)
	}
	file, err := pe.NewFile(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if file.Machine != pe.IMAGE_FILE_MACHINE_AMD64 || len(file.Sections) != 1 || file.Sections[0].Name != ".rsrc" {
		t.Fatalf("machine %#x, sections %v", file.Machine, file.Sections)
	}
	section := file.Sections[0]
	raw, err := section.Data()
	if err != nil {
		t.Fatal(err)
	}
	if len(section.Relocs) != 5 || len(file.Symbols) != 1 || file.Symbols[0].Name != ".rsrc" {
		t.Fatalf("%d relocations, symbols %v", len(section.Relocs), file.Symbols)
	}
	le := binary.LittleEndian
	// entries lists a directory's (ID, offset) pairs.
	entries := func(offset uint32) (out [][2]uint32) {
		count := uint32(le.Uint16(raw[offset+12:]) + le.Uint16(raw[offset+14:]))
		for i := uint32(0); i < count; i++ {
			at := offset + 16 + 8*i
			out = append(out, [2]uint32{le.Uint32(raw[at:]), le.Uint32(raw[at+4:])})
		}
		return out
	}
	found := map[uint32][][]byte{}
	for _, kind := range entries(0) {
		for _, id := range entries(kind[1] &^ (1 << 31)) {
			languages := entries(id[1] &^ (1 << 31))
			if len(languages) != 1 || languages[0][0] != langEnUS {
				t.Fatalf("type %d: languages %v", kind[0], languages)
			}
			entry := languages[0][1]
			at, size := le.Uint32(raw[entry:]), le.Uint32(raw[entry+4:])
			found[kind[0]] = append(found[kind[0]], raw[at:at+size])
		}
	}
	if len(found[rtIcon]) != 3 || len(found[rtGroupIcon]) != 1 || len(found[rtVersion]) != 1 {
		t.Fatalf("resources: %d icons, %d groups, %d versions", len(found[rtIcon]), len(found[rtGroupIcon]), len(found[rtVersion]))
	}
	if !bytes.HasPrefix(found[rtIcon][2], []byte("\x89PNG")) || le.Uint32(found[rtIcon][0]) != 40 {
		t.Error("expected DIBs below 256 and a PNG at 256")
	}
	if group := found[rtGroupIcon][0]; le.Uint16(group[4:]) != 3 || group[6+2*14] != 0 {
		t.Errorf("group directory % x", group[:20])
	}
	version := found[rtVersion][0]
	if int(le.Uint16(version)) != len(version) {
		t.Errorf("version length %d, resource %d bytes", le.Uint16(version), len(version))
	}
	words := make([]uint16, len(version)/2)
	binary.Read(bytes.NewReader(version), le, words)
	text := string(utf16.Decode(words))
	for _, want := range []string{"VS_VERSION_INFO", "FileVersion", "0.3.2-alpha.518.0e20e7b", "OriginalFilename", "1loop.exe", "Translation"} {
		if !strings.Contains(text, want) {
			t.Errorf("version resource lacks %q", want)
		}
	}
	if !bytes.Contains(version, []byte{0xBD, 0x04, 0xEF, 0xFE, 0, 0, 1, 0, 3, 0, 0, 0, 6, 2, 2, 0}) {
		t.Error("fixed file information: expected signature and version 0.3.2.518")
	}
}
