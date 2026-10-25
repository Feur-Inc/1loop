package ui

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/agent"
)

func (ui *desktopUI) chatGPTWebSettings(draft *agent.Connection, say func(string)) fyne.CanvasObject {
	if draft.WebSettings == nil {
		draft.WebSettings = &agent.WebSettings{}
	}
	settings := draft.WebSettings
	tunnel, keyFile, project := widget.NewEntry(), widget.NewEntry(), widget.NewEntry()
	tunnel.SetText(settings.TunnelID)
	keyFile.SetText(settings.KeyFile)
	project.SetText(settings.ProjectURL)
	tunnel.SetPlaceHolder("tunnel_…")
	keyFile.SetPlaceHolder("Path to a private file containing the tunnel key")
	project.SetPlaceHolder("Created automatically during setup")
	project.Disable()
	tunnel.OnChanged = func(s string) { settings.TunnelID = s }
	keyFile.OnChanged = func(s string) { settings.KeyFile = s }
	if keyFile.Text == "" {
		dir, _ := os.UserConfigDir()
		keyFile.SetText(filepath.Join(dir, "1loop", "chatgpt-web-tunnel.key"))
	}
	status := widget.NewLabel("Tunnel not started.")
	status.Wrapping = fyne.TextWrapWord
	account := func() (*agent.ChatGPTWeb, error) {
		if draft.AuthProfile == draft.ID { // Reuse the initial setup profile for the first Web account only.
			first := true
			for _, conn := range ui.connectionsList {
				if conn.Provider == "chatgptweb" && conn.ID != draft.ID {
					first = false
				}
			}
			if first {
				draft.AuthProfile = "default"
			}
		}
		return ui.backend.WebAccount(draft.Config(""))
	}
	open := newRow("Open ChatGPT sign-in", iconBrowser, styleOutline, func() {
		bridge, err := account()
		if err != nil {
			say(err.Error())
			return
		}
		if ui.webSetupWindows == nil {
			ui.webSetupWindows = make(map[*agent.ChatGPTWeb]fyne.Window)
		}
		if window := ui.webSetupWindows[bridge]; window != nil {
			window.Show()
			window.RequestFocus()
			return
		}
		window := fyne.CurrentApp().NewWindow("ChatGPT sign-in · 1loop")
		ui.webSetupWindows[bridge] = window
		view := ui.webBrowserWindow(bridge, "setup", "https://chatgpt.com/", window, true)
		window.SetContent(view)
		window.Resize(fyne.NewSize(1100, 800))
		window.SetOnClosed(func() { bridge.Page("setup", "").SetVisible(false); delete(ui.webSetupWindows, bridge) })
		window.Show()

	})
	start := newRow("Start tunnel", iconPlug, styleOutline, func() {
		bridge, err := account()
		if err == nil {
			err = bridge.StartTunnel(*settings)
		}
		if err != nil {
			say(err.Error())
			return
		}
		status.SetText(bridge.TunnelStatus())
		go func() {
			timer := time.NewTicker(time.Second)
			defer timer.Stop()
			for attempts := 0; attempts < 20; attempts++ {
				select {
				case <-ui.ctx.Done():
					return
				case <-timer.C:
					fyne.Do(func() {
						if ui.ctx.Err() == nil {
							status.SetText(bridge.TunnelStatus())
						}
					})
				}
			}
		}()
	})
	prepare := newRow("Prepare 1loop project", iconFolder, styleOutline, func() {
		bridge, err := account()
		if err != nil {
			say(err.Error())
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(ui.ctx, 30*time.Second)
			defer cancel()
			address, err := bridge.PrepareProject(ctx)
			fyne.Do(func() {
				if err != nil {
					say(err.Error())
					return
				}
				settings.ProjectURL = address
				project.SetText(address)
				say("The 1loop project is ready. Load models, then save this connection.")
			})
		}()
	})
	link := func(label, address string) fyne.CanvasObject {
		u, _ := url.Parse(address)
		return widget.NewHyperlink(label, u)
	}
	return card(
		settingStack("ChatGPT (Web) setup", "Desktop only. Uses the ChatGPT website in a separate browser. This is unofficial and can break when ChatGPT changes. ChatGPT's normal usage limits still apply.", container.NewVBox(
			note("1. Sign in to ChatGPT using the dedicated browser. Browser Use and your normal browser keep separate cookies."), open,
			note("2. Create a Secure MCP Tunnel in your OpenAI organization, associated with this ChatGPT workspace."), link("Open tunnel settings", "https://platform.openai.com/settings/organization/tunnels"), tunnel,
			note("3. Create a restricted key with Tunnels Read + Use. Save it in a private file (chmod 600). Enter only its file path here. The key is never saved in chats or sent to the model."), link("Open API key settings", "https://platform.openai.com/settings/organization/api-keys"), keyFile, start, status,
			note("4. In ChatGPT Plugins, add a custom MCP server named 1loop. Choose Tunnel and the ID above, then No authentication. Review the warning and connect. Keep this desktop running during discovery."), link("Open ChatGPT Plugins", "https://chatgpt.com/plugins"),
			note("5. The ChatGPT project named 1loop holds the Web conversations. After sign-in, load this account's actual models below."), project, prepare,
		)),
	)
}

