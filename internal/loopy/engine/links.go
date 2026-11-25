// SPDX-License-Identifier: MPL-2.0

//go:build !js

package engine

// AutoLinks: the scripts Loopy makes for itself. Loopy writes one, the user
// allows it once, with what it may reach and use; from then on Loopy runs it
// with one tool, or it runs by itself and wakes Loopy when something is
// worth it. What a script may do is checked here at each call it makes; the
// model never sees the vault's values it uses.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"1loop/internal/agent"
	"1loop/internal/browser"
	"1loop/internal/core"
	"1loop/internal/loopy"
	"1loop/internal/loopy/python"
)

// Bounds of the AutoLinks.
const (
	maxLinks    = 100
	maxLinkCode = 200 << 10
	linkWakes   = 12 // an hour
	linkAgents  = 4  // an hour
	tempKept    = 3 * 24 * time.Hour
)

// links is what runs of them.
type links struct {
	mu       sync.Mutex
	python   *python.Python
	running  map[string]context.CancelFunc
	slots    chan struct{}
	waiting  map[string]linkWait // for the vault
	did      map[string][]time.Time
	pages    map[string]int // the scripts' pages, kept open
	progress string
}

type linkWait struct {
	why   string
	input json.RawMessage
}

func (e *Engine) linkDir(id string) string { return filepath.Join(e.dir, "autolinks", id) }

func (e *Engine) link(id string) (loopy.AutoLink, bool) {
	e.store.Lock()
	defer e.store.Unlock()
	for _, link := range e.store.Links {
		if link.ID == id || strings.EqualFold(link.Name, id) && id != "" {
			return copyLink(link), true
		}
	}
	return loopy.AutoLink{}, false
}

func copyLink(link loopy.AutoLink) loopy.AutoLink {
	link.Sites, link.Secrets, link.Rights, link.Libraries = slices.Clone(link.Sites), slices.Clone(link.Secrets), slices.Clone(link.Rights), slices.Clone(link.Libraries)
	link.Runs, link.Seen = slices.Clone(link.Runs), slices.Clone(link.Seen)
	if link.Next != nil {
		next := copyLink(*link.Next)
		link.Next = &next
	}
	return link
}

func (e *Engine) changeLink(id string, change func(*loopy.AutoLink)) {
	e.store.Lock()
	for i := range e.store.Links {
		if e.store.Links[i].ID == id {
			change(&e.store.Links[i])
		}
	}
	e.store.Unlock()
	e.tell(nil, nil, true)
	e.save()
}

func (e *Engine) viewLinks() []loopy.AutoLink {
	e.store.Lock()
	var out []loopy.AutoLink
	for _, link := range e.store.Links {
		link = copyLink(link)
		link.Seen = nil
		out = append(out, link)
	}
	e.store.Unlock()
	e.links.mu.Lock()
	defer e.links.mu.Unlock()
	for i := range out {
		out[i].Running = e.links.running[out[i].ID] != nil
		if _, ok := e.links.waiting[out[i].ID]; ok {
			out[i].Waits = "the vault"
		}
	}
	return out
}

// checkLink is an AutoLink that makes sense, its lists in order.
func checkLink(link *loopy.AutoLink) error {
	link.Name = strings.Join(strings.Fields(link.Name), " ")
	if link.Name == "" || len(link.Name) > 60 {
		return errors.New("give it a short name")
	}
	if link.About = strings.TrimSpace(link.About); link.About == "" {
		return errors.New("say what it does")
	}
	tidy := func(list []string, each func(string) (string, error)) ([]string, error) {
		var out []string
		for _, item := range list {
			item, err := each(strings.TrimSpace(item))
			if err != nil {
				return nil, err
			}
			if item != "" && !slices.Contains(out, item) {
				out = append(out, item)
			}
		}
		slices.Sort(out)
		return out, nil
	}
	var err error
	if link.Sites, err = tidy(link.Sites, func(s string) (string, error) {
		if s == "*" || s == "" {
			return s, nil
		}
		host := site(s)
		if h, _, ok := strings.Cut(host, ":"); ok && !strings.Contains(h, ":") && strings.Count(host, ":") == 1 {
			host = h
		}
		if strings.ContainsAny(host, "/ *") || !strings.Contains(host, ".") && host != "localhost" {
			return "", fmt.Errorf("%q is not a site's name, such as api.example.com", s)
		}
		return host, nil
	}); err != nil {
		return err
	}
	if link.Secrets, err = tidy(link.Secrets, func(s string) (string, error) { return s, nil }); err != nil {
		return err
	}
	if link.Rights, err = tidy(link.Rights, func(s string) (string, error) {
		if s != "" && !slices.ContainsFunc(loopy.LinkRights, func(r struct{ Name, Says string }) bool { return r.Name == s }) {
			return "", fmt.Errorf("%q is not a right", s)
		}
		return s, nil
	}); err != nil {
		return err
	}
	if link.Libraries, err = tidy(link.Libraries, func(s string) (string, error) {
		if s != "" && !python.Requirement.MatchString(s) {
			return "", fmt.Errorf("%q is not a library's name on PyPI, such as \"requests\"", s)
		}
		return s, nil
	}); err != nil {
		return err
	}
	if link.When = strings.TrimSpace(link.When); link.When != "" {
		if err := loopy.CheckSchedule(link.When, time.Minute); err != nil {
			return err
		}
	}
	return nil
}

