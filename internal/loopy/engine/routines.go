// SPDX-License-Identifier: MPL-2.0

//go:build !js

package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"1loop/internal/agent"
	"1loop/internal/loopy"
)

// ---------------------------------------------------------------------------
// Routines.

func (e *Engine) routine(id string) (loopy.Routine, bool) {
	e.store.Lock()
	defer e.store.Unlock()
	for _, routine := range e.store.Routines {
		if routine.ID == id {
			routine.Runs = slices.Clone(routine.Runs)
			return routine, true
		}
	}
	return loopy.Routine{}, false
}

func (e *Engine) changeRoutine(id string, change func(*loopy.Routine)) bool {
	e.store.Lock()
	found := false
	for i := range e.store.Routines {
		if e.store.Routines[i].ID == id {
			change(&e.store.Routines[i])
			found = true
		}
	}
	e.store.Unlock()
	e.tell(nil, nil, true)
	e.save()
	return found
}

var githubFilter = regexp.MustCompile(`^([\w.-]+/[\w.-]+) (issue|pr|ci)$`)

func checkRoutine(routine *loopy.Routine) error {
	routine.Title, routine.Task = strings.TrimSpace(routine.Title), strings.TrimSpace(routine.Task)
	routine.When, routine.Filter = strings.TrimSpace(routine.When), strings.TrimSpace(routine.Filter)
	if routine.Title == "" || routine.Task == "" {
		return errors.New("a routine has a title and a task")
	}
	switch routine.Trigger {
	case "":
		return loopy.ValidSchedule(routine.When)
	case "github":
		if !githubFilter.MatchString(routine.Filter) {
			return errors.New("the filter is \"owner/name issue\", \"owner/name pr\" or \"owner/name ci\"")
		}
	case "webhook":
		if routine.Token == "" {
			routine.Token = loopy.NewID() + loopy.NewID() + loopy.NewID()
		}
	case "mail":
	default:
		return errors.New("the trigger is github, mail or webhook")
	}
	routine.When = ""
	return nil
}

func (e *Engine) proposeRoutine(r *run, args toolArgs) (string, error) {
	routine := loopy.Routine{ID: args.ID, Title: args.Title, When: args.When, Task: args.Task, Trigger: args.Trigger, Filter: args.Filter, Watch: args.Watch}
	old, changes := e.routine(args.ID)
	if args.ID != "" && !changes {
		return "", errors.New("no routine has that id")
	}
	if err := checkRoutine(&routine); err != nil {
		return "", err
	}
	// A change is proposed too: it runs as it was until the user says yes.
	routine.ID = loopy.NewID()
	e.store.Lock()
	e.store.Routines = append(e.store.Routines, routine)
	e.store.Unlock()
	card := loopy.Card{Kind: "routine", Title: routine.Title, Subtitle: "Routine", Fields: []loopy.Field{{Label: "When", Value: describeRoutine(routine)}, {Label: "Does", Value: routine.Task},
		{Label: "Asks you", Value: "before anything is sent, bought, deleted or published"}}, Actions: []string{"Turn on", "Test now", "Discard"}, Routine: routine.ID}
	if changes {
		card.Subtitle = "Change of a routine"
		card.Fields = append(card.Fields, loopy.Field{Label: "Replaces", Value: old.Title})
		card.Task = old.ID
	}
	r.said = true
	message := e.add(loopy.Message{From: "loopy", Card: &card})
	e.notify("A routine to turn on", routine.Title, &message, false)
	return "Proposed, off until the user turns it on: end your turn now.", nil
}

func (e *Engine) answerRoutine(message loopy.Message, answer loopy.Answer) error {
	id := message.Card.Routine
	switch answer.Answer {
	case "Turn on":
		if message.Card.Task != "" {
			e.removeRoutine(message.Card.Task)
		}
		if err := e.SetRoutine(id, true); err != nil {
			return err
		}
		e.close(message.ID, "On", "")
	case "Test now":
		routine, ok := e.routine(id)
		if !ok {
			return errors.New("this routine is gone")
		}
		e.start(routine, "test", "") // the card stays: the user may still turn it on
	case "Discard":
		e.removeRoutine(id)
		e.close(message.ID, "Discarded", "")
	}
	return nil
}

