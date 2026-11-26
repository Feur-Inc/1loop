// SPDX-License-Identifier: MPL-2.0

//go:build !js

package engine

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"1loop/internal/loopy"
)

func sortedKeys(of map[string]string) []string { return slices.Sorted(maps.Keys(of)) }

// linksTool is loopy_autolinks.
func (e *Engine) linksTool(args toolArgs) (string, error) {
	switch args.Action {
	case "help":
		return linkHelp, nil
	case "list":
		links := e.viewLinks()
		if len(links) == 0 {
			return "You have no AutoLink yet.", nil
		}
		var b strings.Builder
		for _, link := range links {
			b.WriteString(describeLink(link))
		}
		return b.String(), nil
	case "read":
		link, ok := e.link(args.ID)
		if !ok {
			return "", errors.New("no such AutoLink: list them")
		}
		var b strings.Builder
		b.WriteString(describeLink(link))
		for _, run := range link.Runs[max(0, len(link.Runs)-8):] {
			fmt.Fprintf(&b, "  %s, by %s, %d ms: %s\n", run.Time.Local().Format("2 Jan 15:04"), run.Why, run.Took, strings.TrimSpace(run.Error+" "+untrusted("output", run.Outcome)))
		}
		code := e.linkCode(link.ID)
		b.WriteString("\n```python\n" + code.Code + "\n```\n")
		if code.Next != "" {
			b.WriteString("The change that waits for the user:\n```python\n" + code.Next + "\n```\n")
		}
		return redact(b.String(), e.secrets()), nil
	}
	return "", errors.New("unknown action")
}

func describeLink(link loopy.AutoLink) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s  %q: %s\n", link.ID, link.Name, link.About)
	state := []string{}
	switch {
	case !link.Allowed:
		state = append(state, "not allowed yet")
	case link.Ask:
		state = append(state, "the user confirms each run")
	default:
		state = append(state, "runs by itself")
	}
	if link.When != "" {
		on := "off"
		if link.On {
			on = "on"
		}
		state = append(state, loopy.Describe(link.When)+" ("+on+")")
	}
	if link.Waits != "" {
		state = append(state, "waits for "+link.Waits)
	}
	if link.Running {
		state = append(state, "running")
	}
	if link.Failed > 0 {
		state = append(state, fmt.Sprintf("failed %d times in a row", link.Failed))
	}
	fmt.Fprintf(&b, "  %s", strings.Join(state, "; "))
	for _, part := range []struct {
		name string
		list []string
	}{{"sites", link.Sites}, {"vault", link.Secrets}, {"rights", link.Rights}, {"libraries", link.Libraries}} {
		if len(part.list) > 0 {
			fmt.Fprintf(&b, "; %s: %s", part.name, strings.Join(part.list, ", "))
		}
	}
	if link.Input != "" {
		b.WriteString("\n  input: " + link.Input)
	}
	if link.Do != "" {
		b.WriteString("\n  when it wakes you: " + link.Do)
	}
	return b.String() + "\n"
}

// notifyTool is loopy_notify.
func (e *Engine) notifyTool(r *run, args toolArgs) (string, error) {
	if strings.TrimSpace(args.Title+args.Text) == "" {
		return "", errors.New("give its words")
	}
	if _, found := sensitive(args.Title+" "+args.Text, e.secrets()); len(found) > 0 {
		return "", fmt.Errorf("refused: that holds %s", strings.Join(found, " and "))
	}
	notice := loopy.Notice{Title: args.Title, Text: args.Text, Level: args.Level}
	if notice.Level == "normal" {
		notice.Level = ""
	}
	if args.Picture != "" {
		name, err := clean(args.Picture)
		if err != nil {
			return "", err
		}
		if _, ok := e.picture(name); !ok {
			return "", errors.New("that is not a picture of your folder (PNG, JPEG, WebP or GIF, 8 MB at most)")
		}
		notice.Picture = "/" + name
	}
	if len(args.Buttons) > 3 {
		return "", errors.New("three buttons at most")
	}
	for _, button := range args.Buttons {
		if strings.TrimSpace(button) == "" || len([]rune(button)) > 30 {
			return "", errors.New("a button is a word or two")
		}
	}
	var card *loopy.Card
	if len(args.Buttons) > 0 {
		card = &loopy.Card{Kind: "needs_you", Title: args.Title, Body: args.Text, Actions: args.Buttons}
	}
	message := loopy.Message{From: "loopy", Text: strings.TrimSpace(args.Text), Card: card}
	if card != nil {
		message.Text = ""
	} else if args.Title != "" {
		message.Text = strings.TrimSpace(args.Title + "\n" + args.Text)
	}
	if notice.Picture != "" {
		message.Files = []string{notice.Picture}
	}
	message = e.add(message)
	if card != nil {
		notice.Card = &message
	}
	r.said = true
	night := notice.Level != "urgent" && e.night(e.now())
	e.notice(notice)
	if night {
		return "It is the user's night: they find it in the conversation, without a notification.", nil
	}
	return "Sent, and in the conversation: do not repeat it.", nil
}

