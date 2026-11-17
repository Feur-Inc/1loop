// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"context"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"1loop/internal/agent"
	"1loop/internal/platform"
)

// make runs the dialog's Create for its name, as submit does off the
// interface's thread, and keeps what it made.
func makeProject(t *testing.T, form *projectForm, name string) madeProject {
	t.Helper()
	run, err := form.maker(name)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	made, err := run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	form.ui.keepMade(form.target, made, form.env)
	return made
}

// A project of this computer made from a folder keeps its variables, and
// a chat there gets them for its run and its terminals; another project
// gets none. Removing the project forgets them.
func TestNewProjectKeepsItsVariables(t *testing.T) {
	ui := addProjectUI(t)
	folder := t.TempDir()
	form := ui.newProjectForm(localTarget)
	form.addFolderRow(folder)
	if form.name.Text != filepath.Base(folder) {
		t.Errorf("the name is not the folder's: %q", form.name.Text)
	}
	form.env = map[string]string{"API_TOKEN": "hidden"}
	made := makeProject(t, form, form.name.Text)
	if made.open != folder || !slices.Contains(ui.projects, folder) {
		t.Fatalf("made %+v, projects %q", made, ui.projects)
	}
	if env := ui.runEnv(agent.Config{Workspace: folder}); !maps.Equal(env, form.env) {
		t.Fatalf("a chat of the project gets %v", env)
	}
	if env := ui.folderEnv(folder, ""); !maps.Equal(env, form.env) {
		t.Fatalf("a terminal of the project gets %v", env)
	}
	if env := ui.runEnv(agent.Config{Workspace: "/home/u/app"}); len(env) != 0 {
		t.Fatalf("another project gets %v", env)
	}
	if env := ui.runEnv(agent.Config{Workspace: folder, MachineID: "m1"}); len(env) != 0 {
		t.Fatalf("the same path on an SSH machine gets %v", env)
	}
	ui.keepProjectEnv(localTarget, folder, nil)
	if len(ui.projectEnv) != 0 {
		t.Fatalf("forgotten variables kept: %v", ui.projectEnv)
	}
}

// With no folder, Create makes one of that name beside the last project.
func TestNewProjectMakesAFolder(t *testing.T) {
	ui := addProjectUI(t)
	base := t.TempDir()
	ui.projects = []string{filepath.Join(base, "app")}
	ui.localWorkspace = ""
	form := ui.newProjectForm(localTarget)
	if _, err := folderName("a/b"); err == nil {
		t.Error("a name with / accepted")
	}
	made := makeProject(t, form, "fresh")
	want, _ := filepath.EvalSymlinks(filepath.Join(base, "fresh"))
	if info, err := os.Stat(want); err != nil || !info.IsDir() || made.open != want || !slices.Contains(ui.projects, want) {
		t.Fatalf("made %+v (%v)", made, err)
	}
}

// A folder's row says its branch; Worktree makes a new worktree of it on
// the branch named after the project, which opens, and whose chats get
// the project's variables.
func TestNewProjectWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	ui := addProjectUI(t)
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "trunk"}, {"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "first"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	repo, _ = filepath.EvalSymlinks(repo)
	if branch, err := folderBranch(context.Background(), repo); err != nil || branch != "trunk" {
		t.Fatalf("branch %q %v", branch, err)
	}
	if _, err := folderBranch(context.Background(), t.TempDir()); err == nil {
		t.Error("a folder outside Git has a branch")
	}
	form := ui.newProjectForm(localTarget)
	form.addFolderRow(repo)
	form.folders[0].worktree.SetChecked(true)
	form.env = map[string]string{"STAGE": "dev"}
	made := makeProject(t, form, "my feature")
	if made.open == repo || made.worktrees[made.open] != repo || ui.worktrees[made.open] != repo {
		t.Fatalf("made %+v, worktrees %v", made, ui.worktrees)
	}
	if branch, _ := folderBranch(context.Background(), made.open); branch != "my-feature" {
		t.Errorf("the worktree's branch is %q", branch)
	}
	if slices.Contains(ui.projects, made.open) || !slices.Contains(ui.projects, repo) {
		t.Errorf("projects %q", ui.projects)
	}
	if env := ui.runEnv(agent.Config{Workspace: made.open}); env["STAGE"] != "dev" {
		t.Errorf("the worktree's chat gets %v", env)
	}
}

// The dialog offers every linked target, and lists folders of this
// computer only when the project lives there.
func TestNewProjectLocations(t *testing.T) {
	ui := addProjectUI(t)
	form := ui.newProjectForm(localTarget)
	if !slices.Equal(form.location.Options, []string{"This computer", "build-box", "box.lan"}) {
		t.Errorf("locations %q", form.location.Options)
	}
	if form.location.Selected != "This computer" || !slices.Contains(visibleTexts(form.dialog.content), "Folders (optional)") {
		t.Errorf("this computer's dialog: %q, %q", form.location.Selected, visibleTexts(form.dialog.content))
	}
	form.location.SetSelected("box.lan")
	if form.target != serverTarget(testServer) || slices.Contains(visibleTexts(form.dialog.content), "Folders (optional)") {
		t.Errorf("choosing the server: %v", form.target)
	}
}

// The editor lists the variables with their values hidden, refuses a name
// a shell would not take, and saves what it lists.
func TestEnvEditor(t *testing.T) {
	ui := addProjectUI(t)
	var saved map[string]string
	dialog := ui.envEditor("Environment variables", map[string]string{"B": "2", "A": "1"}, func(env map[string]string) { saved = env })
	entries := visibleEntries(dialog.content)
	if len(entries) != 4 || entries[0].Text != "A" || entries[2].Text != "B" || !entries[1].Password || !entries[3].Password {
		t.Fatalf("the editor's fields: %d", len(entries))
	}
	entries[2].SetText("2BAD")
	tapText(t, dialog.content, "Save")
	if saved != nil || !strings.Contains(strings.Join(visibleTexts(dialog.content), " "), "is not a variable name") {
		t.Fatal("an invalid name was saved")
	}
	entries[2].SetText("B")
	tapText(t, dialog.content, "Save")
	if !maps.Equal(saved, map[string]string{"A": "1", "B": "2"}) {
		t.Fatalf("saved %v", saved)
	}
	if summary := envSummary(saved); summary != "2 variables: A, B" {
		t.Errorf("summary %q", summary)
	}
	if env := platform.EnvPairs(saved); !slices.Equal(env, []string{"A=1", "B=2"}) {
		t.Errorf("pairs %q", env)
	}
}
