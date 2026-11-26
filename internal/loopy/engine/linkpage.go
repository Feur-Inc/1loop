// SPDX-License-Identifier: MPL-2.0

//go:build !js

package engine

// The browser as an AutoLink's script drives it, and as Loopy inspects it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"1loop/internal/browser"
	"1loop/internal/loopy"
)

// browserRest is how long Loopy's browser stays open unused; longer when
// the user has it on show.
var browserRest, browserShown = 3 * time.Minute, 15 * time.Minute

// reaches says whether a script may act on a page there.
func (s *linkScript) reaches(address string) error {
	if strings.HasPrefix(address, "about:") || address == "" {
		return nil
	}
	if !strings.HasPrefix(address, "https://") && !strings.HasPrefix(address, "http://") {
		return errors.New("a script opens web pages only")
	}
	host := site(address)
	for _, allowed := range s.link.Sites {
		if allowed == "*" || host == allowed || strings.HasSuffix(host, "."+allowed) {
			return nil
		}
	}
	return fmt.Errorf("%s is not one of this AutoLink's sites (%s)", host, strings.Join(s.link.Sites, ", "))
}

// close closes the pages the script left open.
func (s *linkScript) close() {
	for id := range s.mine {
		s.drop(id)
	}
}

func (s *linkScript) drop(id string) {
	delete(s.mine, id)
	s.e.links.mu.Lock()
	delete(s.e.links.pages, id)
	s.e.links.mu.Unlock()
	s.e.host.ClosePage(id)
	s.e.usedBrowser()
}

type pageArgs struct {
	Page, URL, Wait, Selector, Text, Entry, Field, Key, Value, Script, Match, Type string
	X, Y, DX, DY, Timeout                                                          float64
	Width, Height                                                                  int
	Mobile, Hidden, On, Full                                                       bool
}

const (
	shownJS = `(e => { if (!e) return false; const r = e.getBoundingClientRect(), c = getComputedStyle(e); return r.width > 0 && r.height > 0 && c.visibility !== 'hidden' && c.display !== 'none' })`
	// byText is the smallest thing to press that says a text.
	byText = `(t => { const n = s => (s || '').replace(/\s+/g, ' ').trim().toLowerCase(), want = n(t), shown = ` + shownJS + `;
 const say = e => n(e.innerText || e.value || e.getAttribute('aria-label') || e.title || e.placeholder || '');
 const all = [...document.querySelectorAll('button,a,[role=button],[role=link],[role=tab],[role=menuitem],[role=option],input[type=submit],input[type=button],label,summary,td,li,span,div,p')].filter(shown);
 const found = all.filter(e => say(e) === want).concat(all.filter(e => say(e) !== want && say(e).includes(want)));
 const press = found.find(e => e.matches('button,a,[role=button],[role=link],[role=tab],[role=menuitem],[role=option],input'));
 const exact = found.filter(e => say(e) === want);
 return press && say(press) === want ? press : exact.length ? exact[exact.length - 1] : press || found.sort((a, b) => say(a).length - say(b).length)[0] || null })`
	fillAny = `(() => { const a = %s; const el = document.querySelector(a.selector); if (!el) return 'no field ' + a.selector;
 if (a.secret && el.type !== 'password') return 'that field is not a password field';
 el.focus(); if (el.isContentEditable) { el.textContent = a.value }
 else { const proto = el instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype; const set = Object.getOwnPropertyDescriptor(proto, 'value').set; set.call(el, a.value) }
 el.dispatchEvent(new InputEvent('input', {bubbles: true})); el.dispatchEvent(new Event('change', {bubbles: true})); return '' })()`
)