// SetRoutine turns a routine on or off.
func (e *Engine) SetRoutine(id string, on bool) error {
	routine, ok := e.routine(id)
	if !ok {
		return errors.New("no such routine")
	}
	if on && routine.Trigger == "github" && e.host.GitHub(e.ctx) == "" {
		return errors.New("connect GitHub first (Settings → Tools & extensions)")
	}
	if mail, _ := e.accounts(); on && routine.Trigger == "mail" && !mail {
		return errors.New("connect a mail account first (Settings → Loopy → Accounts)")
	}
	e.changeRoutine(id, func(r *loopy.Routine) {
		r.On = on
		if on {
			r.LastRun, r.Seen = e.now(), "" // from now on
		}
	})
	return nil
}

// EditRoutine changes a routine from its page.
func (e *Engine) EditRoutine(routine loopy.Routine) error {
	old, ok := e.routine(routine.ID)
	if !ok {
		return errors.New("no such routine")
	}
	routine.Token = old.Token
	if err := checkRoutine(&routine); err != nil {
		return err
	}
	e.changeRoutine(routine.ID, func(r *loopy.Routine) {
		if r.When != routine.When || r.Trigger != routine.Trigger || r.Filter != routine.Filter {
			r.LastRun, r.Seen = e.now(), ""
		}
		r.Title, r.Task, r.When, r.Trigger, r.Filter, r.Watch, r.Token = routine.Title, routine.Task, routine.When, routine.Trigger, routine.Filter, routine.Watch, routine.Token
	})
	return nil
}

// RunRoutine runs it now.
func (e *Engine) RunRoutine(id string) error {
	routine, ok := e.routine(id)
	if !ok {
		return errors.New("no such routine")
	}
	e.start(routine, "test", "")
	return nil
}

// RemoveRoutine deletes it.
func (e *Engine) RemoveRoutine(id string) {
	e.removeRoutine(id)
	e.store.Lock()
	var cards []string
	for _, m := range e.store.Messages {
		if m.Card.Waiting() && m.Card.Kind == "routine" && m.Card.Routine == id {
			cards = append(cards, m.ID)
		}
	}
	e.store.Unlock()
	for _, card := range cards {
		e.close(card, "Discarded", "")
	}
}

func (e *Engine) removeRoutine(id string) {
	e.store.Lock()
	e.store.Routines = slices.DeleteFunc(e.store.Routines, func(r loopy.Routine) bool { return r.ID == id })
	e.store.Unlock()
	e.tell(nil, nil, true)
	e.save()
}

// start queues a run of a routine; what is what happened.
func (e *Engine) start(routine loopy.Routine, why, what string) {
	e.mu.Lock()
	waiting := slices.ContainsFunc(e.queue, func(q queued) bool { return q.routine == routine.ID && what == "" })
	e.mu.Unlock()
	if waiting {
		return // it waits already, and would do the same
	}
	e.changeRoutine(routine.ID, func(r *loopy.Routine) {
		r.LastRun = e.now()
		r.Runs = append(r.Runs, loopy.Run{Time: e.now(), Why: why})
		if len(r.Runs) > 50 {
			r.Runs = r.Runs[len(r.Runs)-50:]
		}
		if strings.HasPrefix(r.When, "once ") && why == "schedule" {
			r.On = false
		}
	})
	e.add(loopy.Message{From: "loopy", Note: "Routine: " + routine.Title})
	text := fmt.Sprintf("[Your routine %q, %s] %s", routine.Title, describeRoutine(routine), routine.Task)
	if routine.Watch {
		text += "\n(A watch: speak only if it breaks or changed; loopy_notify if it cannot wait.)"
	}
	if what != "" {
		text += "\nWhat happened:\n" + what
	}
	e.enqueue(queued{text: text, why: "routine " + routine.Title, routine: routine.ID}, false)
}

