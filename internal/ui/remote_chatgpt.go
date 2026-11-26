// SPDX-License-Identifier: MPL-2.0

package ui

// A server's ChatGPT account, signed in with this computer's browser. The
// server makes the link and keeps the PKCE verifier; OpenAI sends the
// browser back to localhost:1455 (the only address its client accepts),
// which this computer answers, and the code goes on to the server, which
// trades it for the tokens. They never come here.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/agent"
)

// errOldServer: the server can't sign a ChatGPT account in for a device.
var errOldServer = errors.New("Update the server to sign in to ChatGPT from here")

// chatGPTCallback is where this computer waits for OpenAI's redirect.
var chatGPTCallback = "127.0.0.1:1455"

// relayChatGPTSignIn signs the server's connection id in: opened gets the
// link once the browser should show it. It returns the account's email.
func (remote *remoteDesktop) relayChatGPTSignIn(ctx context.Context, id string, opened func(link string)) (string, error) {
	path := "/api/connections/" + url.PathEscape(id) + "/login"
	var begun struct{ URL, State string }
	if err := remote.client.json(ctx, "POST", path, nil, &begun); err != nil {
		var refused remoteError
		if errors.As(err, &refused) && refused.status == http.StatusNotFound && strings.HasPrefix(refused.message, "404") {
			return "", errOldServer // no such route: the server's own 404 page
		}
		return "", err
	}
	link, err := url.Parse(begun.URL)
	if err != nil || link.Scheme != "https" || link.Host != "auth.openai.com" || begun.State == "" {
		return "", fmt.Errorf("The server gave an invalid sign-in link")
	}
	listener, err := net.Listen("tcp", chatGPTCallback)
	if err != nil {
		return "", fmt.Errorf("Another ChatGPT sign-in is waiting on this computer (port 1455 is in use). Finish or cancel it, then try again.")
	}

	type outcome struct {
		email string
		err   error
	}
	result := make(chan outcome, 1)
	var once sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if r.Method != "GET" || query.Get("state") != begun.State {
			http.Error(w, "Invalid OAuth state", http.StatusBadRequest)
			return
		}
		once.Do(func() {
			var answer struct{ Email string }
			err := fmt.Errorf("OpenAI sign-in denied")
			if query.Get("error") == "" {
				err = remote.client.json(ctx, "POST", path+"/complete", map[string]string{"state": begun.State, "code": query.Get("code")}, &answer)
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, "Sign-in failed. Check 1loop for details.")
			} else {
				fmt.Fprintf(w, "Sign-in successful: the account is connected on %s. You can return to 1loop.", remote.name)
			}
			result <- outcome{answer.Email, err}
		})
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go server.Serve(listener)
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	opened(link.String())
	_ = openURL(link)
	select {
	case got := <-result:
		return got.email, got.err
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("The sign-in timed out; start it again")
		}
		return "", ctx.Err()
	}
}

// chatGPTSignInFields are a ChatGPT connection's Access card in the
// server's editor: who is signed in, and the sign-in. A new connection is
// saved first: the sign-in belongs to one the server keeps. stop ends a
// sign-in still waiting.
func (remote *remoteDesktop) chatGPTSignInFields(draft *agent.Connection, row serverConnection, name *widget.Entry, say func(string), loadModels func()) (fields fyne.CanvasObject, stop func()) {
	ui := remote.parent
	account := plainFlow("", 12.5, colMuted)
	showAccount := func(signedIn bool, email string) {
		switch {
		case email != "":
			setFlow(account, email)
		case signedIn:
			setFlow(account, "Signed in on the server.")
		default:
			setFlow(account, "Not signed in on the server.")
		}
	}
	showAccount(row.SignedIn, row.Email)
	loginLink := widget.NewHyperlink("Open sign-in in your browser", nil)
	loginLink.Hide()
	login := newRow("Continue with ChatGPT", nil, styleOutline, nil)
	cancel := newRow("Cancel", nil, styleOutline, nil)
	cancel.Hide()
	signOut := newRow("Sign out", nil, styleOutline, nil)
	if !row.SignedIn {
		signOut.Hide()
	}
	var generation int
	var cancelSignIn context.CancelFunc
	stop = func() {
		generation++
		if cancelSignIn != nil {
			cancelSignIn()
			cancelSignIn = nil
		}
		login.Enable()
		cancel.Hide()
		loginLink.Hide()
	}
	cancel.onTap = func() {
		stop()
		say("Sign-in canceled.")
	}
	login.onTap = func() {
		stop()
		snapshot := cloneConnection(*draft)
		snapshot.Name = strings.TrimSpace(name.Text)
		if snapshot.Name == "" {
			say("Enter a connection name.")
			return
		}
		ctx, done := context.WithTimeout(remote.ctx, 5*time.Minute)
		cancelSignIn = done
		current := generation
		login.Disable()
		cancel.Show()
		say("Preparing the sign-in on the server…")
		go func() {
			defer done()
			id := snapshot.ID
			var err error
			if id == "" {
				var saved struct{ Connection agent.Connection }
				err = remote.client.json(ctx, "POST", "/api/connections", map[string]any{"connection": snapshot}, &saved)
				if err == nil {
					id = saved.Connection.ID
					fyne.Do(func() { draft.ID, draft.AuthProfile = saved.Connection.ID, saved.Connection.AuthProfile })
					remote.notify()
				}
			}
			email := ""
			if err == nil {
				email, err = remote.relayChatGPTSignIn(ctx, id, func(link string) {
					fyne.Do(func() {
						if current != generation {
							return
						}
						parsed, _ := url.Parse(link)
						loginLink.SetURL(parsed)
						loginLink.Show()
						say("Authorize 1loop in your browser, then return here.")
					})
				})
			}
			fyne.Do(func() {
				if current != generation {
					return // canceled, or begun again
				}
				cancelSignIn = nil
				stop()
				if err != nil {
					say(err.Error())
					return
				}
				remote.notify() // the picker: the server reads the models again
				showAccount(true, email)
				signOut.Show()
				say("Account connected on the server.")
				ui.chatGPTConnected(email, loadModels)
			})
		}()
	}
	signOut.onTap = func() {
		ui.confirm("Sign out of ChatGPT?", "The server forgets this sign-in. Chats are kept.", "Sign out", func() {
			id := draft.ID
			go func() {
				ctx, done := context.WithTimeout(remote.ctx, 30*time.Second)
				defer done()
				var answer struct{ Warning string }
				err := remote.client.json(ctx, "POST", "/api/connections/"+url.PathEscape(id)+"/disconnect", nil, &answer)
				remote.notify()
				fyne.Do(func() {
					if err != nil {
						say(err.Error())
						return
					}
					showAccount(false, "")
					signOut.Hide()
					say("Signed out.")
					if answer.Warning != "" {
						ui.notice("Account disconnected", answer.Warning)
					}
				})
			}()
		})
	}
	below := container.NewVBox(account, container.NewHBox(login, cancel, signOut), loginLink)
	if !remote.chatGPTSignIn {
		login.Hide()
		below.Add(note(errOldServer.Error() + "."))
	}
	return card(settingStack("ChatGPT account", "Uses a ChatGPT subscription on the server. Sign in here.", below)), stop
}
