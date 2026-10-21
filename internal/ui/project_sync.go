package ui

// Copying a project with its chats between this desktop and a paired
// engine (projectsync): the desktop's side for the phone and servers, and
// the remote desktop's Copy project dialog.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"

	"1loop/internal/agent"
	"1loop/internal/core"
	"1loop/internal/projectsync"
)

// syncEngine is this desktop's projects and chats, for a copy. Its calls
// come from other goroutines: the window's state is read on its thread.
func (ui *desktopUI) syncEngine() *projectsync.Local {
	onUI := func(f func()) {
		if ui.ctx.Err() == nil {
			fyne.DoAndWait(f)
		}
	}
	device, _ := os.Hostname()
	if device == "" {
		device = "Desktop"
	}
	identity := ""
	if ui.remote != nil {
		identity = ui.remote.fingerprint
	}
	return &projectsync.Local{
		Device:   device,
		Identity: identity,
		Bases:    &projectsync.BaseStore{Path: filepath.Join(filepath.Dir(ui.statePath), "sync-bases.json")},
		Resolve: func(project string, create bool) (string, error) {
			var found string
			var problem error
			onUI(func() {
				folders := append([]string{ui.cfg.Workspace}, ui.projects...)
				for _, folder := range folders {
					if folder == project && folder != "" {
						found = folder
						break
					}
				}
				if found == "" && project == filepath.Base(project) {
					for _, folder := range folders {
						if folder != "" && filepath.Base(folder) == project {
							if found != "" && found != folder {
								problem = fmt.Errorf("More than one project has this name: select its full folder")
								return
							}
							found = folder
						}
					}
				}
				for _, chat := range ui.chats {
					if chat.Config.Workspace == found && chat.run != nil {
						problem = fmt.Errorf("Stop the running project conversations before copying")
						return
					}
				}
			})
			if problem != nil {
				return "", problem
			}
			if found != "" {
				return found, nil
			}
			if !create {
				return "", fmt.Errorf("No project %q on this computer: %w", project, os.ErrNotExist)
			}

			name := filepath.Base(filepath.Clean(project))
			if name == "." || name == "/" || strings.HasPrefix(name, ".") {
				return "", fmt.Errorf("Invalid project name")
			}
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			// A project copied here goes in ~/1loop-projects.
			folder := filepath.Join(home, "1loop-projects", name)
			if err := os.MkdirAll(folder, 0755); err != nil {
				return "", err
			}
			onUI(func() {
				ui.ensureProject(folder)
				ui.save()
				ui.renderChats()
			})
			return folder, nil
		},
		Chats: func(workspace string) []projectsync.Chat {
			var out []projectsync.Chat
			onUI(func() {
				for _, chat := range ui.chats {
					if chat.Config.Workspace == workspace {
						out = append(out, (projectsync.Chat{ID: chat.ID, Title: chat.Title, Config: chat.Config, History: chat.History, Items: chat.Items, Todo: chat.Todo, Version: chat.Version, Versions: chat.Versions}).Portable())
					}
				}
			})
			return out
		},
		AddChat: func(workspace string, copied projectsync.Chat, conflict bool) (string, error) {
			var problem error
			onUI(func() {
				existing := ui.chatByID(copied.ID)
				if conflict || existing != nil && existing.Config.Workspace != workspace {
					copied.ID = core.Identifier()[:16]
					copied.Title += " (copy)"
					existing = nil
				}
				if existing != nil {
					if existing.run != nil {
						problem = fmt.Errorf("“%s” is running here: stop it before copying", existing.Title)
						return
					}
					// The chat goes on here with this desktop's model; a Claude
					// Code session here reads what happened elsewhere.
					cfg := existing.Config
					cfg.RanWith = copiedRanWith(copied.Config)
					copied = copied.AtWorkspace(cfg)
					existing.Config.Session, existing.Config.SessionMark = "", 0
					existing.Cache = &agent.ContextCache{}
					existing.Title, existing.History, existing.Items = copied.Title, copied.History, copied.Items
					existing.Todo, existing.Version, existing.Versions = copied.Todo, copied.Version, copied.Versions
					existing.Config.RanWith = copiedRanWith(copied.Config)
					if existing == ui.current {
						ui.reloadChat(existing)
					}
					return
				}
				cfg := ui.cfg
				cfg.Workspace, cfg.Session, cfg.SessionMark = workspace, "", 0
				cfg.RanWith = copiedRanWith(copied.Config) // its next turn here goes through a handoff
				copied = copied.AtWorkspace(cfg)
				ui.chats = append([]*conversation{{ID: copied.ID, Title: copied.Title, Config: cfg, History: copied.History, Items: copied.Items,
					Todo: copied.Todo, Version: copied.Version, Versions: copied.Versions}}, ui.chats...)
			})
			return copied.Title, problem
		},
		Saved: func() error {
			var err error
			onUI(func() {
				ui.persist(true)
				if ui.saveError != "" {
					err = fmt.Errorf("%s", ui.saveError)
				}
				ui.renderChats()
			})
			return err
		},
	}
}

