// SPDX-License-Identifier: MPL-2.0

//go:build !js

package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"1loop/internal/agent"
	"1loop/internal/browser"
	"1loop/internal/loopy"
)

// guardBrowser is the browser's tools, behind the guard: what a click does
// is read in the page before it is pressed, what is typed is checked, and
// what comes back is marked as someone else's words.
func (e *Engine) guardBrowser(tools agent.PanelToolHandler) agent.PanelToolHandler {
	if tools == nil {
		return nil
	}
	return func(ctx context.Context, name string, raw json.RawMessage) (agent.PanelToolResult, error) {
		var args agent.BrowserToolArgs
		json.Unmarshal(raw, &args)
		was, err := e.waitBrowser(ctx)
		if err != nil {
			return agent.PanelToolResult{}, err
		}
		if was && name == "browser_interact" {
			return agent.PanelToolResult{}, errors.New("the user just used the browser themselves: read the page again before acting in it")
		}
		page := e.host.Page()
		// What leaves by an address, a script or the keyboard.
		for _, out := range []string{args.Text, args.URL, args.Expression} {
			if _, found := sensitive(out, e.secrets()); len(found) > 0 {
				return agent.PanelToolResult{}, fmt.Errorf("refused: that holds %s, which never leaves. Sign in with loopy_sign_in; for the rest, the user does it themselves (a needs_you card)", strings.Join(found, " and "))
			}
		}
		switch name {
		case "browser_evaluate":
			if clickScript.MatchString(args.Expression) {
				return agent.PanelToolResult{}, errors.New("refused: a script may only read the page. Click and type with browser_interact")
			}
			if page != nil && e.holdsSecret(page) {
				return agent.PanelToolResult{}, errors.New("refused: a password is in this page. Press its button first")
			}
		case "browser_interact":
			if page != nil {
				if err := e.allowPress(ctx, page, args); err != nil {
					return agent.PanelToolResult{}, err
				}
			}
		}
		result, err := tools(ctx, name, raw)
		if was && err == nil {
			result.Text = "(The user just used the browser themselves.)\n" + result.Text
		}
		result.Text = redact(result.Text, e.secrets())
		if err == nil && (name == "browser_read" || name == "browser_evaluate") && result.Text != "" {
			result.Text = untrusted("web page", result.Text)
		}
		if name == "browser_navigate" {
			e.holdsSecret(e.host.Page()) // forgets a page that was left
		}
		return result, err
	}
}

// TakeBrowser gives the browser to the user, who does something in it
// themselves (a CAPTCHA, a code, a payment): Loopy's next action there waits
// until they give it back.
func (e *Engine) TakeBrowser(on bool) {
	e.mu.Lock()
	switch {
	case on && e.hands == nil:
		e.hands = make(chan struct{})
	case !on && e.hands != nil:
		close(e.hands)
		e.hands = nil
	default:
		e.mu.Unlock()
		return
	}
	e.mu.Unlock()
	if on {
		e.log("The user took the browser over.")
	} else {
		// A card that asked for just that is answered.
		for _, id := range e.waiting() {
			message, ok := e.message(id)
			if ok && message.Card.Kind == "needs_you" && slices.Contains(message.Card.Actions, imDone) {
				e.Answer(loopy.Answer{Message: id, Answer: imDone})
				break
			}
		}
	}
	e.tell(nil, nil, true)
}

const imDone = "I'm done"

func (e *Engine) taken() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.hands != nil
}

// waitBrowser waits for the user to be done with the browser; it says
// whether they had it.
func (e *Engine) waitBrowser(ctx context.Context) (bool, error) {
	e.mu.Lock()
	hands := e.hands
	e.mu.Unlock()
	if hands == nil {
		return false, nil
	}
	e.set("waiting", "You have the browser")
	select {
	case <-hands:
		return true, nil
	case <-ctx.Done():
		return true, ctx.Err()
	}
}

// holdsSecret says whether a secret 1loop typed may still be in the page.
func (e *Engine) holdsSecret(page *browser.Page) bool {
	if page == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.filled != "" && e.filled != page.Snapshot().URL {
		e.filled = ""
	}
	return e.filled != ""
}

// pressed reads what is about to be pressed: its words, and those of the
// form it sends.
const pressed = `(() => { const s = %s; let el = null;
 try { el = s.selector ? document.querySelector(s.selector) : document.elementFromPoint(s.x, s.y); } catch (e) {}
 if (!el) return null;
 const b = el.closest('button,a,[role=button],[role=menuitem],[role=link],input,label,summary') || el;
 const words = x => (x.innerText || x.value || x.getAttribute('aria-label') || x.title || x.alt || '').trim().slice(0, 200);
 const form = b.closest('form'); let enter = '';
 if (form && s.action !== 'click') { const submit = form.querySelector('button[type=submit],input[type=submit],button:not([type])'); enter = submit ? words(submit) : ''; }
 const image = b.querySelector && b.querySelector('img[alt],svg[aria-label],[aria-label]');
 return {label: words(b) || (image ? image.getAttribute('alt') || image.getAttribute('aria-label') || '' : ''), aria: (b.getAttribute('aria-label') || ''), enter, type: (b.type || ''), tag: b.tagName, search: !!(form && (form.getAttribute('role') === 'search' || /search|recherche|\bq\b/i.test(form.action + ' ' + form.className))), password: !!(form && form.querySelector('input[type=password]')), text: b.tagName === 'TEXTAREA' || b.isContentEditable || (b.tagName === 'INPUT' && !/^(button|submit|image|checkbox|radio)$/.test(b.type))};
})()`