// wider says what next may do that link may not.
func wider(link, next loopy.AutoLink) (out []string) {
	more := func(has, wants []string, what string) {
		for _, w := range wants {
			if !slices.Contains(has, w) && !slices.Contains(has, "*") {
				out = append(out, what+w)
			}
		}
	}
	more(link.Sites, next.Sites, "site ")
	more(link.Secrets, next.Secrets, "vault entry ")
	more(link.Rights, next.Rights, "right ")
	more(link.Libraries, next.Libraries, "library ")
	if next.When != link.When && next.When != "" {
		out = append(out, "runs "+loopy.Describe(next.When))
	}
	return out
}

// powerful says whether a script's change matters more than itself.
func powerful(link loopy.AutoLink) bool {
	return len(link.Secrets) > 0 || len(link.Rights) > 0 || slices.Contains(link.Sites, "*")
}

// saveLink is loopy_autolink_save.
func (e *Engine) saveLink(ctx context.Context, r *run, args toolArgs) (string, error) {
	old, exists := loopy.AutoLink{}, false
	if args.ID != "" {
		if old, exists = e.link(args.ID); !exists {
			return "", errors.New("no AutoLink has that id: list them")
		}
	}
	if args.Action == "remove" {
		if !exists {
			return "", errors.New("give the id of the AutoLink to remove")
		}
		answer, err := e.ask(ctx, loopy.Card{Kind: "approval", Title: "Remove the AutoLink “" + old.Name + "”", Body: old.About, Warning: "Its script and what it keeps go for good", Actions: []string{"Remove", "Keep"}})
		if err != nil {
			return "", err
		}
		if answer.Answer != "Remove" {
			return "The user keeps it.", nil
		}
		return "Removed.", e.RemoveLink(old.ID)
	}
	next := loopy.AutoLink{ID: old.ID, Name: args.Name, About: args.About, Input: args.Input, Sites: args.Sites, Secrets: args.Secrets, Rights: args.Rights, Libraries: args.Libraries, When: args.When, Do: args.Do}
	if err := checkLink(&next); err != nil {
		return "", err
	}
	code := args.Code
	if code == "" && exists { // only what is said of it changes
		code = e.linkCode(old.ID).Code
		if waits := e.linkCode(old.ID).Next; waits != "" {
			code = waits
		}
	} else if args.Old != "" {
		current := e.linkCode(old.ID)
		base := current.Code
		if current.Next != "" {
			base = current.Next
		}
		if n := strings.Count(base, args.Old); n != 1 {
			return "", fmt.Errorf("old is in the script %d times: it must be there once", n)
		}
		code = strings.Replace(base, args.Old, args.Code, 1)
	}
	if strings.TrimSpace(code) == "" {
		return "", errors.New("give the script")
	}
	if len(code) > maxLinkCode {
		return "", errors.New("that script is too long")
	}
	for _, secret := range e.secrets() {
		if len(secret) >= 6 && strings.Contains(code, secret) {
			return "", errors.New("refused: the script holds a value of the vault. Use autolink.secret(\"entry\")")
		}
	}
	if keyPattern.MatchString(code) {
		return "", errors.New("refused: the script holds a key. The user puts it in their vault (Settings → Loopy → Vault); use autolink.secret(\"entry\")")
	}
	e.set("copy", "Checking the script")
	if err := e.links.python.Check(ctx, code); err != nil {
		return "", err
	}
	for _, name := range next.Secrets { // named as the vault does, when it can be read
		if e.safe != nil && e.safe.State() == "unlocked" {
			if _, err := e.safe.named(name); err != nil {
				return "", fmt.Errorf("the vault holds no entry %q: ask the user to add it (Settings → Loopy → Vault), never for the value", name)
			}
		}
	}
	e.store.Lock()
	for _, other := range e.store.Links {
		if other.ID != next.ID && strings.EqualFold(other.Name, next.Name) {
			e.store.Unlock()
			return "", errors.New("another AutoLink has that name: change that one, or choose another name")
		}
	}
	count := len(e.store.Links)
	e.store.Unlock()
	if exists && old.Allowed {
		more := wider(old, next)
		changed := code != e.linkCode(old.ID).Code
		// On its own run what Loopy was told comes from outside: a script
		// that can do more than read does not change without the user.
		if len(more) == 0 && !(changed && r.why != "" && powerful(next)) {
			if err := e.writeLink(old.ID, "main.py", code); err != nil {
				return "", err
			}
			os.Remove(filepath.Join(e.linkDir(old.ID), "next.py"))
			e.changeLink(old.ID, func(l *loopy.AutoLink) {
				l.Name, l.About, l.Input, l.Sites, l.Secrets, l.Rights, l.Libraries, l.When, l.Do = next.Name, next.About, next.Input, next.Sites, next.Secrets, next.Rights, next.Libraries, next.When, next.Do
				l.Changed, l.Failed, l.Next = e.now(), 0, nil
			})
			e.dropLinkCards(old.ID)
			e.log("Changed the AutoLink " + next.Name + ".")
			return "Changed (id " + old.ID + "): what it may do is as the user allowed. Run it to check.", nil
		}
		if err := e.writeLink(old.ID, "next.py", code); err != nil {
			return "", err
		}
		e.changeLink(old.ID, func(l *loopy.AutoLink) { l.Next = &next })
		e.dropLinkCards(old.ID)
		e.linkCard(r, old.ID, next, more)
		return "The change waits for the user's yes on a card (the old one still runs): end your turn now.", nil
	}
	if !exists {
		if count >= maxLinks {
			return "", errors.New("there are too many AutoLinks: remove some")
		}
		next.ID = loopy.NewID()
	}
	if err := e.writeLink(next.ID, "main.py", code); err != nil {
		return "", err
	}
	next.Changed = e.now()
	e.store.Lock()
	e.store.Links = append(slices.DeleteFunc(e.store.Links, func(l loopy.AutoLink) bool { return l.ID == next.ID }), next)
	e.store.Unlock()
	e.save()
	e.dropLinkCards(next.ID)
	e.linkCard(r, next.ID, next, nil)
	return "Saved as " + next.ID + ", not allowed yet: the user says yes on a card. End your turn now; you are told of their answer, and test it then.", nil
}