func copiedRanWith(cfg agent.Config) *agent.RunIdentity {
	if cfg.RanWith != nil {
		identity := *cfg.RanWith
		return &identity
	}
	if cfg.Provider == "" {
		return nil
	}
	identity := agent.IdentityOf(cfg)
	return &identity
}

// copyProject is the remote desktop's Copy project dialog: the server's
// project to this computer, or this computer's to the server, showing what
// a copy sends before it does.
func (remote *remoteDesktop) copyProject() {
	ui := remote.parent
	peer := &projectsync.Peer{URL: remote.client.address.URL, Token: remote.client.address.Token, Fingerprint: remote.client.address.Fingerprint}
	serverProject := remote.workspace()
	if serverProject == "" {
		serverProject = filepath.Base(ui.cfg.Workspace) // the server's project of the same name
	}
	localProject := ui.cfg.Workspace
	bases := &projectsync.BaseStore{Path: filepath.Join(filepath.Dir(ui.statePath), "sync-bases.json")}
	status := note("")
	var dialog *sheet
	var run func(pull, planOnly bool)
	run = func(pull, planOnly bool) {
		status.SetText("Comparing both sides…")
		go func() {
			ctx, cancel := context.WithTimeout(remote.ctx, time.Hour)
			defer cancel()
			var report projectsync.Report
			var err error
			if pull {
				report, err = projectsync.Copy(ctx, peer, serverProject, ui.syncEngine(), filepath.Base(serverProject), bases, planOnly)
			} else {
				report, err = projectsync.Copy(ctx, ui.syncEngine(), localProject, peer, filepath.Base(localProject), bases, planOnly)
			}
			fyne.Do(func() {
				switch {
				case err != nil:
					status.SetText(err.Error())
				case planOnly && !report.Plan.Empty():
					status.SetText("Will copy: " + report.Summary())
					confirm := newRow("Copy now", nil, stylePrimary, nil)
					confirm.onTap = func() { confirm.Disable(); run(pull, false) }
					dialog.Hide()
					dialog = showModal(remote.window.Canvas(), "Copy project", container.NewVBox(status), 620, nil, newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() }), confirm)
				case planOnly:
					status.SetText(report.Summary())
				default:
					text := "Copied: " + report.Summary() + "\nTo " + report.Workspace
					if len(report.Result.Conflicts) > 0 {
						text += "\nKept twice: " + strings.Join(report.Result.Conflicts, ", ")
					}
					status.SetText(text)
					remote.notify()
				}
			})
		}()
	}
	toHere := newRow("Bring “"+filepath.Base(serverProject)+"” to this computer", nil, styleOutline, func() { run(true, true) })
	toServer := newRow("Send “"+filepath.Base(localProject)+"” to the server", nil, styleOutline, func() { run(false, true) })
	body := container.NewVBox(
		note("Copies the project's files and its conversations. Each copy sends only what changed since the last one; what changed on both sides is kept twice, and a file deleted on one side goes to the other side's trash. Local credentials, .env files, private keys and generated caches are excluded. Links are not copied. Compare again after an interruption to retry only what is still missing."),
		toHere, toServer, status)
	dialog = showModal(remote.window.Canvas(), "Copy project", body, 620, nil)
}