type press struct {
	Label, Aria, Enter, Type, Tag string
	Search, Password, Text        bool
}

// allowPress lets a press through, or asks the user when it matters.
func (e *Engine) allowPress(ctx context.Context, page *browser.Page, args agent.BrowserToolArgs) error {
	action := args.Action
	typed := action == "type" && strings.ContainsAny(args.Text, "\n\r")
	enter := action == "key" || typed
	if action != "click" && !enter {
		return nil
	}
	where, _ := json.Marshal(map[string]any{"selector": args.Selector, "x": args.X, "y": args.Y, "action": action})
	var p *press
	if raw, err := page.Evaluate(ctx, fmt.Sprintf(pressed, where)); err == nil {
		json.Unmarshal(raw, &p)
	}
	if p == nil {
		return nil // nothing there: the click does nothing, or says so
	}
	var what risk
	label := p.Label
	if enter {
		low := strings.ToLower(args.Text)
		if typed {
			low = "enter"
		}
		if !strings.Contains(low, "enter") && !strings.Contains(low, "space") || p.Search || p.Password {
			return nil
		}
		label = p.Enter
		if strings.Contains(low, "ctrl") || strings.Contains(low, "meta") || strings.Contains(low, "cmd") {
			what = risk{"send", "This is sent in your name"} // Ctrl+Enter sends in most writing boxes
			if label == "" {
				label = "Ctrl+Enter"
			}
		} else if p.Tag == "TEXTAREA" && p.Enter == "" {
			return nil // a new line
		}
	} else if p.Text {
		return nil
	}
	if what.kind == "" {
		what = riskOf(label)
	}
	if what.kind == "" && p.Aria != label {
		what = riskOf(p.Aria)
	}
	if p.Password && (what.kind == "publish" || what.kind == "agree") { // signing in
		return nil
	}
	if what.kind == "" {
		return nil
	}
	host := site(page.Snapshot().URL)
	e.mu.Lock()
	once, ok := e.granted[what.kind]
	if ok {
		delete(e.granted, what.kind) // a card said yes to this one
	}
	e.mu.Unlock()
	if ok && e.now().Before(once) || e.rule("browser:"+what.kind, host) {
		return nil
	}
	actions := []string{"Allow once", "Always allow on " + host, "Deny"}
	if what.kind == "buy" || what.kind == "security" || what.kind == "delete" {
		actions = []string{"Allow once", "Deny"} // asked each time
	}
	answer, err := e.ask(ctx, loopy.Card{Kind: "approval", Title: "Press “" + firstWords(label, 12) + "”", Subtitle: host, Warning: what.warning, Actions: actions})
	if err != nil {
		return err
	}
	switch {
	case answer.Answer == "Deny":
		return errors.New("the user said no. Do not try again or another way before their next message")
	case strings.HasPrefix(answer.Answer, "Always"):
		e.addRule("browser:"+what.kind, host)
	}
	return nil
}

func (e *Engine) rule(kind, host string) bool {
	e.store.Lock()
	defer e.store.Unlock()
	return slices.ContainsFunc(e.store.Rules, func(rule loopy.Rule) bool { return rule.Kind == kind && rule.Site == host })
}

func (e *Engine) addRule(kind, host string) {
	e.store.Lock()
	e.store.Rules = append(e.store.Rules, loopy.Rule{ID: loopy.NewID(), Kind: kind, Site: host, Time: e.now()})
	e.store.Unlock()
	e.tell(nil, nil, true)
	e.save()
}

// RemoveRule ends an "always".
func (e *Engine) RemoveRule(id string) {
	e.store.Lock()
	e.store.Rules = slices.DeleteFunc(e.store.Rules, func(rule loopy.Rule) bool { return rule.ID == id })
	e.store.Unlock()
	e.tell(nil, nil, true)
	e.save()
}

// ---------------------------------------------------------------------------
// Signing in.

// fill puts a value in a page's field the way typing does, and says
// nothing back of it.
const fill = `(() => { const s = %s; const el = document.querySelector(s.selector); if (!el) return 'no field ' + s.selector;
 if (s.secret && el.type !== 'password') return 'that field is not a password field';
 el.focus(); const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set; set.call(el, s.value);
 el.dispatchEvent(new Event('input', {bubbles: true})); el.dispatchEvent(new Event('change', {bubbles: true})); return ''; })()`