func (s *linkScript) page(ctx context.Context, what string, raw json.RawMessage) (any, error) {
	e := s.e
	var a pageArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, errors.New("these arguments cannot be read")
	}
	e.usedBrowser()
	if what == "open" {
		if len(s.mine) >= 8 {
			return nil, errors.New("too many pages are open: close some")
		}
		if a.Width < 240 || a.Height < 200 || a.Width > 3840 || a.Height > 2160 {
			a.Width, a.Height = 1280, 800
		}
		page, err := e.host.OpenPage(browser.Info{Width: a.Width, Height: a.Height, Mobile: a.Mobile, Adaptive: false})
		if err != nil {
			return nil, err
		}
		if err := page.Run(ctx); err != nil {
			e.host.ClosePage(page.Snapshot().ID)
			return nil, err
		}
		id := page.Snapshot().ID
		if s.mine == nil {
			s.mine = map[string]*browser.Page{}
		}
		s.mine[id] = page
		e.links.mu.Lock()
		e.links.pages[id]++
		e.links.mu.Unlock()
		return map[string]string{"page": id}, nil
	}
	page := s.mine[a.Page]
	if page == nil {
		return nil, errors.New("that page is closed")
	}
	if a.Timeout <= 0 || a.Timeout > 600 {
		a.Timeout = 20
	}
	limit, cancel := context.WithTimeout(ctx, time.Duration(a.Timeout*float64(time.Second)))
	defer cancel()
	if what != "goto" && what != "close" && what != "info" {
		if err := s.reaches(page.Snapshot().URL); err != nil {
			return nil, err
		}
	}
	quoted := func(v any) string { data, _ := json.Marshal(v); return string(data) }
	js := func(script string, out any) error {
		raw, err := page.Evaluate(ctx, script)
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, out)
	}
	// wait polls a condition of the page.
	wait := func(condition, what string) error {
		for {
			var yes bool
			if err := js(condition, &yes); err == nil && yes {
				return nil
			}
			select {
			case <-limit.Done():
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return fmt.Errorf("%s: not after %g s (the page: %s)", what, a.Timeout, page.Snapshot().URL)
			case <-time.After(80 * time.Millisecond):
			}
		}
	}
	shown := func(selector string) error {
		return wait(shownJS+"(document.querySelector("+quoted(selector)+"))", "nothing shows for "+selector)
	}
	// act refuses a press that matters when the user did not let it.
	act := func(label string) error {
		if risk := riskOf(label); risk.kind != "" {
			if err := s.may("act"); err != nil {
				return fmt.Errorf("pressing %q: %s. %w", label, risk.warning, err)
			}
			if s.test {
				return fmt.Errorf("a test does not press %q", label)
			}
		}
		return nil
	}
	done := map[string]bool{"ok": true}
	switch what {
	case "goto":
		if err := s.reaches(a.URL); err != nil {
			return nil, err
		}
		if err := page.Navigate(ctx, "navigate", a.URL); err != nil {
			return nil, err
		}
		if err := s.reaches(page.Snapshot().URL); err != nil { // sent elsewhere
			page.Navigate(ctx, "navigate", "about:blank")
			return nil, err
		}
		if a.Wait == "idle" {
			limit, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			page.Idle(limit, 500*time.Millisecond)
		}
		return done, nil
	case "reload", "back":
		return done, page.Navigate(ctx, what, "")
	case "info":
		info := page.Snapshot()
		var live struct{ URL, Title string }
		if js("({url: location.href, title: document.title})", &live) == nil {
			info.URL, info.Title = live.URL, live.Title
		}
		return map[string]string{"url": info.URL, "title": info.Title}, nil
	case "close":
		s.drop(a.Page)
		return done, nil
	case "idle":
		if err := page.Idle(limit, 500*time.Millisecond); err != nil {
			return nil, fmt.Errorf("the page still makes requests after %g s", a.Timeout)
		}
		return done, nil
	case "wait":
		switch {
		case a.URL != "":
			return done, wait("location.href.includes("+quoted(a.URL)+")", "the address never held "+a.URL)
		case a.Text != "":
			return done, wait("document.body && document.body.innerText.includes("+quoted(a.Text)+")", "the page never said "+a.Text)
		case a.Selector != "" && a.Hidden:
			return done, wait("!"+shownJS+"(document.querySelector("+quoted(a.Selector)+"))", a.Selector+" still shows")
		case a.Selector != "":
			return done, shown(a.Selector)
		}
		return nil, errors.New("say what to wait for")
	case "click", "hover":
		x, y := a.X, a.Y
		label := ""
		if a.Selector != "" || a.Text != "" {
			find := "document.querySelector(" + quoted(a.Selector) + ")"
			name := a.Selector
			if a.Selector == "" {
				find, name = byText+"("+quoted(a.Text)+")", "“"+a.Text+"”"
			}
			if err := wait(shownJS+"("+find+")", "nothing shows for "+name); err != nil {
				return nil, err
			}
			var at struct {
				X, Y  float64
				Label string
			}
			if err := js("(e => { e.scrollIntoView({block: 'center', inline: 'center', behavior: 'instant'}); const r = e.getBoundingClientRect(); const b = e.closest('button,a,[role=button],input,label') || e; return {x: r.x + r.width / 2, y: r.y + r.height / 2, label: (b.innerText || b.value || b.getAttribute('aria-label') || '').slice(0, 200)} })("+find+")", &at); err != nil {
				return nil, err
			}
			x, y, label = at.X, at.Y, at.Label
		} else {
			js("(e => { const b = e && (e.closest('button,a,[role=button],input,label') || e); return b ? (b.innerText || b.value || b.getAttribute('aria-label') || '').slice(0, 200) : '' })(document.elementFromPoint("+quoted(x)+", "+quoted(y)+"))", &label)
		}
		if what == "hover" {
			return done, page.Interact(ctx, "move", "", "", x, y, 0, 0)
		}
		if err := act(label); err != nil {
			return nil, err
		}
		return done, page.Interact(ctx, "click", "", "", x, y, 0, 0)
	case "fill":
		if err := shown(a.Selector); err != nil {
			return nil, err
		}
		var problem string
		if err := js(fmt.Sprintf(fillAny, quoted(map[string]any{"selector": a.Selector, "value": a.Value})), &problem); err != nil {
			return nil, err
		}
		if problem != "" {
			return nil, errors.New(problem)
		}
		return done, nil
	case "fill_secret":
		entry, err := s.entry(a.Entry)
		if err != nil {
			return nil, err
		}
		if err := shown(a.Selector); err != nil {
			return nil, err
		}
		address := page.Snapshot().URL
		var live string
		if js("location.href", &live) == nil {
			address = live
		}
		host := site(address)
		if !strings.HasPrefix(address, "https://") && host != "localhost" && host != "127.0.0.1" {
			return nil, errors.New("this page is not https: nothing is typed there")
		}
		if entry.Kind == "signin" && !sameSite(host, entry.Site) && host != entry.Site {
			return nil, fmt.Errorf("this page is on %s, the entry is for %s", host, entry.Site)
		}
		values, err := entry.filled(e.now())
		if err != nil {
			return nil, err
		}
		value := values[a.Field]
		if value == "" {
			return nil, fmt.Errorf("the entry has no %q", a.Field)
		}
		if a.Field != "user" {
			s.given = append(s.given, value)
		}
		var problem string
		if err := js(fmt.Sprintf(fillAny, quoted(map[string]any{"selector": a.Selector, "value": value, "secret": a.Field == "password"})), &problem); err != nil {
			return nil, errors.New("the page did not take it")
		}
		if problem != "" {
			return nil, errors.New(problem)
		}
		return done, nil
	case "type":
		return done, page.Interact(ctx, "type", a.Selector, a.Text, 0, 0, 0, 0)
	case "press":
		return done, page.Interact(ctx, "key", "", a.Key, 0, 0, 0, 0)
	case "scroll":
		x, y := 0.0, 0.0
		if a.Selector == "" {
			info := page.Snapshot()
			x, y = float64(info.Width)/2, float64(info.Height)/2
		}
		return done, page.Interact(ctx, "scroll", a.Selector, "", x, y, a.DX, a.DY)
	case "select":
		var problem string
		if err := js("((s, v) => { const e = document.querySelector(s); if (!e || !e.options) return 'no list ' + s; const o = [...e.options].find(o => o.value === v || o.text.trim() === v); if (!o) return 'no choice ' + v; e.value = o.value; e.dispatchEvent(new Event('input', {bubbles: true})); e.dispatchEvent(new Event('change', {bubbles: true})); return '' })("+quoted(a.Selector)+", "+quoted(a.Value)+")", &problem); err != nil {
			return nil, err
		}
		if problem != "" {
			return nil, errors.New(problem)
		}
		return done, nil
	case "check":
		var problem string
		if err := js("((s, on) => { const e = document.querySelector(s); if (!e) return 'no box ' + s; if (e.checked !== on) e.click(); return '' })("+quoted(a.Selector)+", "+quoted(a.On)+")", &problem); err != nil {
			return nil, err
		}
		if problem != "" {
			return nil, errors.New(problem)
		}
		return done, nil
	case "eval":
		raw, err := page.Evaluate(ctx, a.Script)
		if err != nil {
			return nil, err
		}
		if len(raw) > 8<<20 {
			return nil, errors.New("that value is too large")
		}
		return raw, nil
	case "requests":
		list := page.Requests(a.Match, a.Type)
		if list == nil {
			list = []browser.Request{}
		}
		return list, nil
	case "response":
		for {
			list := page.Requests(a.Match, "")
			for _, r := range slices.Backward(list) {
				if r.Done && r.Failed == "" && r.Status != 0 {
					request, body, err := page.Response(ctx, r.ID, 8<<20)
					if err != nil {
						return nil, err
					}
					return map[string]any{"status": request.Status, "headers": request.Received, "body": body}, nil
				}
			}
			select {
			case <-limit.Done():
				return nil, fmt.Errorf("no answer came to a request whose address holds %q", a.Match)
			case <-time.After(80 * time.Millisecond):
			}
		}
	case "screenshot":
		return page.Capture(ctx, a.Full)
	}
	return nil, fmt.Errorf("unknown call page.%s", what)
}

