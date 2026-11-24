// SPDX-License-Identifier: MPL-2.0

//go:build !js

package engine

// Mail (IMAP, SMTP) and calendar (CalDAV) accounts. Reading is Loopy's;
// sending and adding are only done by a card the user pressed.

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	_ "github.com/emersion/go-message/charset"
	gomail "github.com/emersion/go-message/mail"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"

	"1loop/internal/loopy"
)

// servers are those of the common providers, so that an address and an
// app password are enough.
var servers = map[string][3]string{
	"gmail.com": {"imap.gmail.com:993", "smtp.gmail.com:465", "https://apidata.googleusercontent.com/caldav/v2/"}, "googlemail.com": {"imap.gmail.com:993", "smtp.gmail.com:465"},
	"outlook.com": {"outlook.office365.com:993", "smtp-mail.outlook.com:587"}, "hotmail.com": {"outlook.office365.com:993", "smtp-mail.outlook.com:587"},
	"yahoo.com": {"imap.mail.yahoo.com:993", "smtp.mail.yahoo.com:465", "https://caldav.calendar.yahoo.com"}, "yahoo.fr": {"imap.mail.yahoo.com:993", "smtp.mail.yahoo.com:465", "https://caldav.calendar.yahoo.com"},
	"icloud.com": {"imap.mail.me.com:993", "smtp.mail.me.com:587", "https://caldav.icloud.com"}, "me.com": {"imap.mail.me.com:993", "smtp.mail.me.com:587", "https://caldav.icloud.com"},
	"orange.fr": {"imap.orange.fr:993", "smtp.orange.fr:465"}, "wanadoo.fr": {"imap.orange.fr:993", "smtp.orange.fr:465"}, "free.fr": {"imap.free.fr:993", "smtp.free.fr:465"},
	"sfr.fr": {"imap.sfr.fr:993", "smtp.sfr.fr:465"}, "laposte.net": {"imap.laposte.net:993", "smtp.laposte.net:465"}, "gmx.com": {"imap.gmx.com:993", "mail.gmx.com:465"},
	"fastmail.com": {"imap.fastmail.com:993", "smtp.fastmail.com:465", "https://caldav.fastmail.com"}, "zoho.com": {"imap.zoho.com:993", "smtp.zoho.com:465"},
}

func (e *Engine) accounts() (mail, calendar bool) {
	e.store.Lock()
	defer e.store.Unlock()
	for _, account := range e.store.Accounts {
		mail = mail || account.Kind == "mail"
		calendar = calendar || account.Kind == "calendar"
	}
	return
}

// account is the first of a kind, with its password.
func (e *Engine) account(kind string) (loopy.Account, bool) {
	e.store.Lock()
	var found loopy.Account
	for _, account := range e.store.Accounts {
		if account.Kind == kind {
			found = account
			break
		}
	}
	e.store.Unlock()
	if found.ID == "" || e.vault == nil {
		return found, false
	}
	entry, ok := e.vault.get("account-" + found.ID)
	found.Password = entry.Values["password"]
	return found, ok
}