// Hook runs the routine of a webhook.
func (e *Engine) Hook(token string, body []byte) error {
	e.store.Lock()
	var found loopy.Routine
	for _, routine := range e.store.Routines {
		if routine.Trigger == "webhook" && routine.Token != "" && routine.Token == token && routine.On {
			found = routine
		}
	}
	e.store.Unlock()
	if found.ID == "" {
		return errors.New("no such webhook")
	}
	if e.now().Sub(found.LastRun) < 10*time.Second {
		return errors.New("too often")
	}
	if len(body) > 8000 {
		body = body[:8000]
	}
	e.start(found, "webhook", untrusted("webhook", strings.ToValidUTF8(string(body), "")))
	return nil
}

// ---------------------------------------------------------------------------
// The clock.

func (e *Engine) clockLoop() {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	polled := time.Time{}
	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
		}
		e.tick(e.now())
		if time.Since(polled) >= 2*time.Minute {
			polled = time.Now()
			e.poll(e.ctx)
		}
	}
}

// tick starts what its time has come for.
func (e *Engine) tick(now time.Time) {
	e.store.Lock()
	var due []loopy.Routine
	for _, routine := range e.store.Routines {
		if routine.On && routine.Trigger == "" {
			if next := loopy.Next(routine.When, routine.LastRun.In(now.Location())); !next.IsZero() && !next.After(now) {
				due = append(due, routine)
			}
		}
	}
	summary, last := e.store.Settings.Summary, e.store.Settings.LastSummary
	e.store.Unlock()
	for _, routine := range due { // one missed while 1loop was closed runs once
		e.start(routine, "schedule", "")
	}
	if summary == "" {
		summary = "08:00"
	}
	if at, err := time.Parse("15:04", summary); err == nil {
		today := time.Date(now.Year(), now.Month(), now.Day(), at.Hour(), at.Minute(), 0, 0, now.Location())
		if !now.Before(today) && last.Before(today) && now.Sub(today) < 3*time.Hour {
			e.store.Lock()
			e.store.Settings.LastSummary = now
			e.store.Unlock()
			e.save()
			e.Summary("the morning summary")
		}
	}
	if e.memory.LearnNow(now) {
		e.learnSoon()
	}
}

// Summary has Loopy say what is going on.
func (e *Engine) Summary(why string) {
	e.enqueue(queued{why: why, text: "[Summary] Show one summary card (kind summary): what needs the user first, then what is in progress, one line a topic in options with the action it offers; nothing that is not worth a line, and loopy_quiet when there is nothing at all. What 1loop gathered:\n" + e.gather(e.ctx)}, false)
}

