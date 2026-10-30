package ui

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/agent"
)

func (ui *desktopUI) signInClaude() {
	ctx, cancel := context.WithTimeout(ui.ctx, 5*time.Minute)
	status := plainFlow("Starting Claude Code sign-in…", 12.5, colMuted)
	link := widget.NewHyperlink("Open Claude sign-in in your browser", nil)
	link.Hide()
	code := widget.NewEntry()
	code.SetPlaceHolder("Authorization code, if Claude asks for one")
	submit := newRow("Submit code", nil, stylePrimary, nil)
	submit.Disable()
	var dialog *sheet
	close := newRow("Close", nil, styleOutline, func() { dialog.dismiss() })
	dialog = ui.modal("Sign in to Claude Code", container.NewVBox(
		note("Use your Claude subscription in Claude Code's own sign-in page. If the browser gives you a code, paste it below. Credentials are saved by Claude Code, not by 1loop."),
		link, code, status), 620, cancel, close, submit)
	go func() {
		process, err := agent.StartClaudeSignIn(ctx, os.TempDir(), "", func(raw string) {
			parsed, _ := url.Parse(raw)
			fyne.Do(func() {
				if ctx.Err() == nil {
					link.SetURL(parsed)
					link.Show()
					setFlow(status, "Complete sign-in in your browser.")
				}
			})
		})
		if err != nil {
			fyne.Do(func() {
				if ctx.Err() != context.Canceled {
					setFlow(status, err.Error())
				}
				cancel()
			})
			return
		}
		fyne.Do(func() {
			if ctx.Err() != nil {
				return
			}
			submit.onTap = func() {
				value := strings.TrimSpace(code.Text)
				if value == "" || len(value) > 4096 || strings.ContainsAny(value, "\r\n") {
					setFlow(status, "Enter the authorization code from Claude.")
					return
				}
				submit.Disable()
				go func() {
					_, err := io.WriteString(process.Input, value+"\n")
					fyne.Do(func() {
						if ctx.Err() != nil {
							return
						}
						submit.Enable()
						if err != nil {
							setFlow(status, "Claude Code is no longer waiting for a code.")
						} else {
							setFlow(status, "Waiting for Claude Code to finish sign-in…")
						}
					})
				}()
			}
			submit.Enable()
		})
		err = <-process.Done
		fyne.Do(func() {
			if ctx.Err() == context.Canceled {
				return
			}
			submit.Disable()
			link.Hide()
			if ctx.Err() == context.DeadlineExceeded {
				setFlow(status, "Claude Code sign-in timed out. Close this dialog and try again.")
			} else if err != nil {
				setFlow(status, fmt.Sprintf("Claude Code sign-in did not complete: %s", agent.FirstLine(err.Error())))
			} else {
				setFlow(status, "Sign-in completed. Close this dialog and load the connection's models.")
			}
			cancel()
		})
	}()
}
