// SPDX-License-Identifier: MPL-2.0

package main

// winres writes a Windows resource object (.syso) that go build links into
// an exe of the package it sits in: the launcher icon, which Explorer, the
// taskbar and the window show, and the version information of the file's
// Properties → Details. Written here rather than by windres, so the server,
// built without MinGW, gets it too.
//
// The object is a COFF file for x86-64 with one .rsrc section: the resource
// directory (type, then ID, then language), its data entries and their data.
// A data entry gives its data's address relative to the image: the linker
// fills it from a relocation against the section's symbol.

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"1loop/assets"
)

// Resource types and the language every resource is given (en-US).
const (
	rtIcon      = 3
	rtGroupIcon = 14
	rtVersion   = 16
	langEnUS    = 0x0409
)

// winresIconSizes are the sizes in the exe's icon: Explorer's views and the
// taskbar at 100 % to 200 %, and 256 for the large views.
var winresIconSizes = []int{16, 20, 24, 32, 40, 48, 64, 256}

// winresInfo is what the version information says.
type winresInfo struct {
	Version     string // as in VERSION, 0.3.2 or 0.3.2-alpha.518.0e20e7b
	Description string // FileDescription: the name the taskbar and Task Manager show
	File        string // OriginalFilename
}

func winres(args []string) {
	flags := flag.NewFlagSet("winres", flag.ExitOnError)
	version := flags.String("version", "", "the version, as in VERSION")
	description := flags.String("description", "1loop", "the file's description")
	file := flags.String("file", "", "the exe's name, 1loop.exe")
	out := flags.String("o", "", "the .syso to write")
	flags.Parse(args)
	if *version == "" || *file == "" || *out == "" || flags.NArg() != 0 {
		fail("usage: 1loop-release winres -version VERSION -file NAME.exe [-description TEXT] -o FILE.syso")
	}
	ring, err := png.Decode(bytes.NewReader(must(assets.Artwork.ReadFile("1loop-ring.png"))))
	if err != nil {
		fail("Cannot read the ring artwork: " + err.Error())
	}
	icons := make([]image.Image, 0, len(winresIconSizes))
	for _, size := range winresIconSizes {
		icons = append(icons, launcherIcon(ring, size))
	}
	data, err := winresObject(icons, winresInfo{Version: *version, Description: *description, File: *file})
	if err != nil {
		fail(err.Error())
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		fail(err.Error())
	}
}

// resource is one leaf of the directory: a type, an ID and its bytes.
type resource struct {
	kind, id uint16
	data     []byte
}

// winresObject is the .syso holding the icons (one group) and the version.
func winresObject(icons []image.Image, info winresInfo) ([]byte, error) {
	var resources []resource
	var group bytes.Buffer
	le := binary.LittleEndian
	binary.Write(&group, le, [3]uint16{0, 1, uint16(len(icons))}) // GRPICONDIR: reserved, type icon, count
	for i, icon := range icons {
		data, err := iconImage(icon)
		if err != nil {
			return nil, err
		}
		id := uint16(i + 1)
		resources = append(resources, resource{rtIcon, id, data})
		size := icon.Bounds().Dx()
		side := uint8(size)
		if size >= 256 {
			side = 0 // 0 means 256
		}
		// GRPICONDIRENTRY: width, height, colours, reserved, planes, bits, bytes, ID.
		group.Write([]byte{side, side, 0, 0})
		binary.Write(&group, le, uint16(1))
		binary.Write(&group, le, uint16(32))
		binary.Write(&group, le, uint32(len(data)))
		binary.Write(&group, le, id)
	}
	resources = append(resources, resource{rtGroupIcon, 1, group.Bytes()})
	version, err := versionInfo(info)
	if err != nil {
		return nil, err
	}
	resources = append(resources, resource{rtVersion, 1, version})
	return coffObject(resources), nil
}

// iconImage is one size of the icon as an icon resource holds it: PNG at
// 256, a 32-bit bottom-up DIB below (BITMAPINFOHEADER of twice the height,
// BGRA rows, then the 1-bit AND mask, all transparent through alpha).
func iconImage(icon image.Image) ([]byte, error) {
	bounds := icon.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width >= 256 {
		var out bytes.Buffer
		err := png.Encode(&out, icon)
		return out.Bytes(), err
	}
	var out bytes.Buffer
	le := binary.LittleEndian
	maskRow := (width + 31) / 32 * 4
	binary.Write(&out, le, struct {
		Size                     uint32
		Width, Height            int32
		Planes, BitCount         uint16
		Compression, ImageSize   uint32
		XPerMeter, YPerMeter     int32
		ColorsUsed, ColorsNeeded uint32
	}{40, int32(width), int32(height * 2), 1, 32, 0, uint32(width*height*4 + maskRow*height), 0, 0, 0, 0})
	for y := height - 1; y >= 0; y-- {
		for x := 0; x < width; x++ {
			// Straight (not premultiplied) alpha, as icons have it.
			r, g, b, a := icon.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			if a == 0 {
				out.Write([]byte{0, 0, 0, 0})
				continue
			}
			out.Write([]byte{uint8(b * 0xffff / a >> 8), uint8(g * 0xffff / a >> 8), uint8(r * 0xffff / a >> 8), uint8(a >> 8)})
		}
	}
	out.Write(make([]byte, maskRow*height))
	return out.Bytes(), nil
}