const (
	linkAuto = "Run by itself"
	linkAsk  = "Ask me each time"
)

// linkCard asks for the user's yes.
func (e *Engine) linkCard(r *run, id string, link loopy.AutoLink, more []string) {
	card := loopy.Card{Kind: "autolink", Title: link.Name, Subtitle: "AutoLink", Body: link.About, Link: id, Actions: []string{linkAuto, linkAsk, "Discard"}}
	if len(more) > 0 {
		card.Subtitle = "AutoLink, changed: " + strings.Join(more, ", ")
	}
	add := func(label string, list []string) {
		if len(list) > 0 {
			card.Fields = append(card.Fields, loopy.Field{Label: label, Value: strings.Join(list, ", ")})
		}
	}
	if slices.Contains(link.Sites, "*") {
		card.Warning = "It may reach any site"
		add("Reaches", []string{"any site"})
	} else {
		add("Reaches", link.Sites)
	}
	add("Uses from the vault", link.Secrets)
	for _, right := range loopy.LinkRights {
		if slices.Contains(link.Rights, right.Name) {
			add("May", []string{right.Says})
		}
	}
	add("Libraries", link.Libraries)
	if link.When != "" {
		add("Runs", []string{loopy.Describe(link.When)})
	}
	if link.Do != "" {
		add("When it finds something", []string{link.Do})
	}
	if r != nil {
		r.said = true
	}
	message := e.add(loopy.Message{From: "loopy", Card: &card})
	e.notify("An AutoLink to allow", link.Name, &message, false)
}

func (e *Engine) dropLinkCards(id string) {
	for _, card := range e.waiting() {
		if message, ok := e.message(card); ok && message.Card.Link == id && message.Card.Kind == "autolink" {
			e.close(card, "Replaced", "")
		}
	}
}

func (e *Engine) answerLink(message loopy.Message, answer loopy.Answer) error {
	id := message.Card.Link
	link, ok := e.link(id)
	if !ok {
		return errors.New("this AutoLink is gone")
	}
	if answer.Answer == "Discard" {
		e.close(message.ID, "Discarded", "")
		if link.Allowed {
			os.Remove(filepath.Join(e.linkDir(id), "next.py"))
			e.changeLink(id, func(l *loopy.AutoLink) { l.Next = nil })
		} else {
			e.RemoveLink(id)
		}
		e.enqueue(queued{text: fmt.Sprintf("[The user said no to the AutoLink %q. Do not propose it again unless they ask.]", link.Name)}, false)
		return nil
	}
	if err := e.allowLink(id, answer.Answer == linkAsk); err != nil {
		return err
	}
	e.close(message.ID, answer.Answer, "")
	how := "it runs by itself"
	if answer.Answer == linkAsk {
		how = "they confirm each run"
	}
	e.log(fmt.Sprintf("The user allowed the AutoLink %s: %s.", link.Name, how))
	e.enqueue(queued{text: fmt.Sprintf("[The user allowed the AutoLink %q (id %s): %s. Test it now with loopy_autolink_run, fix it if it fails, then go on with what it was for.]", link.Name, id, how)}, false)
	return nil
}

