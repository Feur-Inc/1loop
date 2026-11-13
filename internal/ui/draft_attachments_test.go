// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"image"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
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