// versionNumbers are the four numbers of a version: 0.3.2 is 0.3.2.0, and
// an alpha, 0.3.2-alpha.518.0e20e7b, is 0.3.2.518.
func versionNumbers(version string) ([4]uint16, error) {
	var numbers [4]uint16
	main, rest, _ := strings.Cut(strings.TrimPrefix(version, "v"), "-")
	parts := strings.Split(main, ".")
	if len(parts) != 3 {
		return numbers, fmt.Errorf("version %q is not x.y.z", version)
	}
	for i, part := range parts {
		n, err := strconv.ParseUint(part, 10, 16)
		if err != nil {
			return numbers, fmt.Errorf("version %q is not x.y.z", version)
		}
		numbers[i] = uint16(n)
	}
	if fields := strings.Split(rest, "."); len(fields) > 1 && fields[0] == "alpha" {
		if n, err := strconv.ParseUint(fields[1], 10, 16); err == nil {
			numbers[3] = uint16(n)
		}
	}
	return numbers, nil
}

// versionInfo is the VS_VERSIONINFO resource: the fixed file information,
// then the strings (en-US, Unicode) and the translation they are in.
func versionInfo(info winresInfo) ([]byte, error) {
	numbers, err := versionNumbers(info.Version)
	if err != nil {
		return nil, err
	}
	ms := uint32(numbers[0])<<16 | uint32(numbers[1])
	ls := uint32(numbers[2])<<16 | uint32(numbers[3])
	var fixed bytes.Buffer
	binary.Write(&fixed, binary.LittleEndian, [13]uint32{
		0xFEEF04BD, 0x00010000, // signature, structure version
		ms, ls, ms, ls, // file and product version
		0x3F, 0, // flags mask, flags
		0x00040004, 1, 0, // VOS_NT_WINDOWS32, VFT_APP, no subtype
		0, 0, // date
	})
	strs := [][2]string{
		{"CompanyName", "Feur-Inc"},
		{"FileDescription", info.Description},
		{"FileVersion", info.Version},
		{"InternalName", strings.TrimSuffix(info.File, ".exe")},
		{"LegalCopyright", "MPL-2.0, https://github.com/Feur-Inc/1loop"},
		{"OriginalFilename", info.File},
		{"ProductName", "1loop"},
		{"ProductVersion", info.Version},
	}
	var table [][]byte
	for _, s := range strs {
		value := utf16z(s[1])
		table = append(table, versionNode(s[0], value, 1, len(value)/2))
	}
	stringFileInfo := versionNode("StringFileInfo", nil, 1, 0, versionNode("040904B0", nil, 1, 0, table...))
	var translation bytes.Buffer
	binary.Write(&translation, binary.LittleEndian, [2]uint16{langEnUS, 1200}) // en-US, Unicode
	varFileInfo := versionNode("VarFileInfo", nil, 1, 0, versionNode("Translation", translation.Bytes(), 0, translation.Len()))
	return versionNode("VS_VERSION_INFO", fixed.Bytes(), 0, fixed.Len(), stringFileInfo, varFileInfo), nil
}

// versionNode is one block of a version resource: its length, the length
// of its value (in WORDs for text, bytes for binary), its type (1 text,
// 0 binary), its key, the value and its children, each 32-bit aligned.
func versionNode(key string, value []byte, kind, valueLength int, children ...[]byte) []byte {
	var out bytes.Buffer
	out.Write(make([]byte, 6)) // the three header WORDs, filled below
	out.Write(utf16z(key))
	pad4(&out)
	out.Write(value)
	for _, child := range children {
		pad4(&out)
		out.Write(child)
	}
	data := out.Bytes()
	binary.LittleEndian.PutUint16(data[0:], uint16(len(data)))
	binary.LittleEndian.PutUint16(data[2:], uint16(valueLength))
	binary.LittleEndian.PutUint16(data[4:], uint16(kind))
	return data
}

func utf16z(text string) []byte {
	words := append(utf16.Encode([]rune(text)), 0)
	out := make([]byte, 2*len(words))
	for i, word := range words {
		binary.LittleEndian.PutUint16(out[2*i:], word)
	}
	return out
}

