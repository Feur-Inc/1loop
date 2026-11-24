// SPDX-License-Identifier: MPL-2.0

package ui

// In the browser, Loopy lives on the server.

import (
	"errors"
	"net/http"

	"1loop/internal/browser"
	"1loop/internal/loopy"
)

type loopyHome struct{ chat *conversation }

const loopyChatID = "loopy"

func (ui *desktopUI) localLoopy() (loopy.Client, error) {
	return nil, errors.New("Loopy lives on the server")
}

func (ui *desktopUI) wakeLoopy()                                            {}
func (ui *desktopUI) closeLoopy()                                           {}
func (ui *desktopUI) loopyStep(chat *conversation, step string, waits bool) {}
func (ui *desktopUI) loopyEnded(chat *conversation, ended string)           {}
func (ui *desktopUI) loopyPage() *browser.Page                              { return nil }
func (server *remoteServer) loopyHandlers() map[string]http.HandlerFunc     { return nil }
func (server *remoteServer) loopyHook() http.HandlerFunc                    { return nil }