func (e *Engine) signIn(ctx context.Context, args toolArgs) (string, error) {
	page := e.host.Page()
	if page == nil {
		return "", errors.New("open the site's sign-in page in the browser first")
	}
	if args.UserField == "" && args.PasswordField == "" {
		return "", errors.New("name the fields to fill")
	}
	address := page.Snapshot().URL
	host := site(address)
	if !strings.HasPrefix(address, "https://") && host != "localhost" && host != "127.0.0.1" {
		return "", errors.New("this page is not https: nothing is typed there")
	}
	values := map[string]string{}
	if e.vault != nil {
		entries := e.vault.forSite(host)
		if args.Entry != "" {
			entries = slices.DeleteFunc(entries, func(s secretEntry) bool { return !strings.EqualFold(s.Name, args.Entry) })
		}
		if len(entries) > 0 {
			entry := entries[0]
			if !e.rule("signin", host) {
				answer, err := e.ask(ctx, loopy.Card{Kind: "approval", Title: "Sign in to " + host, Subtitle: "with your vault's " + entry.Name, Body: args.Why, Warning: "Filled directly on " + host + ": Loopy does not see it",
					Actions: []string{"Allow once", "Always allow on " + host, "Deny"}})
				if err != nil {
					return "", err
				}
				if answer.Answer == "Deny" {
					return "", errors.New("the user said no. Do not try again before their next message")
				}
				if strings.HasPrefix(answer.Answer, "Always") {
					e.addRule("signin", host)
				}
			}
			values = entry.Values
		}
	}
	if len(values) == 0 {
		var fields []loopy.Field
		if args.UserField != "" {
			fields = append(fields, loopy.Field{Label: "user"})
		}
		if args.PasswordField != "" {
			fields = append(fields, loopy.Field{Label: "password"})
		}
		answer, err := e.ask(ctx, loopy.Card{Kind: "signin", Title: "Sign in to " + host, Body: args.Why, Subtitle: "Filled directly on " + host, Site: host, Fields: fields, Keep: e.vault != nil, Actions: []string{"Sign in", "Not now"}})
		if err != nil {
			return "", err
		}
		if answer.Answer != "Sign in" {
			return "", errors.New("the user does not want to sign in now")
		}
		values = answer.Values
		if answer.Keep && e.vault != nil && len(values) > 0 {
			if _, err := e.vault.put(loopy.VaultEntry{Kind: "signin", Site: host, Name: host, Hint: answer.Hint}, values); err != nil {
				e.add(loopy.Message{From: "loopy", Note: "The vault could not keep it: " + err.Error()})
			}
			e.tell(nil, nil, true)
		}
	}
	// Only on the page it was asked for.
	if now := site(page.Snapshot().URL); now != host {
		return "", fmt.Errorf("the page went to %s meanwhile: nothing was typed", now)
	}
	for _, f := range []struct {
		selector, name string
		secret         bool
	}{{args.UserField, "user", false}, {args.PasswordField, "password", true}} {
		if f.selector == "" || values[f.name] == "" {
			continue
		}
		what, _ := json.Marshal(map[string]any{"selector": f.selector, "value": values[f.name], "secret": f.secret})
		raw, err := page.Evaluate(ctx, fmt.Sprintf(fill, what))
		if err != nil {
			return "", errors.New("the page did not take it")
		}
		var problem string
		if json.Unmarshal(raw, &problem); problem != "" {
			return "", errors.New(problem)
		}
		if f.secret {
			e.mu.Lock()
			e.filled = page.Snapshot().URL
			e.mu.Unlock()
		}
	}
	e.log("Filled the sign-in of " + host + ".")
	return "Filled, without your seeing it. Press the page's button now.", nil
}

// Vault changes: the settings' page.

// SaveSecret keeps an entry of the vault.
func (e *Engine) SaveSecret(entry loopy.VaultEntry, values map[string]string) error {
	if e.vault == nil {
		return errors.New("the vault is not open")
	}
	if entry.Kind != "signin" && entry.Kind != "key" && entry.Kind != "contact" {
		return errors.New("unknown kind of entry")
	}
	if strings.TrimSpace(entry.Name) == "" || len(values) == 0 {
		return errors.New("give it a name and a value")
	}
	if entry.Kind == "signin" {
		entry.Site = site(entry.Site)
		if !strings.Contains(entry.Site, ".") && entry.Site != "localhost" {
			return errors.New("name the site, as example.com")
		}
	}
	if old, ok := e.vault.get(entry.ID); ok { // an empty field keeps its value
		for name, value := range old.Values {
			if values[name] == "" {
				values[name] = value
			}
		}
	}
	_, err := e.vault.put(entry, values)
	e.tell(nil, nil, true)
	return err
}

// RemoveSecret takes an entry out of the vault.
func (e *Engine) RemoveSecret(id string) error {
	if e.vault == nil {
		return errors.New("the vault is not open")
	}
	err := e.vault.remove(id)
	e.tell(nil, nil, true)
	return err
}
