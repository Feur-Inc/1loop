// SPDX-License-Identifier: MPL-2.0

package loopy

import (
	"context"
	"net/url"
	"strconv"
)

// Client is Loopy as a view reaches it: the engine itself in the app it
// lives in, or Remote over a server's API.
type Client interface {
	View(ctx context.Context) (View, error)
	Before(ctx context.Context, id string) ([]Message, bool, error)
	Send(ctx context.Context, text string, files []string) error
	Stop(ctx context.Context) error
	Answer(ctx context.Context, answer Answer) error
	React(ctx context.Context, message, reaction string) error
	Set(ctx context.Context, settings Settings) error
	Clear(ctx context.Context) error
	Do(ctx context.Context, action Action) error
	Memory(ctx context.Context) (MemoryView, error)
	Files(ctx context.Context, path string) ([]File, error)
	Read(ctx context.Context, path string) ([]byte, error)
	Write(ctx context.Context, dir, name string, data []byte) (string, error)
}

// Action is a change asked for from a page.
type Action struct {
	// Do is "routine.on", "routine.off", "routine.run", "routine.remove",
	// "routine.edit", "task.stop", "fact.change", "fact.add", "fact.forget",
	// "rule.remove", "vault.save", "vault.remove", "account.save",
	// "account.remove", "file.remove", "summary", "learn".
	Do      string            `json:"do"`
	ID      string            `json:"id,omitempty"`
	Text    string            `json:"text,omitempty"`
	Kind    string            `json:"kind,omitempty"`
	On      bool              `json:"on,omitempty"`
	Routine *Routine          `json:"routine,omitempty"`
	Entry   *VaultEntry       `json:"entry,omitempty"`
	Values  map[string]string `json:"values,omitempty"`
	Account *Account          `json:"account,omitempty"`
}

// Caller makes a request of a 1loop's API: out is read from the answer.
type Caller interface {
	JSON(ctx context.Context, method, path string, body, out any) error
	Bytes(ctx context.Context, path string) ([]byte, error)
	Upload(ctx context.Context, path string, data []byte, out any) error
}

// Remote is a Loopy that lives elsewhere.
type Remote struct{ Caller Caller }

type ok struct{}

func (remote Remote) post(ctx context.Context, path string, body any) error {
	return remote.Caller.JSON(ctx, "POST", "/api/loopy/"+path, body, &ok{})
}

func (remote Remote) View(ctx context.Context) (view View, err error) {
	err = remote.Caller.JSON(ctx, "GET", "/api/loopy", nil, &view)
	return
}

func (remote Remote) Before(ctx context.Context, id string) ([]Message, bool, error) {
	var out struct {
		Messages []Message
		More     bool
	}
	err := remote.Caller.JSON(ctx, "GET", "/api/loopy/messages?before="+url.QueryEscape(id), nil, &out)
	return out.Messages, out.More, err
}

func (remote Remote) Send(ctx context.Context, text string, files []string) error {
	return remote.post(ctx, "send", map[string]any{"text": text, "files": files})
}

func (remote Remote) Stop(ctx context.Context) error {
	return remote.post(ctx, "stop", map[string]any{})
}

func (remote Remote) Answer(ctx context.Context, answer Answer) error {
	return remote.post(ctx, "answer", answer)
}

func (remote Remote) React(ctx context.Context, message, reaction string) error {
	return remote.post(ctx, "react", map[string]string{"message": message, "reaction": reaction})
}

func (remote Remote) Set(ctx context.Context, settings Settings) error {
	return remote.post(ctx, "settings", settings)
}

func (remote Remote) Clear(ctx context.Context) error {
	return remote.post(ctx, "clear", map[string]any{})
}

func (remote Remote) Do(ctx context.Context, action Action) error {
	return remote.post(ctx, "do", action)
}

func (remote Remote) Memory(ctx context.Context) (view MemoryView, err error) {
	err = remote.Caller.JSON(ctx, "GET", "/api/loopy/memory", nil, &view)
	return
}

func (remote Remote) Files(ctx context.Context, path string) (files []File, err error) {
	err = remote.Caller.JSON(ctx, "GET", "/api/loopy/files?path="+url.QueryEscape(path), nil, &files)
	return
}

func (remote Remote) Read(ctx context.Context, path string) ([]byte, error) {
	return remote.Caller.Bytes(ctx, "/api/loopy/file?path="+url.QueryEscape(path))
}

func (remote Remote) Write(ctx context.Context, dir, name string, data []byte) (string, error) {
	var out struct{ Path string }
	err := remote.Caller.Upload(ctx, "/api/loopy/file?dir="+url.QueryEscape(dir)+"&name="+url.QueryEscape(name)+"&size="+strconv.Itoa(len(data)), data, &out)
	return out.Path, err
}
