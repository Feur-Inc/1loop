// SPDX-License-Identifier: MPL-2.0

package usage

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Query is what the Usage page asks for: a period (day, week, month or
// all), how many periods back from the current one, and what to cut it
// by (model, connection, project, branch, chat, or turn for one chat).
type Query struct {
	Period string `json:"period"`
	Offset int    `json:"offset,omitempty"` // 0 the current period, -1 the one before…
	Group  string `json:"group"`
	Chat   string `json:"chat,omitempty"` // only this chat's calls
	// Compare sets another period of the same kind beside it, by its offset.
	Compare *int `json:"compare,omitempty"`
}

// Totals add calls up. Cost counts every call that could be priced;
// PlanCost is the part of it from calls paid by a subscription, what the
// same tokens would cost on the API. Unpriced are the tokens of the calls
// that could not be (an unknown or local model).
type Totals struct {
	Calls int `json:"calls"`
	Tokens
	Cost     float64 `json:"cost"`
	PlanCost float64 `json:"planCost,omitempty"`
	Unpriced int     `json:"unpriced,omitempty"`
}

func (totals *Totals) add(prices *Prices, record Record) {
	totals.Calls++
	totals.Tokens.add(record.Tokens)
	cost, ok := prices.Cost(record)
	if !ok {
		totals.Unpriced += record.Total()
		return
	}
	totals.Cost += cost
	if Billing(record.Provider) == "plan" && record.Cost == nil {
		totals.PlanCost += cost
	}
}

// Sum adds calls up.
func Sum(prices *Prices, records []Record) Totals {
	var totals Totals
	for _, record := range records {
		totals.add(prices, record)
	}
	return totals
}

// Row is one cut of the period: a model, a chat, a turn…
type Row struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
	Totals
	Previous *Totals   `json:"previous,omitempty"` // the same key in the compared period
	Last     time.Time `json:"last"`
}

// Day is one day of the chart.
type Day struct {
	Date   string  `json:"date"` // 2006-01-02
	Cost   float64 `json:"cost"`
	Tokens int     `json:"tokens"`
}

// Pace is the month so far and where it is heading.
type Pace struct {
	Spent     float64 `json:"spent"`
	Projected float64 `json:"projected"`
	Day       int     `json:"day"`  // of the month, today or the last
	Days      int     `json:"days"` // in the month
}

// Comparison is the other period of a comparison.
type Comparison struct {
	Label  string `json:"label"`
	Totals Totals `json:"totals"`
}

// Report is the Usage page's content.
type Report struct {
	Query
	Label   string      `json:"label"`
	From    time.Time   `json:"from,omitzero"`
	To      time.Time   `json:"to,omitzero"`
	Totals  Totals      `json:"totals"`
	Rows    []Row       `json:"rows"`
	Days    []Day       `json:"days"`
	Pace    *Pace       `json:"pace,omitempty"`
	Compare *Comparison `json:"compare,omitempty"`
	AsOf    string      `json:"pricesAsOf"`
}

// Names gives what the ledger keeps by id a name: a chat's title, a
// connection's name. An empty answer is a chat that was deleted.
type Names func(group, key string) string

// Periods and groups, as the page offers them.
var (
	Periods = []string{"day", "week", "month", "all"}
	Groups  = []string{"model", "connection", "project", "branch", "chat"}
)

// Range is a period's bounds in the time zone of now, and its name.
func Range(period string, offset int, now time.Time) (from, to time.Time, label string) {
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	switch period {
	case "day":
		from = day.AddDate(0, 0, offset)
		to = from.AddDate(0, 0, 1)
		switch offset {
		case 0:
			label = "Today"
		case -1:
			label = "Yesterday"
		default:
			label = from.Format("Mon 2 Jan 2006")
		}
	case "week":
		monday := day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7))
		from = monday.AddDate(0, 0, 7*offset)
		to = from.AddDate(0, 0, 7)
		switch offset {
		case 0:
			label = "This week"
		case -1:
			label = "Last week"
		default:
			last := to.AddDate(0, 0, -1)
			label = from.Format("2 Jan") + " – " + last.Format("2 Jan 2006")
		}
	case "month":
		from = time.Date(now.Year(), now.Month()+time.Month(offset), 1, 0, 0, 0, 0, now.Location())
		to = from.AddDate(0, 1, 0)
		label = from.Format("January 2006")
		if offset == 0 {
			label = "This month"
		}
	default:
		label = "All time"
	}
	return from, to, label
}

