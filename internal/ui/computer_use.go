package ui

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"fyne.io/fyne/v2"

	"1loop/internal/agent"
	"1loop/internal/computeruse"
	"1loop/internal/platform"
)

// computerSession is the desktop's one sharing session, made on first use.
func (ui *desktopUI) computerSession() *computeruse.ComputerSession {
	if ui.computer == nil {
		ui.computer = &computeruse.ComputerSession{Lifetime: ui.ctx, Instructions: map[string]bool{}}
	}
	return ui.computer
}

func (ui *desktopUI) computerTools(chat *conversation) agent.PanelToolHandler {
	session := ui.computerSession()
	return func(ctx context.Context, name string, raw json.RawMessage) (agent.PanelToolResult, error) {
		var request struct{ Action, Arguments string }
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			return agent.PanelToolResult{}, err
		}
		var arguments map[string]any
		if err := json.Unmarshal([]byte(request.Arguments), &arguments); err != nil || arguments == nil {
			return agent.PanelToolResult{}, fmt.Errorf("arguments must encode a JSON object")
		}
		if err := ctx.Err(); err != nil {
			return agent.PanelToolResult{}, err
		}
		if request.Action == "stop" {
			// Stop must cancel an in-flight action before waiting for its lock.
			// Pending stops also prevent another queued action from starting.
			session.OperationMu.Lock()
			session.Stopping++
			if session.CancelAction != nil {
				session.CancelAction()
			}
			session.OperationMu.Unlock()
			defer func() {
				session.OperationMu.Lock()
				session.Stopping--
				session.OperationMu.Unlock()
			}()
		}
		session.Mu.Lock()
		defer session.Mu.Unlock()
		if ctx.Err() != nil {
			return agent.PanelToolResult{}, ctx.Err()
		}
		if request.Action == "instructions" {
			session.Instructions[chat.ID] = true
			return agent.PanelToolResult{Text: computeruse.ComputerGuideText()}, nil
		}
		if request.Action == "stop" {
			if session.Portal != nil {
				session.Portal.Close()
				session.Portal = nil
			}
			return agent.PanelToolResult{Text: "Computer Use sharing stopped."}, nil
		}
		if !session.Instructions[chat.ID] {
			return agent.PanelToolResult{}, fmt.Errorf("Read action=instructions before starting Computer Use")
		}
		operation, cancel := context.WithTimeout(ctx, 130*time.Second)
		session.OperationMu.Lock()
		if session.Stopping > 0 {
			session.OperationMu.Unlock()
			cancel()
			return agent.PanelToolResult{}, fmt.Errorf("Computer Use sharing is being stopped")
		}
		session.CancelAction = cancel
		session.OperationMu.Unlock()
		defer func() {
			session.OperationMu.Lock()
			session.CancelAction = nil
			session.OperationMu.Unlock()
			cancel()
		}()
		if request.Action == "start" {
			return session.StartLocked(operation, false)
		}
		if session.Portal == nil {
			return agent.PanelToolResult{}, fmt.Errorf("Call start and approve desktop sharing before using Computer Use")
		}
		if _, err := session.Portal.Call(operation, "status", nil); err != nil {
			session.Portal.Close()
			session.Portal = nil
			return agent.PanelToolResult{}, err
		}
		switch request.Action {
		case "screenshot", "click", "move", "scroll", "drag", "press_key", "type_text":
			if request.Action == "press_key" || request.Action == "type_text" {
				target := map[string]any{}
				for _, key := range []string{"window_id", "pid", "app_id", "wm_class", "title"} {
					if value, ok := arguments[key]; ok {
						target[key] = value
						delete(arguments, key)
					}
				}
				if len(target) > 0 {
					backend, err := session.Backend(operation)
					if err != nil {
						return agent.PanelToolResult{}, err
					}
					result, err := backend.Call(operation, "activate_window", target)
					if err != nil {
						return result, err
					}
					var focus struct{ OK bool }
					if json.Unmarshal([]byte(result.Text), &focus) != nil || !focus.OK {
						return agent.PanelToolResult{}, fmt.Errorf("Could not verify target focus: %s", result.Text)
					}
				}
			}
			return session.Portal.Call(operation, request.Action, arguments)
		case "get_app_state":
			arguments["include_screenshot"] = false
			backend, err := session.Backend(operation)
			var result agent.PanelToolResult
			if err == nil {
				result, err = backend.Call(operation, "get_app_state", arguments)
			}
			if err != nil {
				result.Text = "Accessibility unavailable: " + err.Error()
			} else {
				session.StateChat = chat.ID
			}
			screen, screenErr := session.Portal.Call(operation, "screenshot", map[string]any{})
			result.Text += "\n" + screen.Text
			result.Images = screen.Images
			return result, screenErr
		case "describe", "doctor", "list_windows", "focused_window", "activate_window", "perform_action", "set_value", "move_window", "resize_window", "setup_accessibility":
			backend, err := session.Backend(operation)
			if err != nil {
				return agent.PanelToolResult{}, err
			}
			if (request.Action == "perform_action" || request.Action == "set_value") && session.StateChat != chat.ID {
				return agent.PanelToolResult{}, fmt.Errorf("Read get_app_state in this chat before using accessibility selectors")
			}
			if request.Action == "describe" {
				data, err := backend.Request(operation, "tools/list", map[string]any{})
				return agent.PanelToolResult{Text: string(data)}, err
			}
			return backend.Call(operation, request.Action, arguments)
		default:
			return agent.PanelToolResult{}, fmt.Errorf("Unknown Computer Use action: %s", request.Action)
		}
	}
}

// chooseComputerScreen opens GNOME's dialog again to share another screen;
// the agent then works on that one. done runs on the interface thread.
func (ui *desktopUI) chooseComputerScreen(done func(error)) {
	session := ui.computerSession()
	go func() {
		session.Mu.Lock()
		ctx, cancel := context.WithTimeout(session.Lifetime, 130*time.Second)
		_, err := session.StartLocked(ctx, true)
		cancel()
		session.Mu.Unlock()
		fyne.Do(func() { done(err) })
	}()
}

// forgetComputerScreen stops sharing and revokes the remembered permission:
// the next use of Computer Use asks again.
func (ui *desktopUI) forgetComputerScreen() error {
	session := ui.computerSession()
	session.Mu.Lock()
	defer session.Mu.Unlock()
	if session.Portal != nil {
		session.Portal.Close()
		session.Portal = nil
	}
	if grant := computeruse.LoadComputerGrant(); grant.Token != "" && runtime.GOOS == "linux" {
		// The portal's own record of the permission. Without the token it can
		// no longer be used, so a failure here only leaves a dead entry.
		exec.Command("gdbus", "call", "--session", "--dest", "org.freedesktop.impl.portal.PermissionStore",
			"--object-path", "/org/freedesktop/impl/portal/PermissionStore",
			"--method", "org.freedesktop.impl.portal.PermissionStore.Delete", "remote-desktop", grant.Token).Run()
	}
	platform.Devf("computer", "screen permission forgotten")
	return computeruse.SaveComputerGrant(computeruse.ComputerGrant{})
}