// ---------------------------------------------------------------------------
// Loopy's browser: closed when nothing needs it.

func (e *Engine) usedBrowser() {
	e.mu.Lock()
	e.browsed = e.now()
	e.mu.Unlock()
}

// scripts is how many pages scripts work in.
func (e *Engine) scripts() int {
	e.links.mu.Lock()
	defer e.links.mu.Unlock()
	return len(e.links.pages)
}

// ownPage is the page of a tab, or the one Loopy works in: never a script's.
func (e *Engine) ownPage(id string) (*browser.Page, error) {
	of := func(page *browser.Page) bool {
		e.links.mu.Lock()
		defer e.links.mu.Unlock()
		return e.links.pages[page.Snapshot().ID] > 0
	}
	pages := e.host.Pages()
	if id != "" {
		for _, page := range pages {
			if page.Snapshot().ID == id {
				if of(page) {
					return nil, errors.New("an AutoLink works in that tab: open another")
				}
				return page, nil
			}
		}
		return nil, nil // the browser's own tools say it is gone
	}
	if page := e.host.Page(); page != nil && !of(page) {
		return page, nil
	}
	for _, page := range slices.Backward(pages) {
		if !of(page) {
			return page, nil
		}
	}
	return nil, nil
}

// withTab names the tab a call is for.
func withTab(raw json.RawMessage, id string) json.RawMessage {
	var args map[string]any
	if json.Unmarshal(raw, &args) != nil {
		return raw
	}
	args["tab_id"] = id
	out, err := json.Marshal(args)
	if err != nil {
		return raw
	}
	return out
}

