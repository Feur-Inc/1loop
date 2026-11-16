// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The example shaders compile, with the prelude, as GLSL 1.10 and GLSL ES
// 1.00: what the painter hands the driver. Skipped without glslangValidator.
func TestExampleShadersCompile(t *testing.T) {
	validator, err := exec.LookPath("glslangValidator")
	if err != nil {
		t.Skip("no glslangValidator")
	}
	shaders, _ := filepath.Glob("../../docs/themes/*.frag")
	if len(shaders) == 0 {
		t.Fatal("no example shaders")
	}
	dir := t.TempDir()
	for _, path := range shaders {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for name, header := range map[string]string{"core.frag": "#version 110\n", "es.frag": "#version 100\nprecision mediump float;\n"} {
			out := filepath.Join(dir, filepath.Base(path)+"."+name)
			os.WriteFile(out, []byte(header+shaderPrelude+string(body)), 0o644)
			if msg, err := exec.Command(validator, out).CombinedOutput(); err != nil {
				t.Errorf("%s (%s): %s", filepath.Base(path), name, msg)
			}
		}
	}
}