// SaveAccount connects an account, once it answers.
func (e *Engine) SaveAccount(ctx context.Context, account loopy.Account) error {
	if e.vault == nil {
		return errors.New("the vault is not open: the password could not be kept")
	}
	account.Address = strings.TrimSpace(account.Address)
	domain := ""
	if _, after, ok := strings.Cut(account.Address, "@"); ok {
		domain = strings.ToLower(after)
	}
	known := servers[domain]
	fill := func(value *string, fallback string, port string) {
		if *value = strings.TrimSpace(*value); *value == "" {
			*value = fallback
		}
		if *value != "" && port != "" {
			if _, _, err := net.SplitHostPort(*value); err != nil {
				*value = net.JoinHostPort(*value, port)
			}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	switch account.Kind {
	case "mail":
		if domain == "" {
			return errors.New("give a mail address")
		}
		fill(&account.IMAP, known[0], "993")
		fill(&account.SMTP, known[1], "465")
		if account.IMAP == "" || account.SMTP == "" {
			return fmt.Errorf("give the IMAP and SMTP servers of %s", domain)
		}
		box, err := dialMail(ctx, account)
		if err != nil {
			return fmt.Errorf("%s did not let in: %w. Most providers want an app password", account.IMAP, err)
		}
		box.Logout()
	case "calendar":
		fill(&account.CalDAV, known[2], "")
		if !strings.HasPrefix(account.CalDAV, "https://") && !strings.HasPrefix(account.CalDAV, "http://127.0.0.1") && !strings.HasPrefix(account.CalDAV, "http://localhost") {
			return errors.New("give the calendar's CalDAV address (https://…)")
		}
		if _, _, err := calendars(ctx, account); err != nil {
			return fmt.Errorf("the calendar did not let in: %w", err)
		}
	default:
		return errors.New("unknown kind of account")
	}
	if account.ID == "" {
		account.ID = loopy.NewID()
	}
	if _, err := e.vault.put(loopy.VaultEntry{ID: "account-" + account.ID, Kind: "account", Name: account.Address}, map[string]string{"password": account.Password}); err != nil {
		return err
	}
	account.Password = ""
	e.store.Lock()
	e.store.Accounts = append(slices.DeleteFunc(e.store.Accounts, func(a loopy.Account) bool { return a.ID == account.ID }), account)
	e.store.Unlock()
	e.tell(nil, nil, true)
	e.save()
	return nil
}

// RemoveAccount forgets an account and its password.
func (e *Engine) RemoveAccount(id string) error {
	e.store.Lock()
	e.store.Accounts = slices.DeleteFunc(e.store.Accounts, func(a loopy.Account) bool { return a.ID == id })
	e.store.Unlock()
	e.tell(nil, nil, true)
	e.save()
	if e.vault == nil {
		return nil
	}
	return e.vault.remove("account-" + id)
}

func local(address string) bool {
	host, _, err := net.SplitHostPort(address)
	return err == nil && (host == "127.0.0.1" || host == "localhost")
}

func dialMail(ctx context.Context, account loopy.Account) (*client.Client, error) {
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	var box *client.Client
	var err error
	if local(account.IMAP) { // a bridge on this computer, or a test
		box, err = client.DialWithDialer(dialer, account.IMAP)
	} else {
		host, _, _ := net.SplitHostPort(account.IMAP)
		box, err = client.DialWithDialerTLS(dialer, account.IMAP, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	}
	if err != nil {
		return nil, err
	}
	box.Timeout = 25 * time.Second
	if err := box.Login(account.Address, account.Password); err != nil {
		box.Logout()
		return nil, err
	}
	return box, nil
}

type letter struct {
	UID     uint32
	From    string
	Subject string
	Date    time.Time
	Unread  bool
}

func envelope(message *imap.Message) letter {
	l := letter{UID: message.Uid, Unread: !slices.Contains(message.Flags, imap.SeenFlag)}
	if message.Envelope != nil {
		l.Subject, l.Date = message.Envelope.Subject, message.Envelope.Date
		for _, from := range message.Envelope.From {
			l.From = strings.TrimSpace(from.PersonalName + " <" + from.Address() + ">")
		}
	}
	return l
}

// letters are the mails of the inbox that match, the latest first.
func letters(box *client.Client, words string, unread bool, since time.Time, after uint32, n int) ([]letter, error) {
	if _, err := box.Select("INBOX", true); err != nil {
		return nil, err
	}
	criteria := imap.NewSearchCriteria()
	if words = strings.TrimSpace(words); words != "" {
		criteria.Text = []string{words}
	}
	if unread {
		criteria.WithoutFlags = []string{imap.SeenFlag}
	}
	criteria.Since = since
	if after > 0 {
		criteria.Uid = new(imap.SeqSet)
		criteria.Uid.AddRange(after+1, 0)
	}
	ids, err := box.UidSearch(criteria)
	if err != nil {
		return nil, err
	}
	ids = slices.DeleteFunc(ids, func(id uint32) bool { return id <= after }) // "n:*" always gives the last
	slices.Sort(ids)
	ids = ids[max(0, len(ids)-n):]
	if len(ids) == 0 {
		return nil, nil
	}
	set := new(imap.SeqSet)
	set.AddNum(ids...)
	messages := make(chan *imap.Message, 16)
	done := make(chan error, 1)
	go func() {
		done <- box.UidFetch(set, []imap.FetchItem{imap.FetchEnvelope, imap.FetchFlags, imap.FetchUid}, messages)
	}()
	var out []letter
	for message := range messages {
		out = append(out, envelope(message))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UID > out[j].UID })
	return out, <-done
}

func letterText(box *client.Client, uid uint32) (letter, string, error) {
	if _, err := box.Select("INBOX", true); err != nil { // read only: it stays unread
		return letter{}, "", err
	}
	set := new(imap.SeqSet)
	set.AddNum(uid)
	section := &imap.BodySectionName{Peek: true}
	messages := make(chan *imap.Message, 1)
	done := make(chan error, 1)
	go func() {
		done <- box.UidFetch(set, []imap.FetchItem{imap.FetchEnvelope, imap.FetchFlags, imap.FetchUid, section.FetchItem()}, messages)
	}()
	message := <-messages
	for range messages {
	}
	if err := <-done; err != nil {
		return letter{}, "", err
	}
	if message == nil {
		return letter{}, "", errors.New("no such mail")
	}
	body := message.GetBody(section)
	if body == nil {
		return envelope(message), "", nil
	}
	reader, err := gomail.CreateReader(io.LimitReader(body, 12<<20))
	if err != nil && reader == nil {
		return envelope(message), "", err
	}
	var plainText, html string
	var attached []string
	for {
		part, err := reader.NextPart()
		if err != nil {
			break
		}
		switch header := part.Header.(type) {
		case *gomail.InlineHeader:
			kind, _, _ := header.ContentType()
			data, _ := io.ReadAll(io.LimitReader(part.Body, 2<<20))
			switch {
			case kind == "text/plain" && plainText == "":
				plainText = string(data)
			case kind == "text/html" && html == "":
				html = string(data)
			}
		case *gomail.AttachmentHeader:
			name, _ := header.Filename()
			attached = append(attached, name)
		}
	}
	if plainText == "" {
		plainText = htmlText(html)
	}
	if len(attached) > 0 {
		plainText += "\n[attached: " + strings.Join(attached, ", ") + "]"
	}
	return envelope(message), plainText, nil
}

var (
	hiddenHTML = regexp.MustCompile(`(?is)<(script|style|head|title)[^>]*>.*?</\s*(script|style|head|title)\s*>|<!--.*?-->`)
	breakHTML  = regexp.MustCompile(`(?i)<(br|/p|/div|/tr|/li|/h\d)[^>]*>`)
	tagHTML    = regexp.MustCompile(`<[^>]*>`)
	blankLines = regexp.MustCompile(`\n\s*\n\s*\n+`)
)

func htmlText(html string) string {
	html = hiddenHTML.ReplaceAllString(html, "")
	html = breakHTML.ReplaceAllString(html, "\n")
	html = tagHTML.ReplaceAllString(html, "")
	html = strings.NewReplacer("&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'", "&eacute;", "é", "&egrave;", "è", "&agrave;", "à", "&euro;", "€").Replace(html)
	return strings.TrimSpace(blankLines.ReplaceAllString(html, "\n\n"))
}

func (e *Engine) mailTool(ctx context.Context, args toolArgs) (string, error) {
	account, ok := e.account("mail")
	if !ok {
		return "", errors.New("no mail account is connected: the user can add one in Settings → Loopy → Accounts")
	}
	box, err := dialMail(ctx, account)
	if err != nil {
		return "", err
	}
	defer box.Logout()
	switch args.Action {
	case "list":
		since, _ := time.ParseInLocation("2006-01-02", args.Since, time.Local)
		found, err := letters(box, args.Words, args.Unread, since, 0, 25)
		if err != nil {
			return "", err
		}
		if len(found) == 0 {
			return "No mail.", nil
		}
		var b strings.Builder
		for _, l := range found {
			unread := ""
			if l.Unread {
				unread = " (unread)"
			}
			fmt.Fprintf(&b, "id %d · %s · %s · %s%s\n", l.UID, l.Date.Local().Format("Mon 2006-01-02 15:04"), l.From, l.Subject, unread)
		}
		return untrusted("mail", redact(b.String(), e.secrets())), nil
	case "read":
		uid, err := strconv.ParseUint(strings.TrimSpace(args.ID), 10, 32)
		if err != nil {
			return "", errors.New("give a mail's id, from list")
		}
		l, body, err := letterText(box, uint32(uid))
		if err != nil {
			return "", err
		}
		if len(body) > 12000 {
			body = strings.ToValidUTF8(body[:12000], "") + "\n(…)"
		}
		return untrusted("mail", redact(fmt.Sprintf("From: %s\nDate: %s\nSubject: %s\n\n%s", l.From, l.Date.Local().Format("Mon 2006-01-02 15:04"), l.Subject, body), e.secrets())), nil
	}
	return "", errors.New("unknown action")
}

func fieldOf(fields []loopy.Field, names ...string) string {
	for _, field := range fields {
		for _, name := range names {
			if strings.EqualFold(strings.TrimSpace(field.Label), name) {
				return strings.TrimSpace(field.Value)
			}
		}
	}
	return ""
}

func addresses(text string) ([]string, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	list, err := mail.ParseAddressList(text)
	if err != nil {
		return nil, fmt.Errorf("%q is not a mail address", text)
	}
	var out []string
	for _, a := range list {
		out = append(out, a.Address)
	}
	return out, nil
}

func checkMail(fields []loopy.Field) error {
	to, err := addresses(fieldOf(fields, "To", "À", "A"))
	if err != nil {
		return err
	}
	if len(to) == 0 {
		return errors.New("a mail card has a To field, with the address")
	}
	_, err = addresses(fieldOf(fields, "Cc"))
	return err
}

// letterBytes is the mail as it is sent.
func letterBytes(from string, fields []loopy.Field, body string, at time.Time) (data []byte, to []string, err error) {
	if err := checkMail(fields); err != nil {
		return nil, nil, err
	}
	to, _ = addresses(fieldOf(fields, "To", "À", "A"))
	cc, _ := addresses(fieldOf(fields, "Cc"))
	subject := strings.Join(strings.Fields(fieldOf(fields, "Subject", "Objet")), " ")
	var b bytes.Buffer
	var header gomail.Header
	header.SetDate(at)
	header.SetSubject(subject)
	header.SetAddressList("From", []*gomail.Address{{Address: from}})
	group := func(list []string) (out []*gomail.Address) {
		for _, a := range list {
			out = append(out, &gomail.Address{Address: a})
		}
		return
	}
	header.SetAddressList("To", group(to))
	if len(cc) > 0 {
		header.SetAddressList("Cc", group(cc))
	}
	if reply := fieldOf(fields, "In-Reply-To"); reply != "" {
		header.Set("In-Reply-To", strings.Join(strings.Fields(reply), " "))
	}
	_, domain, _ := strings.Cut(from, "@") // never this computer's name
	header.SetMessageID(loopy.NewID() + loopy.NewID() + "@" + domain)
	header.SetContentType("text/plain", map[string]string{"charset": "utf-8"})
	writer, err := gomail.CreateSingleInlineWriter(&b, header)
	if err != nil {
		return nil, nil, err
	}
	io.WriteString(writer, strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n"))
	if err := writer.Close(); err != nil {
		return nil, nil, err
	}
	return b.Bytes(), append(to, cc...), nil
}

func (e *Engine) sendMail(ctx context.Context, account loopy.Account, fields []loopy.Field, body string) error {
	data, to, err := letterBytes(account.Address, fields, body, e.now())
	if err != nil {
		return err
	}
	host, port, err := net.SplitHostPort(account.SMTP)
	if err != nil {
		return err
	}
	dialer := &net.Dialer{Timeout: 20 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", account.SMTP)
	if err != nil {
		return err
	}
	conn.SetDeadline(time.Now().Add(55 * time.Second))
	config := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	if port == "465" {
		conn = tls.Client(conn, config)
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if port != "465" && !local(account.SMTP) {
		if err := c.StartTLS(config); err != nil { // never in clear
			return err
		}
	}
	if ok, _ := c.Extension("AUTH"); ok {
		if err := c.Auth(smtp.PlainAuth("", account.Address, account.Password, host)); err != nil {
			return err
		}
	}
	if err := c.Mail(account.Address); err != nil {
		return err
	}
	for _, a := range to {
		if err := c.Rcpt(a); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	c.Quit()
	return nil
}

// ---------------------------------------------------------------------------
// Calendar.

type event struct {
	what, where, notes string
	from, to           time.Time
	day                bool // the whole day
}

var whenPattern = regexp.MustCompile(`(\d{4}-\d\d-\d\d)(?:[ T](\d{1,2}:\d\d))?(?:\s*(?:-|–|to|à|→)\s*(?:(\d{4}-\d\d-\d\d)[ T]?)?(\d{1,2}:\d\d)?)?`)

// parseEvent reads an event card: When is "2026-12-24 18:00", with an end
// ("… - 19:30") or without (an hour), or a day.
func parseEvent(fields []loopy.Field, body string) (event, error) {
	ev := event{what: fieldOf(fields, "What", "Quoi", "Title"), where: fieldOf(fields, "Where", "Où", "Lieu"), notes: body}
	if ev.what == "" {
		return ev, errors.New("an event card has a What field")
	}
	m := whenPattern.FindStringSubmatch(fieldOf(fields, "When", "Quand"))
	if m == nil {
		return ev, errors.New("an event card has a When field, as \"2026-12-24 18:00 - 19:30\" or \"2026-12-24\"")
	}
	var err error
	if m[2] == "" {
		ev.day = true
		if ev.from, err = time.ParseInLocation("2006-01-02", m[1], time.Local); err != nil {
			return ev, err
		}
		ev.to = ev.from.AddDate(0, 0, 1)
		if m[3] != "" {
			if last, err := time.ParseInLocation("2006-01-02", m[3], time.Local); err == nil && last.After(ev.from) {
				ev.to = last.AddDate(0, 0, 1)
			}
		}
		return ev, nil
	}
	if ev.from, err = time.ParseInLocation("2006-01-02 15:04", m[1]+" "+m[2], time.Local); err != nil {
		return ev, err
	}
	ev.to = ev.from.Add(time.Hour)
	if m[4] != "" {
		last := m[3]
		if last == "" {
			last = m[1]
		}
		if end, err := time.ParseInLocation("2006-01-02 15:04", last+" "+m[4], time.Local); err == nil && end.After(ev.from) {
			ev.to = end
		}
	}
	return ev, nil
}

func (ev event) calendar() *ical.Calendar {
	component := ical.NewEvent()
	component.Props.SetText(ical.PropUID, loopy.NewID()+loopy.NewID()+"@1loop")
	component.Props.SetDateTime(ical.PropDateTimeStamp, time.Now().UTC())
	component.Props.SetText(ical.PropSummary, ev.what)
	if ev.day {
		component.Props.SetDate(ical.PropDateTimeStart, ev.from)
		component.Props.SetDate(ical.PropDateTimeEnd, ev.to)
	} else {
		component.Props.SetDateTime(ical.PropDateTimeStart, ev.from.UTC())
		component.Props.SetDateTime(ical.PropDateTimeEnd, ev.to.UTC())
	}
	if ev.where != "" {
		component.Props.SetText(ical.PropLocation, ev.where)
	}
	if ev.notes != "" {
		component.Props.SetText(ical.PropDescription, ev.notes)
	}
	calendar := ical.NewCalendar()
	calendar.Props.SetText(ical.PropVersion, "2.0")
	calendar.Props.SetText(ical.PropProductID, "-//1loop//Loopy//EN")
	calendar.Children = append(calendar.Children, component.Component)
	return calendar
}

func (ev event) ics() string {
	var b bytes.Buffer
	ical.NewEncoder(&b).Encode(ev.calendar())
	return b.String()
}

func calendars(ctx context.Context, account loopy.Account) (*caldav.Client, []caldav.Calendar, error) {
	transport := webdav.HTTPClientWithBasicAuth(&http.Client{Timeout: 25 * time.Second}, account.Address, account.Password)
	c, err := caldav.NewClient(transport, account.CalDAV)
	if err != nil {
		return nil, nil, err
	}
	principal, err := c.FindCurrentUserPrincipal(ctx)
	if err != nil {
		return nil, nil, err
	}
	home, err := c.FindCalendarHomeSet(ctx, principal)
	if err != nil {
		return nil, nil, err
	}
	list, err := c.FindCalendars(ctx, home)
	if err == nil && len(list) == 0 {
		err = errors.New("it holds no calendar")
	}
	return c, list, err
}

func (e *Engine) calendarTool(ctx context.Context, args toolArgs) (string, error) {
	account, ok := e.account("calendar")
	if !ok {
		return "", errors.New("no calendar is connected: the user can add one in Settings → Loopy → Accounts")
	}
	from, err := time.ParseInLocation("2006-01-02", args.From, time.Local)
	if err != nil {
		return "", errors.New("from is YYYY-MM-DD")
	}
	to, err := time.ParseInLocation("2006-01-02", args.To, time.Local)
	if err != nil {
		to = from
	}
	to = to.AddDate(0, 0, 1)
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	found, err := events(ctx, account, from, to)
	if err != nil {
		return "", err
	}
	if len(found) == 0 {
		return "Nothing.", nil
	}
	return untrusted("calendar", strings.Join(found, "\n")), nil
}

func events(ctx context.Context, account loopy.Account, from, to time.Time) ([]string, error) {
	c, list, err := calendars(ctx, account)
	if err != nil {
		return nil, err
	}
	type row struct {
		at   time.Time
		text string
	}
	var rows []row
	query := &caldav.CalendarQuery{
		CompRequest: caldav.CalendarCompRequest{Name: "VCALENDAR", AllProps: true, AllComps: true},
		CompFilter:  caldav.CompFilter{Name: "VCALENDAR", Comps: []caldav.CompFilter{{Name: "VEVENT", Start: from, End: to}}},
	}
	for _, calendar := range list {
		if len(calendar.SupportedComponentSet) > 0 && !slices.Contains(calendar.SupportedComponentSet, "VEVENT") {
			continue
		}
		objects, err := c.QueryCalendar(ctx, calendar.Path, query)
		if err != nil {
			continue
		}
		for _, object := range objects {
			for _, ev := range object.Data.Events() {
				starts := []time.Time{}
				if set, err := ev.RecurrenceSet(time.Local); err == nil && set != nil {
					starts = set.Between(from, to, true)
				} else if at, err := ev.DateTimeStart(time.Local); err == nil {
					starts = append(starts, at)
				}
				what, _ := ev.Props.Text(ical.PropSummary)
				where, _ := ev.Props.Text(ical.PropLocation)
				for _, at := range starts {
					text := at.Local().Format("Mon 2006-01-02 15:04") + " " + what
					if where != "" {
						text += " (" + where + ")"
					}
					rows = append(rows, row{at, text})
				}
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].at.Before(rows[j].at) })
	var out []string
	for _, r := range rows[:min(len(rows), 80)] {
		out = append(out, r.text)
	}
	return out, nil
}

func (e *Engine) addEvent(ctx context.Context, account loopy.Account, ev event) error {
	c, list, err := calendars(ctx, account)
	if err != nil {
		return err
	}
	for _, calendar := range list {
		if len(calendar.SupportedComponentSet) == 0 || slices.Contains(calendar.SupportedComponentSet, "VEVENT") {
			_, err = c.PutCalendarObject(ctx, path.Join(calendar.Path, loopy.NewID()+".ics"), ev.calendar(), nil)
			return err
		}
	}
	return errors.New("no calendar takes events")
}
