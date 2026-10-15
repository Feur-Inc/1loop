//go:build !server

package main

import "net/http"

func (server *remoteServer) panelStream(writer http.ResponseWriter, req *http.Request) {
	_, tab, ok := server.findTab(req)
	if !ok || tab.browser == nil {
		fail(writer, 404, "This browser tab is closed")
		return
	}
	serveBrowserStream(writer, req, tab.browser, nil)
}
