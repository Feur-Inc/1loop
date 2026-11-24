// SPDX-License-Identifier: MPL-2.0

//go:build !js

package engine

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"1loop/internal/core"
	"1loop/internal/loopy"
	"1loop/internal/loopy/memory"
)

// Do is a change asked for from a page.
func (e *Engine) Do(ctx context.Context, a loopy.Action) error {
	switch a.Do {
	case "routine.on", "routine.off":
		return e.SetRoutine(a.ID, a.Do == "routine.on")
	case "routine.run":
		return e.RunRoutine(a.ID)
	case "routine.remove":
		e.RemoveRoutine(a.ID)
		return nil
	case "routine.edit":
		if a.Routine == nil {
			return errors.New("no routine given")
		}
		return e.EditRoutine(*a.Routine)
	case "task.stop":
		return e.StopTask(a.ID)
	case "rule.remove":
		e.RemoveRule(a.ID)
		return nil
	case "fact.add":
		_, err := e.memory.Remember(memory.Fact{Text: a.Text, Kind: a.Kind, Said: true, Pinned: a.On}, "")
		e.tell(nil, nil, true)
		return err
	case "fact.change":
		_, err := e.memory.Change(a.ID, func(f *memory.Fact) {
			if strings.TrimSpace(a.Text) != "" {
				f.Text = strings.TrimSpace(a.Text)
			}
			if a.Kind != "" {
				f.Kind = a.Kind
			}
			f.Pinned, f.Said = a.On, true // the user looked at it and kept it
		})
		e.tell(nil, nil, true)
		return err
	case "fact.forget":
		_, err := e.memory.Forget(a.ID, "", a.On)
		e.store.Lock()
		var notes []string
		for _, m := range e.store.Messages {
			if m.MemoryID == a.ID {
				notes = append(notes, m.ID)
			}
		}
		e.store.Unlock()
		for _, id := range notes {
			e.update(id, func(m *loopy.Message) { m.Memory, m.MemoryID = "Forgotten", "" })
		}
		e.tell(nil, nil, true)
		return err
	case "vault.save":
		if a.Entry == nil {
			return errors.New("no entry given")
		}
		return e.SaveSecret(*a.Entry, a.Values)
	case "vault.remove":
		return e.RemoveSecret(a.ID)
	case "account.save":
		if a.Account == nil {
			return errors.New("no account given")
		}
		return e.SaveAccount(ctx, *a.Account)
	case "account.remove":
		return e.RemoveAccount(a.ID)
	case "file.remove":
		return e.folder.remove(a.Text)
	case "summary":
		e.Summary("a summary the user asked for")
		return nil
	case "learn":
		e.store.Lock()
		settings := e.store.Settings
		e.store.Unlock()
		return e.learn(ctx, settings)
	}
	return errors.New("unknown action")
}

// Memory is the memory page.
func (e *Engine) Memory() loopy.MemoryView {
	view := loopy.MemoryView{Records: len(e.memory.Records(1 << 30)), Waiting: e.memory.Due(), Habits: e.memory.Habits().Describe()}
	for _, fact := range e.memory.Facts(true) {
		view.Facts = append(view.Facts, loopy.Fact{ID: fact.ID, Text: fact.Text, Kind: fact.Kind, Since: fact.Since, Until: fact.Until, Said: fact.Said, Sure: fact.Sure(), Times: fact.Times, Pinned: fact.Pinned, Quote: fact.Quote})
	}
	e.store.Lock()
	journal := e.store.Journal
	view.Journal = append(view.Journal, journal[max(0, len(journal)-100):]...)
	e.store.Unlock()
	return view
}

// maxUpload is the largest file a view may hand over.
const maxUpload = 32 << 20

// Local is the engine as a view in the same app reaches it.
func (e *Engine) Local() loopy.Client { return localClient{e} }

type localClient struct{ e *Engine }

func (c localClient) View(context.Context) (loopy.View, error) { return c.e.View(), nil }
func (c localClient) Before(_ context.Context, id string) ([]loopy.Message, bool, error) {
	messages, more := c.e.Before(id)
	return messages, more, nil
}
func (c localClient) Send(_ context.Context, text string, files []string) error {
	_, err := c.e.Send(text, files)
	return err
}
func (c localClient) Stop(context.Context) error { c.e.Stop(); return nil }
func (c localClient) Answer(_ context.Context, answer loopy.Answer) error {
	return c.e.Answer(answer)
}
func (c localClient) React(_ context.Context, message, reaction string) error {
	return c.e.React(message, reaction)
}
func (c localClient) Set(_ context.Context, settings loopy.Settings) error {
	c.e.Set(settings)
	return nil
}
func (c localClient) Clear(context.Context) error { c.e.Clear(); return nil }
func (c localClient) Do(ctx context.Context, action loopy.Action) error {
	return c.e.Do(ctx, action)
}
func (c localClient) Memory(context.Context) (loopy.MemoryView, error) { return c.e.Memory(), nil }
func (c localClient) Files(_ context.Context, path string) ([]loopy.File, error) {
	return c.e.folder.list(path, false)
}
func (c localClient) Read(_ context.Context, path string) ([]byte, error) {
	return c.e.folder.bytes(path, maxFile)
}
func (c localClient) Write(_ context.Context, dir, name string, data []byte) (string, error) {
	at := c.e.folder.fresh(dir, name)
	return at, c.e.folder.write(at, data, false)
}

