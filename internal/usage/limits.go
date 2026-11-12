// SPDX-License-Identifier: MPL-2.0

package usage

// Subscription limits over time: how much of each usage window (a week,
// 5 hours…) the provider says is left, noted each time 1loop reads it
// (agent.ConnectionLimits), in usage-limits.jsonl beside the ledger. From
// those notes the Usage page draws the window as it went, the even pace
// that would last exactly until the reset, and where the current pace
// leads: when the window runs out, or what is left at the reset.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// LimitWindow is one usage window of a subscription, as its provider
// reported it: its label ("7d", "Sonnet · 7d"), the percent left, and when
// it resets.
type LimitWindow struct {
	Label string
	Left  float64
	Reset time.Time
}

// limitNote is one reading of one window, a line of the file.
type limitNote struct {
	Time       time.Time `json:"t"`
	Connection string    `json:"conn"`
	Window     string    `json:"window"`
	Left       float64   `json:"left"`
	Reset      time.Time `json:"reset"`
}

// limitLog is the notes, read once and added to.
type limitLog struct {
	mu    sync.Mutex
	path  string
	notes []limitNote // by time
	read  map[string]time.Time
}

func openLimits(path string) *limitLog {
	log := &limitLog{path: path, read: map[string]time.Time{}}
	file, err := os.Open(path)
	if err != nil {
		return log
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var note limitNote
		if json.Unmarshal(scanner.Bytes(), &note) == nil && !note.Time.IsZero() && note.Window != "" {
			log.notes = append(log.notes, note)
		}
	}
	sort.SliceStable(log.notes, func(i, j int) bool { return log.notes[i].Time.Before(log.notes[j].Time) })
	for _, note := range log.notes {
		log.read[note.Connection] = note.Time
	}
	return log
}

// NoteLimits keeps a connection's windows as read at at. A window with no
// reset time is not kept: without it, where the window started is unknown.
// A reading equal to the last one is kept only once an hour, enough to draw
// the flat parts.
func (ledger *Ledger) NoteLimits(connection string, windows []LimitWindow, at time.Time) {
	if ledger == nil || ledger.limits == nil || connection == "" {
		return
	}
	log := ledger.limits
	at = at.UTC().Truncate(time.Second)
	log.mu.Lock()
	defer log.mu.Unlock()
	log.read[connection] = at
	var lines []byte
	for _, window := range windows {
		if window.Reset.IsZero() || windowHours(window.Label) <= 0 {
			continue
		}
		note := limitNote{Time: at, Connection: connection, Window: window.Label, Left: math.Round(window.Left*10) / 10, Reset: window.Reset.UTC().Truncate(time.Second)}
		if last, ok := log.last(connection, window.Label); ok && last.Left == note.Left && sameReset(last.Reset, note.Reset) && at.Sub(last.Time) < time.Hour {
			continue
		}
		log.notes = append(log.notes, note)
		if line, err := json.Marshal(note); err == nil {
			lines = append(append(lines, line...), '\n')
		}
	}
	if len(lines) > 0 && log.path != "" && os.MkdirAll(filepath.Dir(log.path), 0700) == nil {
		if file, err := os.OpenFile(log.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600); err == nil {
			file.Write(lines)
			file.Close()
		}
	}
}

// LimitsDue says whether a connection's limits were last read longer than
// every ago: a turn reads them again only then.
func (ledger *Ledger) LimitsDue(connection string, every time.Duration, now time.Time) bool {
	if ledger == nil || ledger.limits == nil {
		return false
	}
	ledger.limits.mu.Lock()
	defer ledger.limits.mu.Unlock()
	return now.Sub(ledger.limits.read[connection]) >= every
}

func (log *limitLog) last(connection, window string) (limitNote, bool) {
	for i := len(log.notes) - 1; i >= 0; i-- {
		if note := log.notes[i]; note.Connection == connection && note.Window == window {
			return note, true
		}
	}
	return limitNote{}, false
}

// sameReset: providers round a window's reset time differently from one
// reading to the next; within a few minutes it is the same window.
func sameReset(a, b time.Time) bool { return a.Sub(b).Abs() <= 5*time.Minute }

var windowLength = regexp.MustCompile(`(\d+)\s*(min|h|d)$`)

// windowHours is a window's length from its label's end: "7d", "5h",
// "Sonnet · 7d". 0 when the label says none ("Free/day" has no reset).
func windowHours(label string) float64 {
	match := windowLength.FindStringSubmatch(strings.TrimSpace(label))
	if match == nil {
		return 0
	}
	n, _ := strconv.ParseFloat(match[1], 64)
	switch match[2] {
	case "min":
		return n / 60
	case "h":
		return n
	}
	return n * 24
}

