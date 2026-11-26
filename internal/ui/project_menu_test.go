// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"strings"
	"testing"

	"1loop/internal/gitrepo"
)

// A project's menu has the same entries whether the window works on its
// target or not; the target only takes away what it cannot do (#607).
func TestProjectMenuIsTheSameEverywhere(t *testing.T) {
	const project, server = "/src/app", "https://srv:47101"
	labels := func(actions []menuAction) string {
		var out []string
		for _, action := range actions {
			out = append(out, action.label)
		}
		return strings.Join(out, ", ")
	}
	const all = "Project memory…, Theme…, Environment variables…"
	const git = "New worktree…, Repository…, "
	cases := []struct {
		name   string
		target projectTarget
		repo   bool
		want   string
	}{
		{"this computer", localTarget, false, all + ", Rename, Remove from 1loop"},
		{"this computer, Git", localTarget, true, git + all + ", Rename, Remove from 1loop"},
		{"SSH machine", sshTarget("box"), true, all + ", Remove from 1loop"},
		{"server", serverTarget(server), false, all},
		{"server, Git", serverTarget(server), true, git + all},
	}
	for _, c := range cases {
		for _, active := range []bool{false, true} {
			ui := &desktopUI{desktopApp: &desktopApp{}}
			ui.repos = map[string]*repoState{}
			if c.repo {
				key := "local"
				if c.target.kind == targetServer {
					key = server
				}
				ui.repos[key+"\x00"+project] = &repoState{repo: &gitrepo.Repository{}}
			}
			if active {
				switch c.target.kind {
				case targetServer:
					ui.server = &remoteDesktop{client: &remoteClient{address: serverAddress{URL: server}}}
				case targetSSH:
					ui.cfg.MachineID = c.target.id
				}
			}
			got := labels(ui.projectActionsOn(targetGroup{target: c.target, active: active}, project))
			if got != c.want {
				t.Errorf("%s, active %v:\n got %s\nwant %s", c.name, active, got, c.want)
			}
		}
	}
}