// restBrowser closes the browser nobody uses.
func (e *Engine) restBrowser(now time.Time) {
	pages := e.host.Pages()
	if len(pages) == 0 || e.taken() || e.scripts() > 0 {
		return
	}
	e.mu.Lock()
	last, held := e.browsed, len(e.waits) > 0 // a card of the run that uses it
	e.mu.Unlock()
	for _, id := range e.waiting() { // "I'm done" in the browser
		if message, ok := e.message(id); ok && (message.Card.Kind == "needs_you" || message.Card.Kind == "approval" || message.Card.Kind == "signin") {
			held = true
		}
	}
	rest := browserRest
	for _, page := range pages {
		used, shown := page.Used()
		if shown {
			rest = browserShown
		}
		if used.After(last) {
			last = used
		}
	}
	if held && now.Sub(last) < time.Hour || now.Sub(last) < rest {
		return
	}
	e.host.ClosePages()
	e.mu.Lock()
	e.filled = ""
	e.mu.Unlock()
	e.tell(nil, nil, true)
}

// ---------------------------------------------------------------------------
// The inspector: what a page is made of and asks the network, so that Loopy
// can write a script for it.

var hiddenHeader = regexp.MustCompile(`(?i)authorization|cookie|token|secret|api-?key|session|csrf|xsrf|password`)
var hiddenValue = regexp.MustCompile(`(?i)("[\w-]*(?:password|passwd|mdp|motdepasse|secret|token|session|api_?key|otp|pin)[\w-]*"\s*:\s*")([^"]+)(")`)
var hiddenForm = regexp.MustCompile(`(?i)((?:^|[&?])[\w-]*(?:password|passwd|mdp|motdepasse|secret|token|session|api_?key|otp|pin)[\w-]*=)([^&\s]+)`)