// Limit is one subscription window as the Usage page shows it. Percents are
// of the window's whole allowance; times are UTC.
type Limit struct {
	Connection string    `json:"connection"`
	Name       string    `json:"name"`   // the connection's name
	Window     string    `json:"window"` // its label: "7d", "Sonnet · 7d"
	Hours      float64   `json:"hours"`  // the window's length
	Start      time.Time `json:"start"`
	Reset      time.Time `json:"reset"`
	Checked    time.Time `json:"checked"` // the last reading
	// Renewed: the window reset since the last reading, which was of the
	// one before; Left is then the whole allowance, nothing read yet.
	Renewed bool    `json:"renewed,omitempty"`
	Left    float64 `json:"left"` // percent left at Checked
	// Even is what would be left at Checked using the window evenly, to
	// reach 0 exactly at the reset.
	Even   float64      `json:"even"`
	Points []LimitPoint `json:"points"` // the readings in this window
	// Rate is the percent used an hour since the window started; Recent the
	// same over the last day of readings, 0 when there are too few.
	Rate   float64 `json:"rate"`
	Recent float64 `json:"recent,omitempty"`
	// EmptyAt is when the window runs out at Rate, when that is before the
	// reset; LeftAtReset what is left then at Rate (0 when it runs out).
	EmptyAt     time.Time `json:"emptyAt,omitzero"`
	LeftAtReset float64   `json:"leftAtReset"`
	// PerHour is the percent an hour that can be used from Checked on and
	// still last until the reset.
	PerHour float64 `json:"perHour"`
	// The words both the desktop and the phone show, in the time zone of
	// the computer that keeps the ledger.
	Title      string `json:"title"`      // "Weekly", "5-hour", "Sonnet · weekly"
	Status     string `json:"status"`     // "out", "fast", "on", "spare"
	StatusText string `json:"statusText"` // "Runs out early"…
	Advice     string `json:"advice"`     // where the pace leads, and how much to use
	Budget     string `json:"budget"`     // "21% a day", "9% an hour": what lasts until the reset
	ResetText  string `json:"resetText"`  // "Sun 14:00 · in 2 d 6 h"
}

// LimitPoint is one reading drawn on the chart.
type LimitPoint struct {
	Time time.Time `json:"t"`
	Left float64   `json:"left"`
}

// Limits are the subscription windows last read, with their pace at now:
// the weekly windows first, then the shorter ones, each connection's
// together.
func (ledger *Ledger) Limits(names Names, now time.Time) []Limit {
	if ledger == nil || ledger.limits == nil {
		return nil
	}
	log := ledger.limits
	log.mu.Lock()
	notes := append([]limitNote(nil), log.notes...)
	log.mu.Unlock()
	type key struct{ connection, window string }
	latest := map[key]limitNote{}
	var order []key
	for _, note := range notes {
		k := key{note.Connection, note.Window}
		if _, ok := latest[k]; !ok {
			order = append(order, k)
		}
		latest[k] = note
	}
	var limits []Limit
	for _, k := range order {
		last := latest[k]
		hours := windowHours(k.window)
		window := time.Duration(hours * float64(time.Hour))
		limit := Limit{Connection: k.connection, Window: k.window, Hours: hours, Reset: last.Reset, Checked: last.Time, Left: last.Left}
		if names != nil {
			limit.Name = names("connection", k.connection)
		}
		if !last.Reset.After(now) {
			// Reset since: a new window, untouched as far as 1loop knows.
			limit.Renewed, limit.Left = true, 100
			for !limit.Reset.After(now) {
				limit.Reset = limit.Reset.Add(window)
			}
			limit.Checked = limit.Reset.Add(-window)
		}
		limit.Start = limit.Reset.Add(-window)
		if !limit.Renewed {
			for _, note := range notes {
				if note.Connection == k.connection && note.Window == k.window && sameReset(note.Reset, last.Reset) && !note.Time.Before(limit.Start) {
					limit.Points = append(limit.Points, LimitPoint{Time: note.Time, Left: note.Left})
				}
			}
		}
		limit.pace(now)
		limit.describe(now)
		limits = append(limits, limit)
	}
	sort.SliceStable(limits, func(i, j int) bool {
		if limits[i].Hours != limits[j].Hours {
			return limits[i].Hours > limits[j].Hours
		}
		return limits[i].Name < limits[j].Name
	})
	return limits
}

// pace works the window's pace out from its readings.
func (limit *Limit) pace(now time.Time) {
	total := limit.Reset.Sub(limit.Start).Hours()
	toReset := limit.Reset.Sub(limit.Checked).Hours()
	if total <= 0 {
		return
	}
	limit.Even = math.Max(0, math.Min(100, 100*toReset/total))
	if elapsed := limit.Checked.Sub(limit.Start).Hours(); elapsed > 0.05 {
		limit.Rate = math.Max(0, 100-limit.Left) / elapsed
	}
	// The last day of readings, when they span at least two hours.
	if n := len(limit.Points); n >= 2 {
		last := limit.Points[n-1]
		for _, point := range limit.Points {
			if span := last.Time.Sub(point.Time).Hours(); span >= 2 && span <= 24 {
				limit.Recent = math.Max(0, point.Left-last.Left) / span
				break
			}
		}
	}
	if toReset > 0 {
		limit.PerHour = limit.Left / toReset
	}
	limit.LeftAtReset = limit.Left
	if limit.Rate > 0 {
		limit.LeftAtReset = math.Max(0, limit.Left-limit.Rate*toReset)
		if hours := limit.Left / limit.Rate; hours < toReset {
			limit.EmptyAt = limit.Checked.Add(time.Duration(hours * float64(time.Hour))).UTC().Truncate(time.Minute)
		}
	}
}

