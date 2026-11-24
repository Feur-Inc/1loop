// SPDX-License-Identifier: MPL-2.0

//go:build !js

package engine

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"1loop/internal/loopy"
	"1loop/internal/loopy/memory"
)

// Answer is the user pressing a card's button or choosing an option.
func (e *Engine) Answer(answer loopy.Answer) error {
	message, ok := e.message(answer.Message)
	if !ok || message.Card == nil {
		return errors.New("no such card")
	}
	card := message.Card
	if card.Kind == "summary" {
		return e.answerSummary(card, answer)
	}
	if !card.Waiting() {
		return errors.New("this card was already answered")
	}
	known := slices.Contains(card.Actions, answer.Answer) || slices.ContainsFunc(card.Options, func(o loopy.Option) bool { return o.Label == answer.Answer })
	if !known {
		return errors.New("that is not one of the card's answers")
	}
	if card.Kind == "signin" && answer.Answer == card.Actions[0] && strings.TrimSpace(strings.Join(slices.Collect(maps.Values(answer.Values)), "")) == "" {
		return errors.New("type them first")
	}
	e.mu.Lock()
	waiting := e.waits[answer.Message]
	delete(e.waits, answer.Message)
	e.mu.Unlock()
	if waiting != nil { // a run waits for it
		e.close(message.ID, answer.Answer, "")
		waiting <- answer
		e.log(fmt.Sprintf("The user answered %q to %q.", answer.Answer, card.Title))
		return nil
	}
	switch card.Kind {
	case "mail", "message", "event":
		return e.answerDraft(message, answer)
	case "delegate":
		return e.answerDelegate(message, answer)
	case "routine":
		return e.answerRoutine(message, answer)
	}
	if card.Kind == "needs_you" {
		e.TakeBrowser(false) // done with it, or dropped
	}
	note := fmt.Sprintf("[The user answered your card %q: %s]", card.Title, answer.Answer)
	shown := answer.Answer
	if card.Ask != "" && answer.Answer == card.Actions[0] {
		given := strings.TrimSpace(answer.Text)
		if given == "" {

			return errors.New("type what is asked first")
		}
		if _, found := sensitive(given, e.secrets()); len(found) > 0 {
			return fmt.Errorf("that is %s: Loopy must not have it", strings.Join(found, " and "))
		}
		note = fmt.Sprintf("[The user answered your card %q: %s]", card.Title, given)
	}
	e.close(message.ID, shown, "")
	e.log(fmt.Sprintf("The user answered %q to %q.", shown, card.Title))
	e.enqueue(queued{text: note}, false)
	return nil
}

// close marks a card answered.
func (e *Engine) close(id, answer, result string) {
	e.update(id, func(m *loopy.Message) { m.Card.Answer, m.Card.Result = answer, result })
	e.rest(time.Second)
}

func (e *Engine) answerSummary(card *loopy.Card, answer loopy.Answer) error {
	for _, option := range card.Options {
		if option.Label == answer.Answer && option.Action != "" {
			e.enqueue(queued{text: fmt.Sprintf("[In your summary the user pressed %q on %q %s]", option.Action, option.Label, option.Detail)}, false)
			return nil
		}
	}
	return errors.New("that is not one of the summary's lines")
}