func maskBody(text string) string {
	text = hiddenValue.ReplaceAllString(text, "$1••••$3")
	return hiddenForm.ReplaceAllString(text, "$1••••")
}

const elementJS = `((s, limit) => { const all = [...document.querySelectorAll(s)]; const path = e => { const out = []; for (; e && e.nodeType === 1 && out.length < 6; e = e.parentElement) { if (e.id && !/\d{3,}/.test(e.id)) { out.unshift('#' + CSS.escape(e.id)); break } const same = e.parentElement ? [...e.parentElement.children].filter(c => c.tagName === e.tagName) : [e]; out.unshift(e.tagName.toLowerCase() + (same.length > 1 ? ':nth-of-type(' + (same.indexOf(e) + 1) + ')' : '')) } return out.join(' > ') };
 return {count: all.length, elements: all.slice(0, limit).map(e => { const r = e.getBoundingClientRect(), c = getComputedStyle(e); return {path: path(e), tag: e.tagName.toLowerCase(), attributes: Object.fromEntries([...e.attributes].map(a => [a.name, e.type === 'password' && a.name === 'value' ? '••••' : a.value.slice(0, 300)])), text: (e.innerText || '').trim().slice(0, 400), value: e.type === 'password' ? (e.value ? '••••' : '') : e.value, box: {x: r.x, y: r.y, width: r.width, height: r.height}, style: {display: c.display, visibility: c.visibility, position: c.position, color: c.color, background: c.backgroundColor, font: c.fontSize}, listeners: typeof e.onclick === 'function', children: e.children.length, frame: e.tagName === 'IFRAME' ? e.src : undefined} })} })(%s, 12)`

