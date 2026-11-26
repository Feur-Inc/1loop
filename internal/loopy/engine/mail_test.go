// SPDX-License-Identifier: MPL-2.0

//go:build !js

package engine

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-imap/backend/memory"
	imapserver "github.com/emersion/go-imap/server"
	"github.com/emersion/go-webdav/caldav"

	"net/http"

	"1loop/internal/loopy"
)

// mailServers are an IMAP and an SMTP server on this computer.
func mailServers(t *testing.T) (imap, smtp string, sent func() string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := imapserver.New(memory.New()) // user "username", password "password"
	server.AllowInsecureAuth = true
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	out, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { out.Close() })
	var mu sync.Mutex
	var got strings.Builder
	go func() {
		for {
			conn, err := out.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				lines := bufio.NewScanner(conn)
				fmt.Fprint(conn, "220 test\r\n")
				data := false
				for lines.Scan() {
					line := lines.Text()
					if data {
						if line == "." {
							data = false
							fmt.Fprint(conn, "250 ok\r\n")
							continue
						}
						mu.Lock()
						got.WriteString(line + "\n")
						mu.Unlock()
						continue
					}
					switch {
					case strings.HasPrefix(line, "RCPT"):
						mu.Lock()
						got.WriteString(line + "\n")
						mu.Unlock()
						fmt.Fprint(conn, "250 ok\r\n")
					case line == "DATA":
						data = true
						fmt.Fprint(conn, "354 go\r\n")
					case line == "QUIT":
						fmt.Fprint(conn, "221 bye\r\n")
						return
					default:
						fmt.Fprint(conn, "250 ok\r\n")
					}
				}
			}()
		}
	}()
	return listener.Addr().String(), out.Addr().String(), func() string { mu.Lock(); defer mu.Unlock(); return got.String() }
}

func TestMailAccount(t *testing.T) {
	imap, smtp, sent := mailServers(t)
	e, _ := wake(t, t.TempDir(), func([]heard) any { return "ok" })
	ctx := context.Background()
	if err := e.SaveAccount(ctx, loopy.Account{Kind: "mail", Address: "username", IMAP: imap, SMTP: smtp, Password: "password"}); err == nil {
		t.Error("an address without a domain was taken")
	}
	account := loopy.Account{ID: "a", Kind: "mail", Address: "username", IMAP: imap, SMTP: smtp, Password: "password"}
	// The test server's user has no domain: kept as SaveAccount would.
	if _, err := e.vault.put(loopy.VaultEntry{ID: "account-a", Kind: "account"}, map[string]string{"password": "password"}); err != nil {
		t.Fatal(err)
	}
	account.Password = ""
	e.store.Lock()
	e.store.Accounts = []loopy.Account{account}
	e.store.Unlock()
	listed, err := e.mailTool(ctx, toolArgs{Action: "list"})
	if err != nil || !strings.Contains(listed, "A little message, just for you") || !strings.HasPrefix(listed, "⟦mail") {
		t.Fatalf("%q %v", listed, err)
	}
	id := strings.Fields(listed[strings.Index(listed, "id "):])[1]
	body, err := e.mailTool(ctx, toolArgs{Action: "read", ID: id})
	if err != nil || !strings.Contains(body, "Hi there") || !strings.Contains(body, "contact@example.org") {
		t.Errorf("%q %v", body, err)
	}
	if none, err := e.mailTool(ctx, toolArgs{Action: "list", Words: "zzzzzz"}); err != nil || none != "No mail." {
		t.Errorf("%q %v", none, err)
	}
	// A draft the user sends goes, as it is.
	message := e.add(loopy.Message{From: "loopy", Card: &loopy.Card{Kind: "mail", Title: "Réponse", Editable: true, Actions: []string{"Send", "Discard"},
		Fields: []loopy.Field{{Label: "To", Value: "Paul <paul@example.com>"}, {Label: "Subject", Value: "Jeudi à l'été\r\nBcc: evil@example.com"}}, Body: "Salut"}})
	if err := e.Answer(loopy.Answer{Message: message.ID, Answer: "Send", Body: "Salut Paul,\n\nÀ jeudi !"}); err != nil {
		t.Fatal(err)
	}
	text := sent()
	if !strings.Contains(text, "RCPT TO:<paul@example.com>") || strings.Contains(text, "RCPT TO:<evil") || strings.Contains(text, "\nBcc:") || !strings.Contains(text, "jeudi !") {
		t.Errorf("%s", text)
	}
	shown, _ := e.message(message.ID)
	if shown.Card.Result == "" || shown.Card.Answer != "Sent" {
		t.Errorf("%+v", shown.Card)
	}
	// A routine on mail: the one there is old, nothing runs.
	e.store.Lock()
	e.store.Routines = []loopy.Routine{{ID: "m", Title: "Mails", Task: "Lis", Trigger: "mail", On: true}}
	e.store.Unlock()
	until(t, "idle", e.idle)
	before := len(e.View().Messages)
	e.poll(ctx)
	e.poll(ctx)
	if len(e.View().Messages) != before {
		t.Error("an old mail ran the routine")
	}
	if r, _ := e.routine("m"); r.Seen == "" {
		t.Error("where it stands is not kept")
	}
}

func TestMailBodies(t *testing.T) {
	if text := htmlText(`<html><head><style>a{}</style></head><body><p>Bonjour&nbsp;Paul</p><script>evil()</script><div>À jeudi</div></body></html>`); text != "Bonjour Paul\nÀ jeudi" {
		t.Errorf("%q", text)
	}
	if err := checkMail([]loopy.Field{{Label: "To", Value: "not an address"}}); err == nil {
		t.Error("?")
	}
}