// answerDraft sends or drops a draft, as the user left it.
func (e *Engine) answerDraft(message loopy.Message, answer loopy.Answer) error {
	card := message.Card
	if answer.Answer == "Discard" {
		e.close(message.ID, "Discarded", "")
		e.log(fmt.Sprintf("The user discarded the draft %q.", card.Title))
		e.enqueue(queued{text: fmt.Sprintf("[The user discarded your draft %q. Do not propose it again; say nothing unless something else is needed.]", card.Title)}, false)
		return nil
	}
	fields, body := card.Fields, card.Body
	if answer.Fields != nil {
		fields = answer.Fields
	}
	if answer.Body != "" {
		body = answer.Body
	}
	// What must never leave is taken out, and shown again before it goes.
	masked, found := sensitive(body, e.secrets())
	clean := slices.Clone(fields)
	for i := range clean {
		value, more := sensitive(clean[i].Value, e.secrets())
		clean[i].Value = value
		found = append(found, more...)
	}
	if len(found) > 0 {
		e.update(message.ID, func(m *loopy.Message) {
			m.Card.Fields, m.Card.Body = clean, masked
			m.Card.Warning = "I took out " + strings.Join(found, " and ") + " (••••): this never leaves. Check the text, then send it."
		})
		return errors.New("It held " + strings.Join(found, " and ") + ", taken out: check it and send again.")
	}
	before := card.Body
	e.update(message.ID, func(m *loopy.Message) { m.Card.Fields, m.Card.Body, m.Card.Warning = fields, body, "" })
	to := ""
	for _, field := range fields {
		if strings.EqualFold(field.Label, "To") {
			to = field.Value
		}
	}
	// The text the user sent is how they write.
	keep := func() {
		record := memory.Record{Event: strings.TrimSpace(card.Title + " " + to), Draft: before, Final: body}
		e.memory.Add(record)
	}
	ctx, cancel := context.WithTimeout(e.ctx, 60*time.Second)
	defer cancel()
	switch card.Kind {
	case "mail":
		if account, ok := e.account("mail"); ok {
			if err := e.sendMail(ctx, account, fields, body); err != nil {
				return fmt.Errorf("The mail was not sent: %w", err)
			}
			e.close(message.ID, "Sent", "Sent to "+to+" from "+account.Address)
			keep()
			e.log(fmt.Sprintf("Sent a mail to %s: %s.", to, card.Title))
			e.enqueue(queued{text: fmt.Sprintf("[1loop sent the mail %q to %s, as the user left it. React or answer in a word, or go on with what it was for.]", card.Title, to)}, false)
			return nil
		}
	case "event":
		event, err := parseEvent(fields, body)
		if err != nil {
			return err
		}
		if account, ok := e.account("calendar"); ok {
			if err := e.addEvent(ctx, account, event); err != nil {
				return fmt.Errorf("It was not added: %w", err)
			}
			e.close(message.ID, "Added", "In the calendar of "+account.Address)
			e.log(fmt.Sprintf("Added %q to the calendar.", event.what))
			e.enqueue(queued{text: fmt.Sprintf("[1loop added %q to the user's calendar.]", event.what)}, false)
			return nil
		}
		// No calendar connected: a file any calendar opens.
		name := e.folder.fresh("/work", "event.ics")
		if err := e.folder.write(name, []byte(event.ics()), false); err != nil {
			return err
		}
		e.close(message.ID, "Saved", "Open "+name+" to add it: no calendar is connected.")
		return nil
	}
	// No account: it goes through the browser, where this one press is allowed.
	e.mu.Lock()
	e.granted["send"] = e.now().Add(15 * time.Minute)
	e.mu.Unlock()
	e.close(message.ID, "Send", "")
	keep()
	var b strings.Builder
	fmt.Fprintf(&b, "[The user wants this %s sent, exactly as it is here. No account is connected for it: do it in the browser, on the service they use.]\n", card.Kind)
	for _, field := range fields {
		fmt.Fprintf(&b, "%s: %s\n", field.Label, field.Value)
	}
	b.WriteString(body)
	e.enqueue(queued{text: b.String()}, false)
	return nil
}

// ---------------------------------------------------------------------------
// Coding tasks.

func (e *Engine) proposeTask(r *run, project, title, brief string) (string, error) {
	found := ""
	for _, p := range e.host.Projects() {
		if p == project || found == "" && strings.EqualFold(filepath.Base(p), filepath.Base(project)) {
			found = p
		}
	}
	if found == "" {
		return "", fmt.Errorf("%q is not one of the user's projects", project)
	}
	if strings.TrimSpace(title) == "" || strings.TrimSpace(brief) == "" {
		return "", errors.New("give a title and a brief")
	}
	task := loopy.Task{ID: loopy.NewID(), Title: title, Project: found, Brief: brief, Status: "proposed", Time: e.now()}
	e.store.Lock()
	e.store.Tasks = append(e.store.Tasks, task)
	e.store.Unlock()
	r.said = true
	message := e.add(loopy.Message{From: "loopy", Card: &loopy.Card{Kind: "delegate", Title: title, Subtitle: "project " + filepath.Base(found),
		Body: brief, Editable: true, Actions: []string{"Start", "Cancel"}, Task: task.ID}})
	e.notify("A coding task to start", title, &message, false)
	return "Proposed as task " + task.ID + ". The user starts it from the card: end your turn now.", nil
}