// inspect is loopy_inspect.
func (e *Engine) inspect(ctx context.Context, args toolArgs) (string, error) {
	if _, err := e.waitBrowser(ctx); err != nil {
		return "", err
	}
	page, err := e.ownPage("")
	if err != nil {
		return "", err
	}
	if page == nil {
		return "", errors.New("open the page in the browser first (browser_tabs)")
	}
	e.usedBrowser()
	if e.holdsSecret(page) && !e.clearsSecret(ctx, page) && args.Action != "requests" {
		return "", errors.New("refused: a password is still in this page. Press its button if you have not")
	}
	quoted := func(v any) string { data, _ := json.Marshal(v); return string(data) }
	cut := func(text string, limit int) string {
		if len(text) > limit {
			return strings.ToValidUTF8(text[:limit], "") + fmt.Sprintf("\n… (%d characters in all: narrow the selector)", len(text))
		}
		return text
	}
	out := ""
	switch args.Action {
	case "html":
		selector := args.Selector
		if selector == "" {
			selector = "body"
		}
		raw, err := page.Evaluate(ctx, `(s => { const e = document.querySelector(s); if (!e) return null; const c = e.cloneNode(true); c.querySelectorAll('script,style,svg,noscript').forEach(x => x.replaceWith(document.createComment(x.tagName.toLowerCase()))); c.querySelectorAll('input[type=password]').forEach(x => x.removeAttribute('value')); return c.outerHTML })(`+quoted(selector)+`)`)
		if err != nil {
			return "", err
		}
		var html *string
		if json.Unmarshal(raw, &html); html == nil {
			return "", errors.New("nothing matches " + selector)
		}
		out = cut(*html, 24<<10)
	case "elements":
		if args.Selector == "" {
			return "", errors.New("give a CSS selector")
		}
		raw, err := page.Evaluate(ctx, fmt.Sprintf(elementJS, quoted(args.Selector)))
		if err != nil {
			return "", err
		}
		out = cut(string(raw), 16<<10)
	case "requests":
		list := page.Requests(args.Match, args.Kind)
		if len(list) == 0 {
			return "No such request since the page was opened.", nil
		}
		var b strings.Builder
		for _, r := range list[max(0, len(list)-80):] {
			state := fmt.Sprint(r.Status)
			if r.Failed != "" {
				state = r.Failed
			} else if !r.Done {
				state = "…"
			}
			fmt.Fprintf(&b, "%s  %s %s  %s  %s  %d bytes  %d ms\n", r.ID, r.Method, maskBody(cut(r.URL, 300)), state, r.Type+" "+r.Mime, r.Size, r.Took)
		}
		out = b.String()
	case "request":
		r, body, err := page.Response(ctx, args.ID, 1<<20)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s %s\n", r.Method, maskBody(r.URL))
		headers := func(of map[string]string) {
			for _, name := range sortedKeys(of) {
				value := of[name]
				if hiddenHeader.MatchString(name) {
					value = fmt.Sprintf("•••• (%d characters)", len(value))
				}
				fmt.Fprintf(&b, "  %s: %s\n", name, cut(value, 400))
			}
		}
		headers(r.Sent)
		if r.Body != "" {
			fmt.Fprintf(&b, "\n%s\n", cut(maskBody(r.Body), 8<<10))
		}
		fmt.Fprintf(&b, "\n→ %d %s %s\n", r.Status, r.Mime, r.Failed)
		headers(r.Received)
		if body != "" {
			fmt.Fprintf(&b, "\n%s", cut(maskBody(body), 16<<10))
		}
		out = b.String()
	default:
		return "", errors.New("unknown action")
	}
	return untrusted("web page", redact(out, e.secrets())), nil
}

// allowScript lets a script that acts on a page through, when the user says
// so for its site.
func (e *Engine) allowScript(ctx context.Context, page *browser.Page) error {
	if page == nil {
		return nil
	}
	host := site(page.Snapshot().URL)
	if e.rule("script", host) {
		return nil
	}
	answer, err := e.ask(ctx, loopy.Card{Kind: "approval", Title: "Run a script that acts on " + host, Subtitle: "In Loopy's browser", Warning: "It can press, send and change things there in your name", Actions: []string{"Allow once", "Always allow on " + host, "Deny"}})
	if err != nil {
		return err
	}
	switch {
	case answer.Answer == "Deny":
		return errors.New("the user said no: a script may only read this page. Click and type with browser_interact")
	case strings.HasPrefix(answer.Answer, "Always"):
		e.addRule("script", host)
	}
	return nil
}