// Handlers are the API of the Loopy get gives: what a view elsewhere
// reaches it with. get is asked each time, for a Loopy may be made late.
func Handlers(get func() (*Engine, error)) map[string]http.HandlerFunc {
	with := func(serve func(e *Engine, w http.ResponseWriter, r *http.Request) error) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			e, err := get()
			if err != nil {
				core.Fail(w, 503, err.Error())
				return
			}
			if err := serve(e, w, r); err != nil {
				core.Fail(w, 400, err.Error())
			}
		}
	}
	done := func(w http.ResponseWriter) { core.WriteJSON(w, 200, map[string]bool{"ok": true}) }
	read := func(r *http.Request, out any) error {
		if err := core.ReadJSON(r, out); err != nil {
			return errors.New("the request cannot be read")
		}
		return nil
	}
	return map[string]http.HandlerFunc{
		"GET /api/loopy": with(func(e *Engine, w http.ResponseWriter, r *http.Request) error {
			core.WriteJSON(w, 200, e.View())
			return nil
		}),
		"GET /api/loopy/messages": with(func(e *Engine, w http.ResponseWriter, r *http.Request) error {
			messages, more := e.Before(r.URL.Query().Get("before"))
			core.WriteJSON(w, 200, map[string]any{"messages": messages, "more": more})
			return nil
		}),
		"POST /api/loopy/send": with(func(e *Engine, w http.ResponseWriter, r *http.Request) error {
			var in struct {
				Text  string
				Files []string
			}
			if err := read(r, &in); err != nil {
				return err
			}
			message, err := e.Send(in.Text, in.Files)
			if err == nil {
				core.WriteJSON(w, 200, message)
			}
			return err
		}),
		"POST /api/loopy/stop": with(func(e *Engine, w http.ResponseWriter, r *http.Request) error { e.Stop(); done(w); return nil }),
		"POST /api/loopy/answer": with(func(e *Engine, w http.ResponseWriter, r *http.Request) error {
			var in loopy.Answer
			if err := read(r, &in); err != nil {
				return err
			}
			if err := e.Answer(in); err != nil {
				return err
			}
			done(w)
			return nil
		}),
		"POST /api/loopy/react": with(func(e *Engine, w http.ResponseWriter, r *http.Request) error {
			var in struct{ Message, Reaction string }
			if err := read(r, &in); err != nil {
				return err
			}
			if err := e.React(in.Message, in.Reaction); err != nil {
				return err
			}
			done(w)
			return nil
		}),
		"POST /api/loopy/settings": with(func(e *Engine, w http.ResponseWriter, r *http.Request) error {
			var in loopy.Settings
			if err := read(r, &in); err != nil {
				return err
			}
			e.Set(in)
			done(w)
			return nil
		}),
		"POST /api/loopy/clear": with(func(e *Engine, w http.ResponseWriter, r *http.Request) error { e.Clear(); done(w); return nil }),
		"POST /api/loopy/do": with(func(e *Engine, w http.ResponseWriter, r *http.Request) error {
			var in loopy.Action
			if err := read(r, &in); err != nil {
				return err
			}
			if err := e.Do(r.Context(), in); err != nil {
				return err
			}
			done(w)
			return nil
		}),
		"GET /api/loopy/memory": with(func(e *Engine, w http.ResponseWriter, r *http.Request) error {
			core.WriteJSON(w, 200, e.Memory())
			return nil
		}),
		"GET /api/loopy/files": with(func(e *Engine, w http.ResponseWriter, r *http.Request) error {
			files, err := e.folder.list(r.URL.Query().Get("path"), false)
			if err != nil {
				return err
			}
			if files == nil {
				files = []loopy.File{}
			}
			core.WriteJSON(w, 200, files)
			return nil
		}),
		"GET /api/loopy/file": with(func(e *Engine, w http.ResponseWriter, r *http.Request) error {
			name := r.URL.Query().Get("path")
			file, err := e.folder.open(name)
			if err != nil {
				return err
			}
			defer file.Close()
			info, err := file.Stat()
			if err != nil || !info.Mode().IsRegular() {
				return errors.New("not a file")
			}
			// Never opened as a page of the server's: a script or a page the
			// folder holds is only ever downloaded.
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(name)}))
			http.ServeContent(w, r, "", time.Time{}, file)
			return nil
		}),
		"POST /api/loopy/file": with(func(e *Engine, w http.ResponseWriter, r *http.Request) error {
			dir := r.URL.Query().Get("dir")
			if dir == "" {
				dir = "/inbox"
			}
			data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxUpload))
			if err != nil {
				return errors.New("that file is too large (32 MB at most)")
			}
			at := e.folder.fresh(dir, r.URL.Query().Get("name"))
			if err := e.folder.write(at, data, false); err != nil {
				return err
			}
			core.WriteJSON(w, 200, map[string]string{"path": at})
			return nil
		}),
	}
}

// HookHandler answers the webhooks: no pairing, the token is the key.
func HookHandler(get func() (*Engine, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		e, err := get()
		if err != nil {
			core.Fail(w, 503, err.Error())
			return
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		if err := e.Hook(r.PathValue("token"), body); err != nil {
			core.Fail(w, 404, "No such webhook")
			return
		}
		core.WriteJSON(w, 200, map[string]bool{"ok": true})
	}
}