func (e *Engine) answerDelegate(message loopy.Message, answer loopy.Answer) error {
	id := message.Card.Task
	if answer.Answer == "Cancel" {
		e.changeTask(id, func(t *loopy.Task) { t.Status = "cancelled" })
		e.close(message.ID, "Cancelled", "")
		return nil
	}
	if strings.TrimSpace(answer.Body) != "" {
		e.changeTask(id, func(t *loopy.Task) { t.Brief = answer.Body })
		e.update(message.ID, func(m *loopy.Message) { m.Card.Body = answer.Body })
	}
	task, ok := e.task(id)
	if !ok || task.Status != "proposed" {
		return errors.New("this task was already started")
	}
	chat, err := e.host.StartChat(task.Project, task.Title, task.Brief)
	if err != nil {
		return err
	}
	e.changeTask(id, func(t *loopy.Task) {
		t.Chat, t.Status = chat, "running"
		t.Steps = append(t.Steps, loopy.Step{Time: e.now(), Text: "Started in " + filepath.Base(t.Project)})
	})
	e.close(message.ID, "Started", "")
	e.log(fmt.Sprintf("Handed %q to a coding agent in %s.", task.Title, filepath.Base(task.Project)))
	return nil
}

func (e *Engine) task(id string) (loopy.Task, bool) {
	e.store.Lock()
	defer e.store.Unlock()
	for _, task := range e.store.Tasks {
		if task.ID == id {
			task.Steps, task.PRs = slices.Clone(task.Steps), slices.Clone(task.PRs)
			return task, true
		}
	}
	return loopy.Task{}, false
}

func (e *Engine) changeTask(id string, change func(*loopy.Task)) {
	e.store.Lock()
	for i := range e.store.Tasks {
		if e.store.Tasks[i].ID == id {
			change(&e.store.Tasks[i])
		}
	}
	e.store.Unlock()
	e.tell(nil, nil, true)
	e.save()
}

// taskOf is the task a chat works on.
func (e *Engine) taskOf(chat string) (loopy.Task, bool) {
	e.store.Lock()
	defer e.store.Unlock()
	for _, task := range e.store.Tasks {
		if task.Chat == chat && chat != "" && (task.Status == "running" || task.Status == "waiting") {
			return task, true
		}
	}
	return loopy.Task{}, false
}

var prPattern = regexp.MustCompile(`https://github\.com/([\w.-]+/[\w.-]+)/pull/(\d+)`)

// ChatStep is something the chat of a task did.
func (e *Engine) ChatStep(chat, step string, waits bool) {
	task, ok := e.taskOf(chat)
	if !ok {
		return
	}
	step = firstWords(step, 20)
	e.changeTask(task.ID, func(t *loopy.Task) {
		t.Status = map[bool]string{false: "running", true: "waiting"}[waits]
		if n := len(t.Steps); step != "" && (n == 0 || t.Steps[n-1].Text != step) {
			t.Steps = append(t.Steps, loopy.Step{Time: e.now(), Text: step})
			if len(t.Steps) > 60 {
				t.Steps = append(t.Steps[:1:1], t.Steps[len(t.Steps)-59:]...)
			}
		}
	})
	if waits && task.Status != "waiting" {
		e.notify("The coding agent needs you", task.Title+": "+step, nil, false)
	}
}

// ChatEnded is the end of a task's chat: how ("done", "stopped" or else a
// failure) and its last words.
func (e *Engine) ChatEnded(chat, how, result string) {
	task, ok := e.taskOf(chat)
	if !ok {
		return
	}
	status := map[string]string{"done": "done", "stopped": "cancelled"}[how]
	if status == "" {
		status = "failed"
	}
	var prs []string
	for _, m := range prPattern.FindAllStringSubmatch(result, -1) {
		if pr := m[1] + "#" + m[2]; !slices.Contains(prs, pr) {
			prs = append(prs, pr)
		}
	}
	e.changeTask(task.ID, func(t *loopy.Task) {
		t.Status, t.Result, t.PRs = status, result, prs
		t.Steps = append(t.Steps, loopy.Step{Time: e.now(), Text: map[string]string{"done": "Finished", "cancelled": "Stopped", "failed": "Ended with an error"}[status]})
	})
	e.log(fmt.Sprintf("The coding task %q ended: %s.", task.Title, status))
	if status == "cancelled" {
		return
	}
	pr := ""
	if len(prs) > 0 {
		pr = " Its pull request: " + strings.Join(prs, ", ") + "."
	}
	e.enqueue(queued{why: "a coding task ended", text: fmt.Sprintf("[The coding task %q %s.%s What the coding agent said:\n%s\nTell the user in a message or two what came of it, with the pull request as owner/name#N.]",
		task.Title, map[string]string{"done": "is finished", "failed": "ended with an error"}[status], pr, untrusted("the coding agent's answer", firstWords(result, 400)))}, false)
}

// StopTask stops a task's chat.
func (e *Engine) StopTask(id string) error {
	task, ok := e.task(id)
	if !ok || task.Chat == "" {
		return errors.New("no such task")
	}
	e.host.StopChat(task.Chat)
	return nil
}