// allowLink is the user's yes, to it or to the change that waits.
func (e *Engine) allowLink(id string, ask bool) error {
	link, ok := e.link(id)
	if !ok {
		return errors.New("no such AutoLink")
	}
	if link.Next != nil {
		code := e.linkCode(id).Next
		if code == "" {
			return errors.New("that change is gone")
		}
		if err := e.writeLink(id, "main.py", code); err != nil {
			return err
		}
		os.Remove(filepath.Join(e.linkDir(id), "next.py"))
	}
	e.changeLink(id, func(l *loopy.AutoLink) {
		if n := l.Next; n != nil {
			l.Name, l.About, l.Input, l.Sites, l.Secrets, l.Rights, l.Libraries, l.When, l.Do = n.Name, n.About, n.Input, n.Sites, n.Secrets, n.Rights, n.Libraries, n.When, n.Do
			l.Next = nil
		}
		if !l.Allowed { // its first run is the test
			l.LastRun = e.now()
		}
		l.Allowed, l.Ask, l.Failed, l.Changed = true, ask, 0, e.now()
		l.On = l.When != ""
	})
	return nil
}

func (e *Engine) writeLink(id, name, code string) error {
	dir := e.linkDir(id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmp := filepath.Join(dir, name+".tmp")
	if err := os.WriteFile(tmp, []byte(code), 0600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, name))
}

func (e *Engine) linkCode(id string) (out loopy.LinkCode) {
	if link, ok := e.link(id); ok {
		code, _ := os.ReadFile(filepath.Join(e.linkDir(link.ID), "main.py"))
		next, _ := os.ReadFile(filepath.Join(e.linkDir(link.ID), "next.py"))
		out.Code, out.Next = string(code), string(next)
	}
	return
}

// RemoveLink deletes an AutoLink, its script and what it keeps.
func (e *Engine) RemoveLink(id string) error {
	link, ok := e.link(id)
	if !ok {
		return errors.New("no such AutoLink")
	}
	e.StopLink(link.ID)
	e.store.Lock()
	e.store.Links = slices.DeleteFunc(e.store.Links, func(l loopy.AutoLink) bool { return l.ID == link.ID })
	e.store.Unlock()
	e.dropLinkCards(link.ID)
	e.tell(nil, nil, true)
	e.save()
	e.log("Removed the AutoLink " + link.Name + ".")
	return os.RemoveAll(e.linkDir(link.ID))
}

