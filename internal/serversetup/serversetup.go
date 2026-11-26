// SPDX-License-Identifier: MPL-2.0

// Package serversetup holds the short guides to set up a headless 1loop
// server (#443): Docker, Docker Compose, the Linux program and the Windows
// program, each a few steps with the command to copy. The desktop's and the
// browser's Connect a server ask first whether a server already runs, and
// show the guide picked when none does; the phone shows the same from its
// copy of guides.json (android/app/src/main/assets/server_setup.json).
package serversetup

import (
	_ "embed"
	"encoding/json"
)

//go:embed guides.json
var guidesJSON []byte

// Link is a page to open from a step: a download, an installer.
type Link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// Step is one thing to do: a short sentence, and the command to copy or the
// page to open, when it has one.
type Step struct {
	Text    string `json:"text"`
	Command string `json:"command,omitempty"`
	Link    *Link  `json:"link,omitempty"`
}

// Method is one way to run a server, with its guide.
type Method struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Steps  []Step `json:"steps"`
}

var methods = func() []Method {
	var guides struct {
		Methods []Method `json:"methods"`
	}
	if err := json.Unmarshal(guidesJSON, &guides); err != nil {
		panic("serversetup: guides.json: " + err.Error())
	}
	return guides.Methods
}()

// Methods are the ways to run a server, in the order they are offered.
func Methods() []Method { return methods }

// JSON is guides.json as embedded, the phone's copy compared against it.
func JSON() []byte { return guidesJSON }
