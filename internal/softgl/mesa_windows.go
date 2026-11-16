// SPDX-License-Identifier: MPL-2.0

//go:build windows

package softgl

import "embed"

// Written by go generate (fetch/), from the pinned Mesa archive: a build
// for Windows without them stops here, saying the pattern matches no file.
var (
	//go:embed opengl32.dll.xz
	openGL []byte
	//go:embed libgallium_wgl.dll.xz
	gallium []byte
	//go:embed licenses
	licenses embed.FS
)

func files() map[string][]byte {
	return map[string][]byte{"opengl32.dll": openGL, "libgallium_wgl.dll": gallium}
}