// StopLink ends the run in progress.
func (e *Engine) StopLink(id string) {
	e.links.mu.Lock()
	stop := e.links.running[id]
	delete(e.links.waiting, id)
	e.links.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// EditLink is the user changing one from its page: theirs, no card.
func (e *Engine) EditLink(link loopy.AutoLink, code string) error {
	old, ok := e.link(link.ID)
	if !ok {
		return errors.New("no such AutoLink")
	}
	if err := checkLink(&link); err != nil {
		return err
	}
	if code != "" && code != e.linkCode(old.ID).Code {
		if len(code) > maxLinkCode {
			return errors.New("that script is too long")
		}
		if err := e.links.python.Check(e.ctx, code); err != nil {
			return err
		}
		if err := e.writeLink(old.ID, "main.py", code); err != nil {
			return err
		}
	}
	e.changeLink(old.ID, func(l *loopy.AutoLink) {
		if l.When != link.When {
			l.LastRun, l.On = e.now(), link.When != "" && l.Allowed
		}
		l.Name, l.About, l.Sites, l.Secrets, l.Rights, l.Libraries, l.When, l.Do, l.Ask = link.Name, link.About, link.Sites, link.Secrets, link.Rights, link.Libraries, link.When, link.Do, link.Ask
		l.Failed, l.Changed = 0, e.now()
	})
	return nil
}

// doLink is a change from the AutoLinks' page.
func (e *Engine) doLink(a loopy.Action) error {
	link, ok := e.link(a.ID)
	if !ok {
		return errors.New("no such AutoLink")
	}
	switch a.Do {
	case "link.on", "link.off":
		if !link.Allowed {
			return errors.New("allow it first")
		}
		if link.When == "" {
			return errors.New("it has no schedule")
		}
		e.changeLink(link.ID, func(l *loopy.AutoLink) { l.On, l.LastRun, l.Failed = a.Do == "link.on", e.now(), 0 })
	case "link.ask":
		e.changeLink(link.ID, func(l *loopy.AutoLink) { l.Ask = a.On })
	case "link.allow":
		if err := e.allowLink(link.ID, a.On); err != nil {
			return err
		}
		e.dropLinkCards(link.ID)
	case "link.run":
		if !link.Allowed {
			return errors.New("allow it first")
		}
		var input json.RawMessage
		if text := strings.TrimSpace(a.Text); text != "" {
			if input = json.RawMessage(text); !json.Valid(input) {
				input, _ = json.Marshal(text)
			}
		}
		go e.runLinkAlone(link, "you", input)
	case "link.stop":
		e.StopLink(link.ID)
	case "link.remove":
		return e.RemoveLink(link.ID)
	case "link.edit":
		if a.Link == nil {
			return errors.New("nothing to change")
		}
		a.Link.ID = link.ID
		return e.EditLink(*a.Link, a.Text)
	default:
		return errors.New("unknown action")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Running.

// linkResult is a run as Loopy reads it.
type linkResult struct {
	text   string
	failed string
	files  []string
	images []core.ImagePart
}

// runLink runs an AutoLink now. It is the user's to have allowed it.
func (e *Engine) runLink(ctx context.Context, link loopy.AutoLink, why string, input json.RawMessage, limit time.Duration) (linkResult, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	e.links.mu.Lock()
	if e.links.running[link.ID] != nil {
		e.links.mu.Unlock()
		return linkResult{}, errors.New("it is already running")
	}
	e.links.running[link.ID] = cancel
	e.links.mu.Unlock()
	e.tell(nil, nil, true)
	defer func() {
		e.links.mu.Lock()
		delete(e.links.running, link.ID)
		e.links.mu.Unlock()
		e.tell(nil, nil, true)
	}()
	select { // a few at a time
	case e.links.slots <- struct{}{}:
		defer func() { <-e.links.slots }()
	case <-ctx.Done():
		return linkResult{}, ctx.Err()
	}
	begun := e.now()
	dir := e.linkDir(link.ID)
	name := "temp/" + slug(link.Name) + "-" + begun.Format("20060102-150405.000")
	out := filepath.Join(e.folder.dir, filepath.FromSlash(name))
	s := &linkScript{e: e, link: link, why: why, test: why == "test"}
	defer s.close()
	var result python.Result
	err := e.links.python.Install(ctx, dir, link.Libraries, func(text string) { e.set("copy", text) })
	if err == nil {
		result, err = e.links.python.Run(ctx, python.Job{Dir: dir, Data: filepath.Join(dir, "data"), Out: out, Input: input, Sites: link.Sites, Test: s.test, Limit: limit, Call: s.call})
	}
	if err != nil {
		result.Error = err.Error()
	}
	secrets := append(e.secrets(), s.given...)
	answer := linkResult{failed: redact(result.Error, secrets)}
	var b strings.Builder
	for _, output := range result.Outputs {
		var text string
		if json.Unmarshal(output, &text) != nil {
			text = string(output)
		}
		b.WriteString(strings.TrimRight(text, "\n") + "\n")
	}
	if printed := strings.TrimSpace(result.Printed); printed != "" {
		b.WriteString("[printed]\n" + printed + "\n")
	}
	answer.text = redact(strings.TrimSpace(b.String()), secrets)
	for _, file := range result.Files {
		kept, err := e.keepLinkFile(file, out, name)
		if err != nil {
			continue
		}
		answer.files = append(answer.files, "/"+kept)
		if picture, ok := e.picture(kept); ok && len(answer.images) < 4 {
			answer.images = append(answer.images, picture)
		}
	}
	os.Remove(out) // empty
	e.changeLink(link.ID, func(l *loopy.AutoLink) {
		l.LastRun = e.now()
		run := loopy.LinkRun{Time: begun, Why: why, Took: e.now().Sub(begun).Milliseconds(), Error: lastLine(answer.failed), Outcome: firstWords(answer.text, 24), Files: answer.files}
		l.Runs = append(l.Runs, run)
		l.Runs = l.Runs[max(0, len(l.Runs)-30):]
		if answer.failed == "" {
			l.Failed = 0
		} else if ctx.Err() == nil {
			l.Failed++
		}
	})
	if err := ctx.Err(); err != nil {
		return answer, err
	}
	return answer, nil
}

func lastLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	line := lines[len(lines)-1]
	return line[:min(len(line), 300)]
}

func slug(name string) string {
	var b strings.Builder
	for _, r := range plain(name) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else if s := b.String(); s != "" && !strings.HasSuffix(s, "-") {
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-") + ""
}

// keepLinkFile is where Loopy finds a file a script gave back: it is there
// already, or a copy is made of one of the script's own.
func (e *Engine) keepLinkFile(file, out, name string) (string, error) {
	real, err := filepath.EvalSymlinks(out)
	if err != nil {
		return "", err
	}
	if relative, err := filepath.Rel(real, file); err == nil && filepath.IsLocal(relative) {
		return name + "/" + filepath.ToSlash(relative), nil
	}
	info, err := os.Stat(file)
	if err != nil || info.Size() > maxFile {
		return "", errors.New("too large")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	kept := name + "/" + filepath.Base(file)
	return kept, e.folder.write("/"+kept, data, false)
}

// picture is a file of the folder a model can look at.
func (e *Engine) picture(name string) (core.ImagePart, bool) {
	data, err := e.folder.bytes("/"+name, 8<<20)
	if err != nil {
		return core.ImagePart{}, false
	}
	switch mime := http.DetectContentType(data); mime {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
		return core.ImagePart{Mime: mime, Data: base64.StdEncoding.EncodeToString(data)}, true
	}
	return core.ImagePart{}, false
}

// runLinkTool is loopy_autolink_run.
func (e *Engine) runLinkTool(ctx context.Context, r *run, args toolArgs) (agent.PanelToolResult, error) {
	link, ok := e.link(args.ID)
	if !ok {
		return agent.PanelToolResult{}, errors.New("no such AutoLink: list them")
	}
	if !link.Allowed {
		return agent.PanelToolResult{}, errors.New("the user has not allowed it yet: their card waits")
	}
	var input json.RawMessage
	if text := strings.TrimSpace(args.Input); text != "" {
		if input = json.RawMessage(text); !json.Valid(input) {
			return agent.PanelToolResult{}, errors.New("input is a JSON value, or empty")
		}
		if _, found := sensitive(args.Input, e.secrets()); len(found) > 0 {
			return agent.PanelToolResult{}, fmt.Errorf("refused: that holds %s", strings.Join(found, " and "))
		}
	}
	if link.Ask {
		answer, err := e.ask(ctx, loopy.Card{Kind: "autolink_run", Title: "Run “" + link.Name + "”", Subtitle: "AutoLink", Body: strings.TrimSpace(link.About + "\n" + args.Input), Link: link.ID, Actions: []string{"Run", "Not now"}})
		if err != nil {
			return agent.PanelToolResult{}, err
		}
		if answer.Answer != "Run" {
			return agent.PanelToolResult{}, errors.New("the user does not want it run now")
		}
	}
	if len(link.Secrets) > 0 {
		if err := e.openVaultFor(ctx, "The AutoLink “"+link.Name+"” needs "+strings.Join(link.Secrets, ", ")+"."); err != nil {
			return agent.PanelToolResult{}, err
		}
	}
	e.set("copy", link.Name)
	why := "Loopy"
	if args.Test {
		why = "test"
	}
	got, err := e.runLink(ctx, link, why, input, time.Duration(args.Seconds)*time.Second)
	if err != nil {
		return agent.PanelToolResult{}, err
	}
	var b strings.Builder
	if got.text != "" {
		text := got.text
		if len(text) > 48<<10 {
			text = text[:48<<10] + "\n(the rest is cut: have it write a file)"
		}
		b.WriteString(untrusted("AutoLink's output", text) + "\n")
	}
	if len(got.files) > 0 {
		b.WriteString("Files, in your folder for three days: " + strings.Join(got.files, ", ") + "\n")
	}
	if got.failed != "" {
		return agent.PanelToolResult{}, errors.New(strings.TrimSpace(b.String() + "\nIt failed:\n" + untrusted("error", got.failed)))
	}
	if b.Len() == 0 {
		b.WriteString("It gave nothing back.")
	}
	return agent.PanelToolResult{Text: strings.TrimSpace(b.String()), Images: got.images}, nil
}

// runLinkAlone is a run nobody waits for: the schedule's or the user's.
func (e *Engine) runLinkAlone(link loopy.AutoLink, why string, input json.RawMessage) {
	if len(link.Secrets) > 0 && e.safe != nil && e.safe.State() != "unlocked" {
		e.links.mu.Lock()
		_, already := e.links.waiting[link.ID]
		e.links.waiting[link.ID] = linkWait{why, input}
		first := len(e.links.waiting) == 1
		e.links.mu.Unlock()
		e.changeLink(link.ID, func(l *loopy.AutoLink) { l.LastRun = e.now() })
		if !already && first {
			card := unlockCard("The AutoLink “" + link.Name + "” waits for it.")
			message := e.add(loopy.Message{From: "loopy", Card: &card})
			e.notify("Your vault is locked", "The AutoLink “"+link.Name+"” waits for it.", &message, false)
		}
		return
	}
	got, err := e.runLink(e.ctx, link, why, input, 0)
	if err != nil {
		return
	}
	link, _ = e.link(link.ID)
	switch {
	case got.failed != "" && why == "you":
		e.add(loopy.Message{From: "loopy", Note: "AutoLink " + link.Name + " failed: " + lastLine(got.failed)})
	case got.failed != "" && link.Failed == 3: // once, not at each run
		e.add(loopy.Message{From: "loopy", Note: "AutoLink " + link.Name + " failed three times"})
		e.enqueue(queued{why: "the AutoLink " + link.Name + " fails", text: fmt.Sprintf("[Your AutoLink %q (id %s) failed three times in a row: %s\nRead its script (loopy_autolinks), find why, fix it and run it. If you cannot, tell the user in a sentence.]", link.Name, link.ID, untrusted("error", got.failed))}, false)
	case why == "you" && (got.text != "" || len(got.files) > 0):
		e.add(loopy.Message{From: "loopy", Note: "AutoLink " + link.Name, Text: firstWords(got.text, 120), Files: got.files})
	}
}

// resumeLinks runs what waited for the vault.
func (e *Engine) resumeLinks() {
	e.links.mu.Lock()
	waiting := e.links.waiting
	e.links.waiting = map[string]linkWait{}
	e.links.mu.Unlock()
	for id, wait := range waiting {
		if link, ok := e.link(id); ok {
			go e.runLinkAlone(link, wait.why, wait.input)
		}
	}
}

// tickLinks starts what its time has come for.
func (e *Engine) tickLinks(now time.Time) {
	e.store.Lock()
	var due []loopy.AutoLink
	for _, link := range e.store.Links {
		if link.On && link.Allowed && link.When != "" {
			if next := loopy.Next(link.When, link.LastRun.In(now.Location())); !next.IsZero() && !next.After(now) {
				due = append(due, copyLink(link))
			}
		}
	}
	e.store.Unlock()
	for _, link := range due {
		e.links.mu.Lock()
		_, waits := e.links.waiting[link.ID]
		busy := e.links.running[link.ID] != nil || waits
		e.links.mu.Unlock()
		if busy {
			continue
		}
		e.changeLink(link.ID, func(l *loopy.AutoLink) {
			l.LastRun = now
			if strings.HasPrefix(l.When, "once ") {
				l.On = false
			}
		})
		if link.Ask {
			e.askLinkRun(link)
			continue
		}
		go e.runLinkAlone(link, "schedule", nil)
	}
}

// askLinkRun asks for the schedule's run, once while the card waits.
func (e *Engine) askLinkRun(link loopy.AutoLink) {
	for _, id := range e.waiting() {
		if message, ok := e.message(id); ok && message.Card.Kind == "autolink_run" && message.Card.Link == link.ID {
			return
		}
	}
	message := e.add(loopy.Message{From: "loopy", Card: &loopy.Card{Kind: "autolink_run", Title: "Run “" + link.Name + "”", Subtitle: "AutoLink, " + loopy.Describe(link.When), Body: link.About, Link: link.ID, Actions: []string{"Run", "Not now"}}})
	e.notify("An AutoLink to run", link.Name, &message, false)
}

func (e *Engine) answerLinkRun(message loopy.Message, answer loopy.Answer) error {
	link, ok := e.link(message.Card.Link)
	if !ok || !link.Allowed {
		return errors.New("this AutoLink is gone")
	}
	e.close(message.ID, answer.Answer, "")
	if answer.Answer == "Run" {
		go e.runLinkAlone(link, "schedule", nil)
	}
	return nil
}

// cleanTemp drops what scripts gave back days ago.
func (e *Engine) cleanTemp() {
	dir := filepath.Join(e.folder.dir, "temp")
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if info, err := entry.Info(); err == nil && time.Since(info.ModTime()) > tempKept {
			os.RemoveAll(filepath.Join(dir, entry.Name()))
		}
	}
}

// ---------------------------------------------------------------------------
// What a script asks of 1loop.

type linkScript struct {
	e     *Engine
	link  loopy.AutoLink
	why   string
	test  bool
	given []string // values handed out, never shown
	mine  map[string]*browser.Page
}

func (s *linkScript) may(right string) error {
	if slices.Contains(s.link.Rights, right) {
		return nil
	}
	return fmt.Errorf("the user did not give this AutoLink the right %q: change it with loopy_autolink_save", right)
}

// often refuses what comes too often.
func (s *linkScript) often(what string, most int) error {
	l := &s.e.links
	key, now := s.link.ID+" "+what, s.e.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.did[key] = slices.DeleteFunc(l.did[key], func(t time.Time) bool { return now.Sub(t) > time.Hour })
	if len(l.did[key]) >= most {
		return fmt.Errorf("too often: at most %d an hour", most)
	}
	l.did[key] = append(l.did[key], now)
	return nil
}

// fresh says whether a key is new, and keeps it.
func (s *linkScript) fresh(what, key string) bool {
	if key == "" {
		return true
	}
	key = what + ":" + key
	is := true
	s.e.changeLink(s.link.ID, func(l *loopy.AutoLink) {
		if is = !slices.Contains(l.Seen, key); is {
			l.Seen = append(l.Seen, key)
			l.Seen = l.Seen[max(0, len(l.Seen)-1000):]
		}
	})
	return is
}

// entry is a vault entry the script may use, on its own sites only.
func (s *linkScript) entry(name string) (secretEntry, error) {
	if !slices.ContainsFunc(s.link.Secrets, func(n string) bool { return strings.EqualFold(n, strings.TrimSpace(name)) }) {
		return secretEntry{}, fmt.Errorf("the user did not let this AutoLink use %q (it may use: %s)", name, strings.Join(s.link.Secrets, ", "))
	}
	if s.e.safe == nil {
		return secretEntry{}, errors.New("the vault cannot be read")
	}
	entry, err := s.e.safe.named(name)
	if err != nil {
		return entry, err
	}
	if entry.Kind == "signin" && entry.Site != "" && !slices.ContainsFunc(s.link.Sites, func(site string) bool {
		return site == "*" || sameSite(site, entry.Site) || sameSite(entry.Site, site) || related(site, entry.Site)
	}) {
		return entry, fmt.Errorf("%q is for %s, which is not one of this AutoLink's sites", name, entry.Site)
	}
	return entry, nil
}

// related says whether two hosts are of one site: api.example.com and
// www.example.com.
func related(a, b string) bool {
	root := func(host string) string {
		parts := strings.Split(host, ".")
		return strings.Join(parts[max(0, len(parts)-2):], ".")
	}
	return strings.Count(a, ".") > 0 && strings.Count(b, ".") > 0 && root(a) == root(b) && !publicSuffix(root(a))
}

func publicSuffix(root string) bool {
	first, _, _ := strings.Cut(root, ".")
	return len(first) <= 3 || slices.Contains([]string{"gouv", "asso", "github", "gitlab", "pages", "netlify", "vercel", "herokuapp", "web", "blogspot", "firebaseapp", "workers"}, first)
}

func (s *linkScript) call(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	e := s.e
	var args struct {
		Name, Text, Key, Title, Level, Project, Brief, Path, Method string
		Body                                                        json.RawMessage
	}
	if !strings.HasPrefix(name, "page.") {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, errors.New("these arguments cannot be read")
		}
	}
	switch name {
	case "secret":
		entry, err := s.entry(args.Name)
		if err != nil {
			return nil, err
		}
		values, err := entry.filled(e.now())
		if err != nil {
			return nil, err
		}
		for field, value := range values {
			if entry.Kind != "contact" && field != "user" && field != "note" {
				s.given = append(s.given, value)
			}
		}
		return values, nil
	case "wake":
		if strings.TrimSpace(args.Text) == "" {
			return nil, errors.New("say what happened")
		}
		if s.test {
			return "a test: Loopy is not woken", nil
		}
		if !s.fresh("wake", args.Key) {
			return "already said", nil
		}
		if err := s.often("wake", linkWakes); err != nil {
			return nil, err
		}
		s.wake(args.Text)
		return "woken", nil
	case "notify":
		if s.test {
			return "a test: not sent", nil
		}
		if err := s.often("notify", 20); err != nil {
			return nil, err
		}
		e.notice(loopy.Notice{Title: args.Title, Text: redact(args.Text, e.secrets()), Level: args.Level})
		return "sent", nil
	case "agent":
		if err := s.may("agent"); err != nil {
			return nil, err
		}
		if s.test {
			return "a test: not started", nil
		}
		if !s.fresh("agent", args.Key) {
			return "already started", nil
		}
		if err := s.often("agent", linkAgents); err != nil {
			return nil, err
		}
		return e.startTask(args.Project, args.Title, redact(args.Brief, e.secrets()), "the AutoLink "+s.link.Name)
	case "github":
		return s.github(ctx, args.Method, args.Path, args.Body)
	}
	if strings.HasPrefix(name, "page.") {
		return s.page(ctx, strings.TrimPrefix(name, "page."), raw)
	}
	return nil, fmt.Errorf("unknown call %s", name)
}

// wake starts a run of Loopy's on what the script saw.
func (s *linkScript) wake(text string) {
	e := s.e
	text = redact(text, append(e.secrets(), s.given...))
	if len(text) > 12000 {
		text = text[:12000] + "…"
	}
	e.add(loopy.Message{From: "loopy", Note: "AutoLink " + s.link.Name + " woke me"})
	do := s.link.Do
	if do == "" {
		do = "Tell the user what matters in it."
	}
	e.enqueue(queued{why: "the AutoLink " + s.link.Name + " saw something", link: s.link.ID,
		text: fmt.Sprintf("[Your AutoLink %q woke you. What the user wants done then: %s]\nWhat it saw:\n%s", s.link.Name, do, untrusted("AutoLink's output", text))}, false)
}

func (s *linkScript) github(ctx context.Context, method, path string, body json.RawMessage) (any, error) {
	method = strings.ToUpper(method)
	right := "github_write"
	if method == "GET" || method == "HEAD" {
		right = "github"
	}
	if right == "github" && slices.Contains(s.link.Rights, "github_write") {
		right = "github_write"
	}
	if err := s.may(right); err != nil {
		return nil, err
	}
	if s.test && method != "GET" && method != "HEAD" {
		return nil, errors.New("a test changes nothing on GitHub")
	}
	parsed, err := url.Parse(path)
	if err != nil || parsed.Host != "" || parsed.Scheme != "" || !strings.HasPrefix(path, "/") {
		return nil, errors.New("path is one of the API's, such as /repos/owner/name/pulls")
	}
	auth := s.e.host.GitHub(ctx)
	if auth == "" {
		return nil, errors.New("GitHub is not connected (Settings → Tools & extensions)")
	}
	var reader io.Reader
	if len(body) > 0 && string(body) != "null" {
		reader = strings.NewReader(string(body))
	}
	request, err := http.NewRequestWithContext(ctx, method, githubAPI+path, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", auth)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	response, err := (&http.Client{Timeout: 30 * time.Second, CheckRedirect: func(r *http.Request, _ []*http.Request) error {
		if r.URL.Host != request.URL.Host {
			return http.ErrUseLastResponse
		}
		return nil
	}}).Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	headers := map[string]string{}
	for _, name := range []string{"Link", "X-RateLimit-Remaining", "ETag", "Content-Type"} {
		if value := response.Header.Get(name); value != "" {
			headers[name] = value
		}
	}
	return map[string]any{"status": response.StatusCode, "headers": headers, "body": string(data)}, nil
}