// describe words the window's pace.
func (limit *Limit) describe(now time.Time) {
	daily := limit.Hours >= 24
	unit, scale := "an hour", 1.0
	if daily {
		unit, scale = "a day", 24
	}
	rate := func(perHour float64) string { return Percent(perHour*scale) + " " + unit }
	limit.Title = windowTitle(limit.Window)
	limit.Budget = rate(limit.PerHour)
	reset := limit.Reset.In(now.Location())
	limit.ResetText = clock(reset, now) + " · in " + span(limit.Reset.Sub(now))
	gap := limit.Even - limit.Left
	switch {
	case limit.Renewed:
		limit.Status, limit.StatusText = "spare", "Just reset"
		limit.Advice = "This window reset " + clock(limit.Start.In(now.Location()), now) + ". Its pace shows once 1loop reads it again, after your next turn."
		return
	case !limit.EmptyAt.IsZero():
		limit.Status, limit.StatusText = "out", "Runs out early"
		limit.Advice = fmt.Sprintf("At your pace since the window opened (%s), it runs out %s, %s before the reset. To make it last, keep under %s.",
			rate(limit.Rate), clock(limit.EmptyAt.In(now.Location()), now), span(limit.Reset.Sub(limit.EmptyAt)), limit.Budget)
	case gap > 5:
		limit.Status, limit.StatusText = "fast", "Faster than even"
		limit.Advice = fmt.Sprintf("You are %s ahead of an even pace. At this pace, about %s is left at the reset; to stay even, keep under %s.",
			Points(gap), Percent(limit.LeftAtReset), limit.Budget)
	case gap < -10:
		limit.Status, limit.StatusText = "spare", "Room to spare"
		limit.Advice = fmt.Sprintf("You are %s under an even pace: about %s would be left unused at the reset. You can use up to %s.",
			Points(-gap), Percent(limit.LeftAtReset), limit.Budget)
	default:
		limit.Status, limit.StatusText = "on", "On pace"
		limit.Advice = fmt.Sprintf("Close to an even pace: about %s is left at the reset. Up to %s lasts until then.", Percent(limit.LeftAtReset), limit.Budget)
	}
	if limit.Recent > 0 {
		limit.Advice += " Over the last day of readings: " + rate(limit.Recent) + "."
	}
}

// windowTitle names a window from its label: "7d" is "Weekly", "Sonnet ·
// 7d" "Sonnet · weekly".
func windowTitle(label string) string {
	prefix, length := "", strings.TrimSpace(label)
	if i := strings.LastIndex(label, " · "); i >= 0 {
		prefix, length = label[:i]+" · ", label[i+len(" · "):]
	}
	name := map[string]string{"7d": "weekly", "1d": "daily", "5h": "5-hour", "30d": "monthly"}[length]
	if name == "" {
		name = length + " window"
	}
	if prefix == "" {
		return strings.ToUpper(name[:1]) + name[1:]
	}
	return prefix + name
}

// Percent is a share of a window: "64%", "0.5%".
func Percent(value float64) string {
	if value > 0 && value < 1 {
		return fmt.Sprintf("%.1f%%", value)
	}
	return fmt.Sprintf("%.0f%%", value)
}

// Points is a difference of percents: "6 points".
func Points(value float64) string {
	if value < 1.5 {
		return fmt.Sprintf("%.1f points", value)
	}
	return fmt.Sprintf("%.0f points", value)
}

// clock is a moment: "14:20" today, "Sat 18:40" within the week, else a date.
func clock(at, now time.Time) string {
	switch {
	case at.Year() == now.Year() && at.YearDay() == now.YearDay():
		return "today " + at.Format("15:04")
	case at.Sub(now).Abs() < 6*24*time.Hour:
		return at.Format("Mon 15:04")
	}
	return at.Format("2 Jan 15:04")
}

// span is a duration in days and hours, or hours and minutes: "2 d 6 h".
func span(d time.Duration) string {
	d = d.Abs().Round(time.Minute)
	days, hours, minutes := int(d.Hours())/24, int(d.Hours())%24, int(d.Minutes())%60
	switch {
	case days > 0 && hours > 0:
		return fmt.Sprintf("%d d %d h", days, hours)
	case days > 0:
		return fmt.Sprintf("%d d", days)
	case hours > 0 && minutes > 0:
		return fmt.Sprintf("%d h %d min", hours, minutes)
	case hours > 0:
		return fmt.Sprintf("%d h", hours)
	}
	return fmt.Sprintf("%d min", minutes)
}