// chatsTool is loopy_chats.
func (e *Engine) chatsTool(ctx context.Context, r *run, args toolArgs) (string, error) {
	chats := e.host.Chats()
	switch args.Action {
	case "list":
		var b strings.Builder
		words := strings.Fields(plain(args.Words))
		count := 0
		for _, chat := range chats {
			if slices.ContainsFunc(words, func(w string) bool { return !strings.Contains(plain(chat.Title+" "+chat.Project), w) }) {
				continue
			}
			if count++; count > 100 {
				b.WriteString("(more: give words)\n")
				break
			}
			fmt.Fprintf(&b, "%s  %q", chat.ID, chat.Title)
			if chat.Project != "" {
				b.WriteString("  in " + chat.Project)
			}
			if chat.Running {
				b.WriteString("  (working)")
			}
			b.WriteString("\n")
		}
		if count == 0 {
			return "None.", nil
		}
		return b.String(), nil
	case "delete":
		if r.why != "" {
			return "", errors.New("only when the user asks, in their message")
		}
		var ids, titles []string
		empty := false
		for _, id := range args.IDs {
			if id == "loopy" {
				empty = true
				continue
			}
			at := slices.IndexFunc(chats, func(c loopy.Chat) bool { return c.ID == id })
			if at < 0 {
				return "", fmt.Errorf("no chat has the id %q: list them", id)
			}
			ids, titles = append(ids, id), append(titles, chats[at].Title)
		}
		if empty {
			titles = append(titles, "This conversation with Loopy (the memory stays)")
		}
		if len(titles) == 0 {
			return "", errors.New("give the ids")
		}
		title := "Delete " + titles[0]
		if len(titles) > 1 {
			title = fmt.Sprintf("Delete %d chats", len(titles))
		}
		answer, err := e.ask(ctx, loopy.Card{Kind: "chats", Title: title, Body: strings.Join(titles, "\n"), Warning: "This can't be undone", Chats: ids, Actions: []string{"Delete", "Keep"}})
		if err != nil {
			return "", err
		}
		if answer.Answer != "Delete" {
			return "The user keeps them.", nil
		}
		var gone []string
		for i, id := range ids {
			if err := e.host.DeleteChat(id); err != nil {
				return "", fmt.Errorf("deleted %d, then %q: %w", len(gone), titles[i], err)
			}
			gone = append(gone, titles[i])
		}
		e.log("Deleted the chats: " + strings.Join(titles, ", ") + ".")
		if empty {
			go e.Clear() // ends this run
		}
		return fmt.Sprintf("Deleted %d.", len(titles)), nil
	}
	return "", errors.New("unknown action")
}

const linkHelp = `An AutoLink is a Python 3 script you keep, so that a job takes one tool call, or none, instead of driving the browser again. It runs on this machine, in its own process: it writes in its folder, reaches the sites you list and nothing else, and starts no program.

How to make one:
1. Is there an API or a library for it (an official API, a PyPI package)? Use it: faster and steadier than pages. Only built packages (wheels) install.
2. Else look at what the site's own pages do: do it once in the browser by hand, then loopy_inspect requests (kind xhr) and request <id>: often there is a JSON address the script can ask itself, or read in the page with page.response("…").
3. Else drive the pages: loopy_inspect elements / html to find steady selectors (an id, a name, a data- attribute, a role; not a generated class).
4. loopy_autolink_save with the fewest sites, vault entries and rights; the user allows it; loopy_autolink_run with test first; fix; then use it.
A value that changes is an input, not text in the script. Never put a password or a key in the script.

import autolink
autolink.input                     the JSON it was run with, or None
autolink.output(value)             gives back text or JSON (print() also comes back)
autolink.file("a.png")             gives back a file made in autolink.out; you see pictures
autolink.data                      its folder, kept; autolink.state["x"] = 1 is kept too
autolink.secret("Name")            a vault entry: .user .password .totp (the code now) .key; you never see it
autolink.new("key", items, id=f)   the items not seen before (nothing at the first run)
autolink.wake("what happened", key="pr-12")   wakes you with it (once a key); you then do what "do" says
autolink.notify(title, text, level="normal")  tells the user without waking you
autolink.agent(project, title, brief, key="pr-12")  starts a coding agent in a project (right "agent")
autolink.github("/repos/o/n/pulls", method="GET", body=None)  → .status .json() .headers (rights "github", "github_write")
autolink.test                      true in a test

page = autolink.browser.open("https://…", wait="idle")   your browser, kept signed in between runs
page.goto(url) .click("css") .click(text="Next") .fill("css", "text") .select("css", "v") .check("css") .press("Enter") .type("…") .scroll(800) .hover("css")
page.fill_secret("input[type=password]", "Name", field="password")   on the entry's own site
page.wait("css") .wait(1.5) .wait(text="Welcome") .wait(url="/home") .wait("css", hidden=True) .wait_idle()
page.text("css") .texts("css") .attr("css", "href") .table("table") .html("css") .exists("css") .eval("js") .url .title
page.requests("api/") → list of dicts; page.response("api/grades").json() waits for the page's own request and reads it
page.screenshot("a.png")           a picture, given back

Sign-in pattern: open the page; if the sign-in field exists, fill the user with fill_secret(…, field="user"), the password with fill_secret, click, wait for what shows only when signed in; else go on (the browser stays signed in).
Watching: when="every 5m"; keep what was seen with autolink.new; call autolink.wake with all you need to act (who, what, the address, the text), and say in "do" what the user asked for. GitHub mentions: autolink.github("/search/issues?q=…") or "/repos/o/n/issues/comments?sort=updated&direction=desc&per_page=50", keep those that hold "@login" (autolink.github("/user").json()["login"]). A pull request accepted: "/repos/o/n/pulls/12/reviews" has an APPROVED; then autolink.agent(…, key="merge-12").
Python code cannot be confined to reading: ask for the right "act" when the script changes something on a site.
`