// webBrowser reuses the existing page renderer, without Browser Use's
// address bar, console or tools. Input stays locked until explicitly taken.
func (ui *desktopUI) webBrowser(bridge *agent.ChatGPTWeb, key, address string) fyne.CanvasObject {
	return ui.webBrowserWindow(bridge, key, address, ui.window, false)
}

func (ui *desktopUI) webBrowserWindow(bridge *agent.ChatGPTWeb, key, address string, window fyne.Window, setup bool) fyne.CanvasObject {
	tab := bridge.Page(key, address)
	view := newBrowserView(ui.panel, nil, tab)
	view.window = window
	view.clipboardMenu = true
	unlocked := setup
	view.inputAllowed = func() bool { return unlocked }
	control := newRow("Take control", iconBrowser, styleOutline, nil)
	control.onTap = func() {
		if unlocked {
			unlocked = false
			control.SetText("Take control")
			return
		}
		ui.confirm("Take control of ChatGPT?", "Clicks and typing can interrupt the automated conversation. Complete sign-in or security checks yourself, then lock the browser again.", "Take control", func() { unlocked = true; control.SetText("Lock browser") })
	}
	header := container.NewVBox(note("ChatGPT · separate account browser"), container.NewHBox(control))
	if setup {
		header = container.NewVBox(note("Sign in normally. This separate browser is ready for typing, paste and right-click. Complete any security check yourself, then close this window and return to connection setup."))
	}
	view.content = container.NewBorder(header, nil, nil, nil, view.screen)
	tab.OnFrame = view.frame
	tab.SetVisible(true)
	return view
}

// ensureWebTab shows the ChatGPT page of the chat on screen in the side
// panel: its tab follows the chat (the start screen has the setup page).
func (ui *desktopUI) ensureWebTab() {
	bridge, err := ui.backend.WebAccount(ui.cfg)
	if err != nil {
		return
	}
	key, address := "setup", "https://chatgpt.com/"
	if ui.cfg.WebSettings != nil && ui.cfg.WebSettings.ProjectURL != "" {
		address = ui.cfg.WebSettings.ProjectURL
	}
	if ui.current != nil {
		key = ui.current.ID + ":" + ui.current.Version // the run's page (Config.ContextKey)
		if web := ui.current.Config.WebConversation; web != nil && web.URL != "" {
			address = web.URL
		}
	}
	for i, tab := range ui.panel.tabs {
		if tab.web {
			if tab.webKey != key {
				ui.panel.tabs[i].content, ui.panel.tabs[i].webKey = ui.webBrowser(bridge, key, address), key
				if i == ui.panel.active {
					ui.panel.render()
				}
			}
			return
		}
	}
	ui.panel.tabs = append(ui.panel.tabs, panelTab{kind: "chatgptweb", title: "ChatGPT", icon: iconPlug, web: true, webKey: key, content: ui.webBrowser(bridge, key, address)})
	ui.panel.active = len(ui.panel.tabs) - 1
	ui.panel.render()
}
