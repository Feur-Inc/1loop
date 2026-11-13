// SPDX-License-Identifier: MPL-2.0

//go:build windows && mesa

package softgl

import _ "embed"

// Written by packaging/windows/build.sh from the release's Mesa archive.
var (
	//go:embed opengl32.dll.gz
	openGL []byte
	//go:embed libgallium_wgl.dll.gz
	gallium []byte
)

func files() map[string][]byte {
	return map[string][]byte{"opengl32.dll": openGL, "libgallium_wgl.dll": gallium}
}