// gather is what a summary is made of.
func (e *Engine) gather(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	var b strings.Builder
	e.store.Lock()
	for _, m := range e.store.Messages {
		if m.Card.Waiting() {
			fmt.Fprintf(&b, "- A card waits for them: %s %q\n", m.Card.Kind, m.Card.Title)
		}
	}
	for _, task := range e.store.Tasks[max(0, len(e.store.Tasks)-12):] {
		if task.Status == "running" || task.Status == "waiting" || e.now().Sub(task.Time) < 36*time.Hour && task.Status != "proposed" {
			fmt.Fprintf(&b, "- Coding task %q: %s %s\n", task.Title, task.Status, strings.Join(task.PRs, " "))
		}
	}
	for _, routine := range e.store.Routines {
		if n := len(routine.Runs); n > 0 && e.now().Sub(routine.Runs[n-1].Time) < 24*time.Hour {
			fmt.Fprintf(&b, "- Routine %q last ran %s: %s\n", routine.Title, routine.Runs[n-1].Time.Local().Format("15:04"), routine.Runs[n-1].Outcome)
		}
	}
	e.store.Unlock()
	if account, ok := e.account("calendar"); ok {
		day := e.now()
		day = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
		if found, err := events(ctx, account, day, day.AddDate(0, 0, 1)); err == nil && len(found) > 0 {
			b.WriteString(untrusted("today's calendar", strings.Join(found, "\n")) + "\n")
		}
	}
	if account, ok := e.account("mail"); ok {
		if box, err := dialMail(ctx, account); err == nil {
			found, _ := letters(box, "", true, e.now().AddDate(0, 0, -2), 0, 15)
			box.Logout()
			var lines []string
			for _, l := range found {
				lines = append(lines, fmt.Sprintf("id %d · %s · %s", l.UID, l.From, l.Subject))
			}
			if len(lines) > 0 {
				b.WriteString(untrusted("unread mail of the last two days", strings.Join(lines, "\n")) + "\n")
			}
		}
	}
	var notices []struct {
		Reason  string
		Subject struct{ Title, Type, URL string }
		Repo    struct {
			Name string `json:"full_name"`
		} `json:"repository"`
	}
	if auth := e.host.GitHub(ctx); auth != "" && e.getGitHub(ctx, auth, "/notifications?per_page=15", &notices) == nil && len(notices) > 0 {
		var lines []string
		for _, n := range notices {
			number := n.Subject.URL[strings.LastIndex(n.Subject.URL, "/")+1:]
			lines = append(lines, fmt.Sprintf("%s#%s %s: %s (%s)", n.Repo.Name, number, n.Subject.Type, n.Subject.Title, n.Reason))
		}
		b.WriteString(untrusted("GitHub notifications", strings.Join(lines, "\n")) + "\n")
	}
	if b.Len() == 0 {
		return "(nothing)"
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Triggers.

var githubAPI = "https://api.github.com" // tests set it

func (e *Engine) getGitHub(ctx context.Context, auth, path string, out any) error {
	request, err := http.NewRequestWithContext(ctx, "GET", githubAPI+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", auth)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "1loop")
	response, err := (&http.Client{Timeout: 25 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("GitHub answered %s", response.Status)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(out)
}

// poll looks at what the triggers watch.
func (e *Engine) poll(ctx context.Context) {
	e.store.Lock()
	routines := slices.Clone(e.store.Routines)
	e.store.Unlock()
	for _, routine := range routines {
		if !routine.On || ctx.Err() != nil {
			continue
		}
		switch routine.Trigger {
		case "github":
			e.pollGitHub(ctx, routine)
		case "mail":
			e.pollMail(ctx, routine)
		}
	}
}

// pollGitHub starts a routine for what is new since the last look; its
// first look only notes where things are.
func (e *Engine) pollGitHub(ctx context.Context, routine loopy.Routine) {
	auth := e.host.GitHub(ctx)
	m := githubFilter.FindStringSubmatch(routine.Filter)
	if auth == "" || m == nil {
		return
	}
	seen, _ := strconv.ParseInt(routine.Seen, 10, 64)
	latest := seen
	var found []string
	if m[2] == "ci" {
		var runs struct {
			Runs []struct {
				ID         int64
				Name       string
				Conclusion string
				Status     string
				Branch     string `json:"head_branch"`
				URL        string `json:"html_url"`
			} `json:"workflow_runs"`
		}
		if e.getGitHub(ctx, auth, "/repos/"+m[1]+"/actions/runs?per_page=20&status=completed", &runs) != nil {
			return
		}
		for _, r := range runs.Runs {
			latest = max(latest, r.ID)
			if r.ID > seen {
				found = append(found, fmt.Sprintf("CI %q on %s: %s (%s)", r.Name, r.Branch, r.Conclusion, r.URL))
			}
		}
	} else {
		var items []struct {
			Number int64
			Title  string
			Body   string
			User   struct{ Login string }
			Pull   json.RawMessage `json:"pull_request"`
		}
		if e.getGitHub(ctx, auth, "/repos/"+m[1]+"/issues?state=open&sort=created&direction=desc&per_page=20", &items) != nil {
			return
		}
		for _, item := range items {
			if (m[2] == "pr") != (len(item.Pull) > 0) {
				continue
			}
			latest = max(latest, item.Number)
			if item.Number > seen {
				found = append(found, fmt.Sprintf("%s#%d by %s: %s\n%s", m[1], item.Number, item.User.Login, item.Title, firstWords(item.Body, 120)))
			}
		}
	}
	if latest != seen || routine.Seen == "" {
		e.changeRoutine(routine.ID, func(r *loopy.Routine) { r.Seen = strconv.FormatInt(latest, 10) })
	}
	if routine.Seen == "" || len(found) == 0 {
		return
	}
	slices.Reverse(found)
	e.start(routine, "github: "+firstWords(found[0], 10), untrusted("GitHub", strings.Join(found, "\n\n")))
}

func (e *Engine) pollMail(ctx context.Context, routine loopy.Routine) {
	account, ok := e.account("mail")
	if !ok {
		return
	}
	box, err := dialMail(ctx, account)
	if err != nil {
		return
	}
	defer box.Logout()
	seen, _ := strconv.ParseUint(routine.Seen, 10, 32)
	found, err := letters(box, "", false, time.Time{}, uint32(seen), 20)
	if err != nil {
		return
	}
	latest := uint32(seen)
	var texts []string
	for _, l := range slices.Backward(found) {
		latest = max(latest, l.UID)
		if routine.Seen == "" {
			continue
		}
		about := plain(l.From + " " + l.Subject)
		match := routine.Filter == "" || slices.ContainsFunc(strings.Fields(plain(routine.Filter)), func(word string) bool { return strings.Contains(about, word) })
		if match && len(texts) < 5 {
			_, body, err := letterText(box, l.UID)
			if err == nil {
				texts = append(texts, fmt.Sprintf("id %d\nFrom: %s\nSubject: %s\n\n%s", l.UID, l.From, l.Subject, firstWords(body, 400)))
			}
		}
	}
	if latest != uint32(seen) || routine.Seen == "" {
		e.changeRoutine(routine.ID, func(r *loopy.Routine) { r.Seen = strconv.FormatUint(uint64(latest), 10) })
	}
	if len(texts) > 0 {
		e.start(routine, "mail: "+firstWords(texts[0], 10), untrusted("mail", redact(strings.Join(texts, "\n---\n"), e.secrets())))
	}
}

// ---------------------------------------------------------------------------
// Learning.

// learnSoon reads what was said since the last time, when it is time and
// nothing runs.
func (e *Engine) learnSoon() {
	e.store.Lock()
	settings := e.store.Settings
	e.store.Unlock()
	e.mu.Lock()
	if e.learning || e.run != nil || e.closed || settings.AskToRemember || e.now().Before(e.learnAfter) || !e.memory.LearnNow(e.now()) {
		e.mu.Unlock()
		return
	}
	e.learning = true
	e.wait.Add(1)
	e.mu.Unlock()
	go func() {
		defer e.wait.Done()
		err := e.learn(e.ctx, settings)
		e.mu.Lock()
		e.learning = false
		if err != nil { // a limit, no network…: later
			e.learnAfter = e.now().Add(15 * time.Minute)
		}
		e.mu.Unlock()
	}()
}

func (e *Engine) learn(ctx context.Context, settings loopy.Settings) error {
	if settings.Learner != "" {
		settings.Connection, settings.Model, settings.Effort = settings.LearnerConnection, settings.Learner, ""
	}
	cfg, err := e.host.Config(settings, e.folder.dir)
	if err == nil {
		err = usable(cfg)
	}
	if err != nil {
		return err
	}
	learned, err := e.memory.Learn(ctx, func(ctx context.Context, system, prompt string) (string, error) {
		return agent.Ask(ctx, e.host.Backend(), cfg, system, prompt)
	}, 12)
	if err != nil {
		return err
	}
	for _, fact := range learned.Added {
		if fact.Sure() { // what is only noticed is not announced
			e.add(loopy.Message{From: "loopy", Memory: "I'll remember: " + fact.Text, MemoryID: fact.ID})
		}
	}
	if len(learned.Added)+len(learned.Confirmed)+len(learned.Ended) > 0 {
		e.tell(nil, nil, true)
	}
	return nil
}
