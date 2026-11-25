// SPDX-License-Identifier: MPL-2.0

package browser

import (
	"encoding/json"
	"testing"
)

// A lent browser browses the web for the server: what would reach this
// computer's files never gets to it.
func TestLendRefusal(t *testing.T) {
	for command, refused := range map[string]bool{
		`{"id":1,"method":"Page.navigate","params":{"url":"https://example.com"}}`:                      false,
		`{"id":1,"method":"Target.createTarget","params":{"url":"about:blank"}}`:                        false,
		`{"id":1,"method":"Runtime.evaluate","params":{"expression":"location='file:///etc/passwd'"}}`:  false,
		`{"id":1,"method":"Browser.setDownloadBehavior","params":{"behavior":"deny"}}`:                  false,
		`{"id":1,"method":"Page.navigate","params":{"url":"file:///etc/passwd"}}`:                       true,
		`{"id":1,"method":"Target.createTarget","params":{"url":"chrome://settings"}}`:                  true,
		`{"id":1,"method":"Network.loadNetworkResource","params":{"url":"file:///home"}}`:               true,
		`{"id":1,"method":"Browser.setDownloadBehavior","params":{"behavior":"allow"}}`:                 true,
		`{"id":1,"method":"Page.setDownloadBehavior","params":{"behavior":"allow","downloadPath":"/"}}`: true,
		`{"id":1,"method":"DOM.setFileInputFiles","params":{"files":["/etc/passwd"]}}`:                  true,
		`{"id":1,"method":"Input.dispatchDragEvent","params":{"type":"drop","data":{"files":["/x"]}}}`:  true,
		`{"id":1,"method":"Input.dispatchDragEvent","params":{"type":"drop","data":{"items":[]}}}`:      false,
		`{"id":1,"method":"Browser.grantPermissions","params":{"permissions":["clipboardReadWrite"]}}`:  true,
		`not json`: true,
	} {
		if got := lendRefusal(json.RawMessage(command)) != ""; got != refused {
			t.Errorf("%s: refused %v, want %v", command, got, refused)
		}
	}
}