// Build makes the report a query asks for, from the ledger, at now.
func Build(ledger *Ledger, prices *Prices, names Names, query Query, now time.Time) Report {
	if query.Group == "" {
		query.Group = "model"
	}
	if query.Period == "" {
		query.Period = "month"
	}
	if query.Period == "all" {
		query.Offset, query.Compare = 0, nil
	}
	if query.Offset > 0 {
		query.Offset = 0
	}
	from, to, label := Range(query.Period, query.Offset, now)
	report := Report{Query: query, Label: label, From: from, To: to, AsOf: PricesAsOf}
	keep := func(record Record) bool { return query.Chat == "" || record.Chat == query.Chat }
	rows := map[string]*Row{}
	for _, record := range ledger.Between(from, to) {
		if !keep(record) {
			continue
		}
		report.Totals.add(prices, record)
		key := groupKey(query.Group, record)
		row := rows[key]
		if row == nil {
			row = &Row{Key: key}
			rows[key] = row
			row.Label, row.Detail = rowLabel(query.Group, record, names, now)
		} else if query.Group == "turn" && !strings.Contains(row.Detail, record.Model) {
			row.Detail += ", " + record.Model
		}
		row.add(prices, record)
		if record.Time.After(row.Last) {
			row.Last = record.Time
		}
	}
	if query.Compare != nil && *query.Compare != query.Offset {
		otherFrom, otherTo, otherLabel := Range(query.Period, *query.Compare, now)
		report.Compare = &Comparison{Label: otherLabel}
		previous := map[string]*Totals{}
		for _, record := range ledger.Between(otherFrom, otherTo) {
			if !keep(record) {
				continue
			}
			report.Compare.Totals.add(prices, record)
			key := groupKey(query.Group, record)
			if previous[key] == nil {
				previous[key] = &Totals{}
			}
			previous[key].add(prices, record)
			if rows[key] == nil && query.Group != "turn" {
				row := &Row{Key: key}
				row.Label, row.Detail = rowLabel(query.Group, record, names, now)
				rows[key] = row
			}
		}
		for key, row := range rows {
			if totals := previous[key]; totals != nil {
				row.Previous = totals
			} else {
				row.Previous = &Totals{}
			}
		}
	}
	for _, row := range rows {
		report.Rows = append(report.Rows, *row)
	}
	sort.Slice(report.Rows, func(i, j int) bool {
		a, b := report.Rows[i], report.Rows[j]
		if query.Group == "turn" {
			return a.Last.After(b.Last)
		}
		if a.Cost != b.Cost {
			return a.Cost > b.Cost
		}
		if a.Total() != b.Total() {
			return a.Total() > b.Total()
		}
		return a.Key < b.Key
	})
	report.Days = days(ledger, prices, keep, to, now)
	if query.Period == "month" {
		report.Pace = pace(report.Totals.Cost, from, to, now)
	}
	return report
}

func groupKey(group string, record Record) string {
	switch group {
	case "connection":
		if record.Connection == "" {
			return record.Provider
		}
		return record.Connection
	case "project":
		return record.Project
	case "branch":
		return record.Project + "\x00" + record.Branch
	case "chat":
		return record.Chat
	case "turn":
		return record.Turn
	}
	return Key(record.Model)
}

func rowLabel(group string, record Record, names Names, now time.Time) (label, detail string) {
	name := func(key string) string {
		if names == nil {
			return ""
		}
		return names(group, key)
	}
	switch group {
	case "connection":
		label = name(record.Connection)
		if label == "" {
			label = record.Provider
		}
		switch Billing(record.Provider) {
		case "plan":
			detail = "Subscription: API-equivalent cost"
		case "local":
			detail = "Local"
		}
	case "project":
		label = projectName(record.Project)
		detail = record.Project
	case "branch":
		label = record.Branch
		if label == "" {
			label = "No branch"
		}
		detail = projectName(record.Project)
	case "chat":
		label = name(record.Chat)
		if label == "" {
			label = "Deleted chat"
		}
		detail = projectName(record.Project)
		if record.Parent != "" {
			if parent := name(record.Parent); parent != "" {
				detail = "Subchat of " + parent
			} else {
				detail = "Subchat"
			}
		}
	case "turn":
		local := record.Time.In(now.Location())
		if local.Year() == now.Year() && local.YearDay() == now.YearDay() {
			label = "Today " + local.Format("15:04")
		} else {
			label = local.Format("Mon 2 Jan 15:04")
		}
		detail = record.Model
	default:
		label = Key(record.Model)
		if label == "" {
			label = "Unknown model"
		}
		if _, ok := Shipped(record.Model); !ok && Billing(record.Provider) != "local" {
			detail = "No price known: tokens only"
		}
	}
	return label, detail
}

func projectName(folder string) string {
	if folder == "" {
		return "No folder"
	}
	return filepath.Base(folder)
}

// days is the chart: the 30 days up to the period's end (or today).
func days(ledger *Ledger, prices *Prices, keep func(Record) bool, to, now time.Time) []Day {
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, 1)
	if !to.IsZero() && to.Before(end) {
		end = to
	}
	start := end.AddDate(0, 0, -30)
	out := make([]Day, 30)
	for i := range out {
		out[i].Date = start.AddDate(0, 0, i).Format("2006-01-02")
	}
	for _, record := range ledger.Between(start, end) {
		if !keep(record) {
			continue
		}
		local := record.Time.In(now.Location())
		day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, now.Location())
		i := int(day.Sub(start).Hours()/24 + 0.5)
		if i < 0 || i >= len(out) {
			continue
		}
		cost, _ := prices.Cost(record)
		out[i].Cost += cost
		out[i].Tokens += record.Total()
	}
	return out
}

func pace(spent float64, from, to, now time.Time) *Pace {
	days := int(to.Sub(from).Hours()/24 + 0.5)
	result := &Pace{Spent: spent, Projected: spent, Days: days, Day: days}
	if now.After(from) && now.Before(to) {
		gone := now.Sub(from).Seconds() / to.Sub(from).Seconds()
		result.Day = now.Day()
		if gone > 0 {
			result.Projected = spent / gone
		}
	}
	return result
}

// Money writes a cost the way the page does.
func Money(cost float64) string {
	switch {
	case cost == 0:
		return "$0"
	case cost < 0.01:
		return "<$0.01"
	case cost < 100:
		return fmt.Sprintf("$%.2f", cost)
	}
	return fmt.Sprintf("$%.0f", cost)
}

// Count writes a number of tokens: 950, 12.3k, 4.1M.
func Count(tokens int) string {
	switch {
	case tokens < 1000:
		return fmt.Sprint(tokens)
	case tokens < 1_000_000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(tokens)/1e3), ".0") + "k"
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(tokens)/1e6), ".0") + "M"
}