func TestEvents(t *testing.T) {
	for when, want := range map[string]string{
		"2026-12-24 18:00 - 19:30":            "18:00 19:30 false",
		"2026-12-24 18:00":                    "18:00 19:00 false",
		"2026-12-24":                          "00:00 00:00 true",
		"2026-12-24 22:00 → 2026-12-25 01:00": "22:00 01:00 false",
	} {
		ev, err := parseEvent([]loopy.Field{{Label: "What", Value: "Dîner"}, {Label: "When", Value: when}}, "")
		if err != nil || fmt.Sprint(ev.from.Format("15:04"), " ", ev.to.Format("15:04"), " ", ev.day) != want {
			t.Errorf("%s: %+v %v", when, ev, err)
		}
	}
	if _, err := parseEvent([]loopy.Field{{Label: "What", Value: "x"}, {Label: "When", Value: "bientôt"}}, ""); err == nil {
		t.Error("?")
	}
	ev, _ := parseEvent([]loopy.Field{{Label: "What", Value: "Dîner; chez Paul"}, {Label: "When", Value: "2026-12-24 18:00"}, {Label: "Where", Value: "Lyon"}}, "apporter le vin")
	if ics := ev.ics(); !strings.Contains(ics, "BEGIN:VEVENT") || !strings.Contains(ics, "LOCATION:Lyon") {
		t.Error(ics)
	}
}

// calendarBackend is a CalDAV server of one calendar.
type calendarBackend struct {
	mu      sync.Mutex
	objects map[string]*ical.Calendar
}

func (b *calendarBackend) CalendarHomeSetPath(context.Context) (string, error) {
	return "/me/cal/", nil
}
func (b *calendarBackend) CurrentUserPrincipal(context.Context) (string, error) {
	return "/me/", nil
}
func (b *calendarBackend) CreateCalendar(context.Context, *caldav.Calendar) error { return nil }
func (b *calendarBackend) ListCalendars(context.Context) ([]caldav.Calendar, error) {
	return []caldav.Calendar{{Path: "/me/cal/home/", Name: "Home", SupportedComponentSet: []string{"VEVENT"}}}, nil
}
func (b *calendarBackend) GetCalendar(ctx context.Context, path string) (*caldav.Calendar, error) {
	list, _ := b.ListCalendars(ctx)
	return &list[0], nil
}
func (b *calendarBackend) GetCalendarObject(_ context.Context, path string, _ *caldav.CalendarCompRequest) (*caldav.CalendarObject, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return &caldav.CalendarObject{Path: path, Data: b.objects[path]}, nil
}
func (b *calendarBackend) ListCalendarObjects(context.Context, string, *caldav.CalendarCompRequest) ([]caldav.CalendarObject, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []caldav.CalendarObject
	for path, data := range b.objects {
		out = append(out, caldav.CalendarObject{Path: path, Data: data, ETag: "e"})
	}
	return out, nil
}
func (b *calendarBackend) QueryCalendarObjects(ctx context.Context, path string, query *caldav.CalendarQuery) ([]caldav.CalendarObject, error) {
	all, _ := b.ListCalendarObjects(ctx, path, nil)
	return caldav.Filter(query, all)
}
func (b *calendarBackend) PutCalendarObject(_ context.Context, path string, data *ical.Calendar, _ *caldav.PutCalendarObjectOptions) (*caldav.CalendarObject, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.objects[path] = data
	return &caldav.CalendarObject{Path: path, ETag: "e"}, nil
}
func (b *calendarBackend) DeleteCalendarObject(context.Context, string) error { return nil }

func TestCalendarAccount(t *testing.T) {
	handler := &caldav.Handler{Backend: &calendarBackend{objects: map[string]*ical.Calendar{}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, password, _ := r.BasicAuth(); user != "me@example.com" || password != "app password" {
			http.Error(w, "no", 401)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	e, _ := wake(t, t.TempDir(), func([]heard) any { return "ok" })
	ctx := context.Background()
	if err := e.SaveAccount(ctx, loopy.Account{Kind: "calendar", Address: "me@example.com", CalDAV: server.URL, Password: "wrong"}); err == nil {
		t.Error("a wrong password was taken")
	}
	if err := e.SaveAccount(ctx, loopy.Account{Kind: "calendar", Address: "me@example.com", CalDAV: server.URL, Password: "app password"}); err != nil {
		t.Fatal(err)
	}
	if view := e.View(); len(view.Accounts) != 1 || view.Accounts[0].Password != "" || len(view.Vault) != 0 {
		t.Errorf("%+v %+v", view.Accounts, view.Vault)
	}
	day := time.Now().AddDate(0, 0, 2).Format("2006-01-02")
	message := e.add(loopy.Message{From: "loopy", Card: &loopy.Card{Kind: "event", Title: "Dentiste", Editable: true, Actions: []string{"Add", "Discard"},
		Fields: []loopy.Field{{Label: "What", Value: "Dentiste"}, {Label: "When", Value: day + " 14:00 - 14:30"}, {Label: "Where", Value: "Lyon"}}}})
	if err := e.Answer(loopy.Answer{Message: message.ID, Answer: "Add"}); err != nil {
		t.Fatal(err)
	}
	out, err := e.calendarTool(ctx, toolArgs{From: day, To: day})
	if err != nil || !strings.Contains(out, "14:00 Dentiste (Lyon)") {
		t.Errorf("%q %v", out, err)
	}
	if out, _ := e.calendarTool(ctx, toolArgs{From: "2020-01-01", To: "2020-01-02"}); out != "Nothing." {
		t.Errorf("%q", out)
	}
	if err := e.Do(ctx, loopy.Action{Do: "account.remove", ID: e.View().Accounts[0].ID}); err != nil || len(e.View().Accounts) != 0 || len(e.vault.entries) != 0 {
		t.Error("not removed", err)
	}
}