func pad4(out *bytes.Buffer) {
	for out.Len()%4 != 0 {
		out.WriteByte(0)
	}
}

// coffObject lays the resources out in a .rsrc section: the root directory
// of types, one directory of IDs per type, one of languages per resource,
// the data entries, then the data, 8-byte aligned. Every data entry's
// address gets a relocation (IMAGE_REL_AMD64_ADDR32NB) against the
// section's symbol.
func coffObject(resources []resource) []byte {
	sort.Slice(resources, func(i, j int) bool {
		if resources[i].kind != resources[j].kind {
			return resources[i].kind < resources[j].kind
		}
		return resources[i].id < resources[j].id
	})
	var kinds []uint16
	byKind := map[uint16][]int{}
	for i, r := range resources {
		if len(byKind[r.kind]) == 0 {
			kinds = append(kinds, r.kind)
		}
		byKind[r.kind] = append(byKind[r.kind], i)
	}
	const dirHeader, dirEntry, dataEntry = 16, 8, 16
	// Offsets of each part, in the order they are written.
	rootSize := dirHeader + dirEntry*len(kinds)
	kindDir := map[uint16]int{}
	at := rootSize
	for _, kind := range kinds {
		kindDir[kind] = at
		at += dirHeader + dirEntry*len(byKind[kind])
	}
	langDir := make([]int, len(resources))
	for i := range resources {
		langDir[i] = at
		at += dirHeader + dirEntry
	}
	entry := make([]int, len(resources))
	for i := range resources {
		entry[i] = at
		at += dataEntry
	}
	data := make([]int, len(resources))
	for i, r := range resources {
		at = (at + 7) &^ 7
		data[i] = at
		at += len(r.data)
	}
	section := make([]byte, (at+7)&^7)
	le := binary.LittleEndian
	directory := func(offset, ids int) {
		le.PutUint16(section[offset+14:], uint16(ids)) // NumberOfIdEntries
	}
	child := func(offset int, id uint16, target int, subdirectory bool) {
		le.PutUint32(section[offset:], uint32(id))
		if subdirectory {
			target |= 1 << 31
		}
		le.PutUint32(section[offset+4:], uint32(target))
	}
	directory(0, len(kinds))
	for k, kind := range kinds {
		child(dirHeader+dirEntry*k, kind, kindDir[kind], true)
		directory(kindDir[kind], len(byKind[kind]))
		for n, i := range byKind[kind] {
			child(kindDir[kind]+dirHeader+dirEntry*n, resources[i].id, langDir[i], true)
		}
	}
	var relocations []int
	for i, r := range resources {
		directory(langDir[i], 1)
		child(langDir[i]+dirHeader, langEnUS, entry[i], false)
		le.PutUint32(section[entry[i]:], uint32(data[i])) // relocated: + the section's address
		le.PutUint32(section[entry[i]+4:], uint32(len(r.data)))
		relocations = append(relocations, entry[i])
		copy(section[data[i]:], r.data)
	}

	const fileHeader, sectionHeader, relocation, symbol = 20, 40, 10, 18
	sectionAt := fileHeader + sectionHeader
	relocationsAt := sectionAt + len(section)
	symbolsAt := relocationsAt + relocation*len(relocations)
	var out bytes.Buffer
	binary.Write(&out, le, struct {
		Machine, Sections                   uint16
		Time, Symbols, SymbolCount          uint32
		OptionalHeaderSize, Characteristics uint16
	}{0x8664, 1, 0, uint32(symbolsAt), 1, 0, 0})
	binary.Write(&out, le, struct {
		Name                                 [8]byte
		VirtualSize, VirtualAddress, RawSize uint32
		RawAt, RelocationsAt, LineNumbersAt  uint32
		RelocationCount, LineNumberCount     uint16
		Characteristics                      uint32
	}{[8]byte{'.', 'r', 's', 'r', 'c'}, 0, 0, uint32(len(section)), uint32(sectionAt), uint32(relocationsAt), 0,
		uint16(len(relocations)), 0, 0x40000040}) // initialized data, readable
	out.Write(section)
	for _, offset := range relocations {
		binary.Write(&out, le, struct {
			Address, Symbol uint32
			Type            uint16
		}{uint32(offset), 0, 3}) // IMAGE_REL_AMD64_ADDR32NB
	}
	binary.Write(&out, le, struct {
		Name       [8]byte
		Value      uint32
		Section    int16
		Type       uint16
		Class, Aux uint8
	}{[8]byte{'.', 'r', 's', 'r', 'c'}, 0, 1, 0, 3, 0}) // static, section 1
	binary.Write(&out, le, uint32(4)) // empty string table
	return out.Bytes()
}
