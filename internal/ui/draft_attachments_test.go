// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"image"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"

	"1loop/internal/agent"
)

// A picture or file staged in one chat stays with that chat's draft: the
// other chats never show or send it, and it is there again on coming back
// (#409).
func TestAttachmentsBelongToTheirDraft(t *testing.T) {
	test.NewTempApp(t)
	ui := &desktopUI{prompt: newChatEntry(), attachRow: container.NewHBox()}
	ui.attachTray = ui.attachRow
	ui.prompt.switched = ui.switchAttachments
	ui.prompt.bindDrafts(loadPromptDrafts(filepath.Join(t.TempDir(), "drafts.json")))

	ui.prompt.switchDraft("chat-a")
	ui.attachImage(image.NewNRGBA(image.Rect(0, 0, 4, 4)))
	ui.fileAttachments = append(ui.fileAttachments, fileAttachment{Name: "notes.txt", Path: "/tmp/notes.txt"})
	ui.renderAttachments()

	ui.prompt.switchDraft("chat-b")
	if len(ui.attachments) != 0 || len(ui.fileAttachments) != 0 || len(ui.attachRow.Objects) != 0 {
		t.Fatalf("chat B shows chat A's attachments: %d images, %d files, %d thumbnails", len(ui.attachments), len(ui.fileAttachments), len(ui.attachRow.Objects))
	}
	ui.attachImage(image.NewNRGBA(image.Rect(0, 0, 2, 2)))

	ui.prompt.switchDraft("new:/project")
	if len(ui.attachments) != 0 || len(ui.fileAttachments) != 0 {
		t.Fatalf("the start screen shows another chat's attachments: %d images, %d files", len(ui.attachments), len(ui.fileAttachments))
	}

	ui.prompt.switchDraft("chat-a")
	if len(ui.attachments) != 1 || len(ui.fileAttachments) != 1 || ui.fileAttachments[0].Name != "notes.txt" || len(ui.attachRow.Objects) != 2 {
		t.Fatalf("chat A's attachments not back: %d images, %d files, %d thumbnails", len(ui.attachments), len(ui.fileAttachments), len(ui.attachRow.Objects))
	}
	ui.prompt.switchDraft("chat-b")
	if len(ui.attachments) != 1 || len(ui.fileAttachments) != 0 {
		t.Fatalf("chat B's picture not back: %d images, %d files", len(ui.attachments), len(ui.fileAttachments))
	}
}

// A server chat's uploaded files stay with its draft too (#544), files
// uploaded for a chat left meanwhile wait in it, and a picture uploaded from
// here shows as a thumbnail, not its name (#543).
func TestServerAttachmentsBelongToTheirDraft(t *testing.T) {
	test.NewTempApp(t)
	ui := &desktopUI{prompt: newChatEntry(), attachRow: container.NewHBox()}
	ui.attachTray = ui.attachRow
	ui.server = &remoteDesktop{parent: ui}
	ui.prompt.switched = ui.switchAttachments
	ui.prompt.bindDrafts(loadPromptDrafts(filepath.Join(t.TempDir(), "drafts.json")))

	ui.prompt.switchDraft("server|chat-a")
	ui.server.fileAttachments = []agent.FileAttachment{{Name: "pasted-image.png", Path: "/w/.1loop/pasted-image.png"}}
	ui.server.thumbnails = map[string]image.Image{"/w/.1loop/pasted-image.png": image.NewNRGBA(image.Rect(0, 0, 8, 4))}
	ui.renderAttachments()
	if len(ui.attachRow.Objects) != 1 {
		t.Fatalf("%d attachments shown", len(ui.attachRow.Objects))
	}
	if _, ok := ui.attachRow.Objects[0].(*pictureTile); !ok {
		t.Fatalf("the picture shows as %T, not a thumbnail", ui.attachRow.Objects[0])
	}

	ui.prompt.switchDraft("server|chat-b")
	if len(ui.server.fileAttachments) != 0 || len(ui.attachRow.Objects) != 0 {
		t.Fatalf("chat B shows chat A's files: %v", ui.server.fileAttachments)
	}
	ui.parkServerFiles("server|chat-a", []agent.FileAttachment{{Name: "notes.txt", Path: "/w/.1loop/notes.txt"}})

	ui.prompt.switchDraft("server|chat-a")
	if len(ui.server.fileAttachments) != 2 || ui.server.fileAttachments[1].Name != "notes.txt" || len(ui.attachRow.Objects) != 2 {
		t.Fatalf("chat A's files not back: %v, %d shown", ui.server.fileAttachments, len(ui.attachRow.Objects))
	}
}
