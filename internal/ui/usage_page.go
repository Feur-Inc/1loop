// SPDX-License-Identifier: MPL-2.0

package ui

// The Usage page: where the tokens and the money went, from the usage
// ledger (internal/usage). It opens from the sidebar over the settings'
// place, like a settings page: a period (today, this week, this month, all
// time) with arrows to move it, its total and the month's pace, the last 30
// days, then the period cut by model, connection, project, branch or chat.
// A chat opens its turns. The page reads this computer's ledger, or, while
// a server's chats are on screen, the server's through its API.

import (
	"context"
	"fmt"
	"image/color"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/usage"
)

// UsagePage is the page's name among the settings' pages.
const UsagePage = "Usage"

// iconUsage is a gauge: how much is used, and how fast.
var iconUsage = lineIcon("usage", `<path d="M2.4 12.2A6 6 0 1 1 13.6 12.2"/><path d="M8 9.8 10.9 6.6"/><circle cx="8" cy="9.9" r="1.1" fill="currentColor" stroke="none"/>`)

// usageSource is where the page reads its report and prices.
type usageSource interface {
	report(ctx context.Context, query usage.Query) (usage.Report, error)
	priceTable(ctx context.Context) ([]usage.PriceRow, error)
	setPrice(ctx context.Context, model string, price *usage.Price) error
}

func (ui *desktopUI) usageSource() usageSource {
	if ui.server != nil {
		return serverUsage{ui.server}
	}
	return localUsage{ui}
}

// localUsage is this computer's ledger, read on the interface's thread
// (chats and connections give their names).
type localUsage struct{ ui *desktopUI }

func (local localUsage) report(_ context.Context, query usage.Query) (usage.Report, error) {
	return usage.Build(local.ui.usage, local.ui.prices, local.ui.usageNames, query, time.Now()), nil
}

func (local localUsage) priceTable(context.Context) ([]usage.PriceRow, error) {
	return local.ui.prices.Table(local.ui.usage.Models()), nil
}

func (local localUsage) setPrice(_ context.Context, model string, price *usage.Price) error {
	return local.ui.prices.Set(model, price)
}

// usageNames names the chats and connections the ledger keeps by id.
func (ui *desktopUI) usageNames(group, key string) string {
	if group == "connection" {
		for _, conn := range ui.connectionsList {
			if conn.ID == key {
				return conn.Name
			}
		}
		return ""
	}
	if chat := ui.chatByID(key); chat != nil {
		return chat.Title
	}
	return ""
}

// serverUsage is the server's ledger (internal/usage/http.go).
type serverUsage struct{ remote *remoteDesktop }

func (server serverUsage) report(ctx context.Context, query usage.Query) (report usage.Report, err error) {
	err = server.remote.client.json(ctx, "GET", "/api/usage?"+query.Encode(), nil, &report)
	return report, err
}

func (server serverUsage) priceTable(ctx context.Context) (rows []usage.PriceRow, err error) {
	err = server.remote.client.json(ctx, "GET", "/api/usage/prices", nil, &rows)
	return rows, err
}

func (server serverUsage) setPrice(ctx context.Context, model string, price *usage.Price) error {
	return server.remote.client.json(ctx, "POST", "/api/usage/prices", map[string]any{"model": model, "price": price}, nil)
}

// usagePage is the page while it is open.
type usagePage struct {
	ui      *desktopUI
	source  usageSource
	body    *fyne.Container
	loading int // the latest load: an older answer is dropped
}

// showUsage opens the page (the sidebar's Usage row).
func (ui *desktopUI) showUsage() { ui.prefs.openOn(UsagePage) }

// openUsage reads the ledger and prices kept beside the state file; the
// browser client keeps none (its chats run on the server).
func openUsage(statePath string) (*usage.Ledger, *usage.Prices) {
	if statePath == "" {
		return usage.Open(""), usage.OpenPrices("")
	}
	dir := filepath.Dir(statePath)
	return usage.Open(filepath.Join(dir, "usage.jsonl")), usage.OpenPrices(filepath.Join(dir, "usage-prices.json"))
}

func (ui *desktopUI) usageSettings() fyne.CanvasObject {
	page := &usagePage{ui: ui, source: ui.usageSource(), body: container.NewVBox()}
	if ui.usageQuery.Group == "" {
		ui.usageQuery = usage.Query{Period: "month", Group: "model"}
	}
	prices := newRow("Prices", iconPencil, styleChip, page.openPrices)
	page.load()
	if _, local := page.source.(localUsage); local {
		// The subscriptions' windows as they are now, for their pace; a
		// server reads its own when asked for the report.
		for _, conn := range ui.connectionsList {
			if conn.Enabled {
				ui.noteLimits(conn.ID, 2*time.Minute, page.load)
			}
		}
	}
	return container.NewVBox(ui.prefs.pageTitle("Usage", "", prices), page.body)
}

// load asks for the report of ui.usageQuery and shows it.
func (page *usagePage) load() {
	page.loading++
	ticket, query := page.loading, page.ui.usageQuery
	if local, ok := page.source.(localUsage); ok {
		report, _ := local.report(context.Background(), query)
		page.show(report)
		return
	}
	if len(page.body.Objects) == 0 {
		page.body.Objects = []fyne.CanvasObject{note("Loading the server's usage…")}
	}
	go func() {
		ctx, cancel := context.WithTimeout(page.ui.ctx, 20*time.Second)
		defer cancel()
		report, err := page.source.report(ctx, query)
		fyne.Do(func() {
			if ticket != page.loading {
				return
			}
			if err != nil {
				page.body.Objects = []fyne.CanvasObject{note("The server's usage could not be read: " + err.Error())}
				page.body.Refresh()
				return
			}
			page.show(report)
		})
	}()
}

func (page *usagePage) change(edit func(query *usage.Query)) {
	edit(&page.ui.usageQuery)
	page.load()
}

func (page *usagePage) show(report usage.Report) {
	page.body.Objects = nil
	if limits := limitsSection(report.Limits); limits != nil {
		page.body.Add(limits)
	}
	page.body.Objects = append(page.body.Objects,
		sectionTitle("Spend"),
		page.periodBar(report),
		usageSummary(report),
		usageChart(report),
		page.groupBar(report.Group),
		page.rows(report),
		padded(10, 24, 2, 2, note(fmt.Sprintf("A call costs what its provider reported (OpenRouter), else your price under Prices, else the price its provider lists, else the model's API list price (as of %s); \":free\" models cost nothing. Subscriptions are not billed by the token: their cost is what the same tokens would cost on the API.", report.AsOf))),
	)
	page.body.Refresh()
}

func (page *usagePage) periodBar(report usage.Report) fyne.CanvasObject {
	names := map[string]string{"day": "Today", "week": "Week", "month": "Month", "all": "All time"}
	periods := container.NewHBox()
	for _, period := range usage.Periods {
		chip := newRow(names[period], nil, styleChip, func() {
			page.change(func(query *usage.Query) { query.Period, query.Offset, query.Compare = period, 0, nil })
		})
		chip.SetSelected(report.Period == period)
		periods.Add(chip)
	}
	if report.Period == "all" {
		return padded(0, 10, 0, 0, periods)
	}
	earlier := newRow("", iconChevronLeft, styleChip, func() { page.change(func(query *usage.Query) { query.Offset-- }) })
	later := newRow("", iconChevronRight, styleChip, func() { page.change(func(query *usage.Query) { query.Offset++ }) })
	if report.Offset >= 0 {
		later.Disable()
	}
	label := textLine(report.Label, 14, colText)
	label.TextStyle = fyne.TextStyle{Bold: true}
	compare := newRow("Compare", nil, styleChip, func() {
		page.change(func(query *usage.Query) {
			if query.Compare != nil {
				query.Compare = nil
			} else {
				previous := query.Offset - 1
				query.Compare = &previous
			}
		})
	})
	compare.SetSelected(report.Compare != nil)
	move := container.NewHBox(earlier, container.NewCenter(padded(0, 0, 6, 6, label)), later)
	return padded(0, 10, 0, 0, container.NewBorder(nil, nil, periods, container.NewHBox(move, compare)))
}

func (page *usagePage) groupBar(group string) fyne.CanvasObject {
	names := map[string]string{"day": "Day", "model": "Model", "connection": "Connection", "project": "Project", "branch": "Branch", "chat": "Chat"}
	bar := container.NewHBox()
	for _, name := range usage.Groups {
		chip := newRow(names[name], nil, styleChip, func() { page.change(func(query *usage.Query) { query.Group = name }) })
		chip.SetSelected(group == name)
		bar.Add(chip)
	}
	return container.NewVBox(sectionTitle("Where it went"), bar, spacer(0, 6))
}

func (page *usagePage) rows(report usage.Report) fyne.CanvasObject {
	if len(report.Rows) == 0 {
		return card(padded(16, 16, 16, 16, note("No model calls in this period.")))
	}
	var rows []fyne.CanvasObject
	for _, row := range report.Rows {
		var control fyne.CanvasObject = usageAmount(row.Totals, row.Previous)
		if report.Group == "chat" && row.Key != "" {
			open := newRow("", iconChevronRight, styleChip, func() { page.openTurns(row, report.Query) })
			control = container.NewHBox(control, container.NewCenter(open))
		}
		rows = append(rows, shareRow(row.Label, rowDetail(row), share(row.Totals, report.Totals), control))
	}
	return card(rows...)
}

// openTurns shows a chat's turns in the period: which one was expensive.
func (page *usagePage) openTurns(row usage.Row, query usage.Query) {
	turns := usage.Query{Period: query.Period, Offset: query.Offset, Group: "turn", Chat: row.Key}
	body := container.NewVBox(note("Loading…"))
	show := func(report usage.Report) {
		var rows []fyne.CanvasObject
		for _, turn := range report.Rows {
			calls := fmt.Sprintf("%d model calls", turn.Calls)
			if turn.Calls == 1 {
				calls = "1 model call"
			}
			rows = append(rows, settingRow(turn.Label, usageDetail(calls+" · "+turn.Detail, turn.Totals), usageAmount(turn.Totals, nil)))
		}
		if len(rows) == 0 {
			rows = append(rows, padded(16, 16, 16, 16, note("No turns in this period.")))
		}
		body.Objects = []fyne.CanvasObject{usageSummary(report), sectionTitle("Turns, latest first"), card(rows...)}
		body.Refresh()
	}
	content := container.NewVBox(page.ui.prefs.pageTitle(row.Label, "Usage", nil), body)
	page.ui.prefs.push(settingsFrame{content: content})
	if local, ok := page.source.(localUsage); ok {
		report, _ := local.report(context.Background(), turns)
		show(report)
		return
	}
	go func() {
		report, err := page.source.report(page.ui.ctx, turns)
		fyne.Do(func() {
			if err != nil {
				body.Objects = []fyne.CanvasObject{note(err.Error())}
				body.Refresh()
				return
			}
			show(report)
		})
	}()
}

// usageAmount is a row's cost (its tokens when it has no price), and the
// compared period's beside it.
func usageAmount(totals usage.Totals, previous *usage.Totals) fyne.CanvasObject {
	amount := textLine(costText(totals), 14, colText)
	amount.TextStyle = fyne.TextStyle{Bold: true}
	amount.Alignment = fyne.TextAlignTrailing
	column := container.New(layout.NewCustomPaddedVBoxLayout(2), amount)
	if previous != nil {
		line := textLine("was "+costText(*previous)+change(totals.Cost, previous.Cost), 12, colMuted)
		line.Alignment = fyne.TextAlignTrailing
		column.Add(line)
	}
	return column
}

func costText(totals usage.Totals) string {
	if totals.Cost == 0 && totals.Unpriced > 0 {
		return usage.Count(totals.Total()) + " tokens"
	}
	return usage.Money(totals.Cost)
}

// change is how a cost moved from before, as " · +12%".
func change(now, before float64) string {
	if before <= 0 || now == before {
		return ""
	}
	percent := (now - before) / before * 100
	if math.Abs(percent) >= 1000 {
		return fmt.Sprintf(" · ×%.0f", now/before)
	}
	return fmt.Sprintf(" · %+.0f%%", percent)
}

// usageDetail is a row's second line: what it is, then its tokens.
func usageDetail(detail string, totals usage.Totals) string {
	parts := []string{}
	if detail != "" {
		parts = append(parts, detail)
	}
	parts = append(parts, tokenBreakdown(totals.Tokens))
	return strings.Join(parts, " · ")
}

func tokenBreakdown(tokens usage.Tokens) string {
	parts := []string{usage.Count(tokens.Input) + " in", usage.Count(tokens.Output) + " out"}
	if tokens.CacheRead > 0 {
		parts = append(parts, usage.Count(tokens.CacheRead)+" cached")
	}
	if tokens.CacheWrite > 0 {
		parts = append(parts, usage.Count(tokens.CacheWrite)+" cache writes")
	}
	if tokens.Reasoning > 0 {
		parts = append(parts, usage.Count(tokens.Reasoning)+" reasoning")
	}
	return strings.Join(parts, ", ")
}

// usageSummary is the period's figures side by side: what it cost, its
// tokens, how much came from the cache, and the month's forecast (or the
// compared period, or the average day).
func usageSummary(report usage.Report) fyne.CanvasObject {
	totals := report.Totals
	spentUnder := "billed by the token"
	switch {
	case totals.Cost > 0 && totals.PlanCost >= totals.Cost*0.99:
		spentUnder = "API-equivalent: subscriptions"
	case totals.PlanCost > 0:
		spentUnder = usage.Money(totals.PlanCost) + " of it API-equivalent"
	case totals.Cost == 0 && totals.Calls == 0:
		spentUnder = "no model calls"
	}
	calls := fmt.Sprintf("in %d model calls", totals.Calls)
	if totals.Calls == 1 {
		calls = "in 1 model call"
	}
	read := totals.Input + totals.CacheRead + totals.CacheWrite
	cache := "—"
	if read > 0 {
		cache = usage.Percent(100 * float64(totals.CacheRead) / float64(read))
	}
	tiles := []fyne.CanvasObject{
		statTile("Spent", usage.Money(totals.Cost), spentUnder, colText),
		statTile("Tokens", usage.Count(totals.Total()), calls, colText),
		statTile("From the cache", cache, "of the prompt tokens read", colText),
	}
	switch pace, compare := report.Pace, report.Compare; {
	case compare != nil:
		ink, moved := colText, "same as"
		if compare.Totals.Cost > 0 && totals.Cost != compare.Totals.Cost {
			moved = fmt.Sprintf("%+.0f%%", (totals.Cost-compare.Totals.Cost)/compare.Totals.Cost*100)
			if totals.Cost > compare.Totals.Cost {
				ink = colAmber
			}
		}
		tiles = append(tiles, statTile("Against "+strings.ToLower(compare.Label[:1])+compare.Label[1:], moved, "was "+usage.Money(compare.Totals.Cost), ink))
	case pace != nil && pace.Day < pace.Days:
		tiles = append(tiles, statTile("Month forecast", usage.Money(pace.Projected), fmt.Sprintf("at this pace · day %d of %d", pace.Day, pace.Days), colText))
	default:
		tiles = append(tiles, statTile("A day", usage.Money(totals.Cost/periodDays(report)), "on average", colText))
	}
	lines := container.New(layout.NewCustomPaddedVBoxLayout(10), container.NewGridWithColumns(4, tiles...))
	if totals.Unpriced > 0 {
		lines.Add(plainFlow(usage.Count(totals.Unpriced)+" tokens are on models with no known price and are not in the cost. Set a price under Prices.", 12.5, colMuted))
	}
	return padded(0, 10, 0, 0, card(padded(14, 14, 16, 16, lines)))
}

// periodDays is how many days of the period have begun, at least one.
func periodDays(report usage.Report) float64 {
	end := time.Now()
	if !report.To.IsZero() && report.To.Before(end) {
		end = report.To
	}
	from := report.From
	if from.IsZero() {
		for _, day := range report.Days {
			if day.Cost > 0 || day.Tokens > 0 {
				from, _ = time.ParseInLocation("2006-01-02", day.Date, time.Local)
				break
			}
		}
	}
	if from.IsZero() {
		return 1
	}
	return math.Max(1, math.Ceil(end.Sub(from).Hours()/24))
}

// share is a row's part of the period: of the cost, or of the tokens when
// nothing has a price.
func share(row, total usage.Totals) float64 {
	if total.Cost > 0 {
		return row.Cost / total.Cost
	}
	if total.Total() > 0 {
		return float64(row.Total()) / float64(total.Total())
	}
	return 0
}

// rowDetail is a row's second line: what it is, its tokens and calls.
func rowDetail(row usage.Row) string {
	parts := []string{}
	if row.Detail != "" {
		parts = append(parts, row.Detail)
	}
	calls := fmt.Sprintf("%d calls", row.Calls)
	if row.Calls == 1 {
		calls = "1 call"
	}
	return strings.Join(append(parts, usage.Count(row.Total())+" tokens", calls), " · ")
}

// shareRow is a breakdown row: its name and detail, a bar of its share of
// the period with the percent, and its amount.
func shareRow(title, detail string, part float64, control fyne.CanvasObject) fyne.CanvasObject {
	name := plainFlow(title, 14, colText)
	percent := textLine(usage.Percent(100*part), 12, colMuted)
	percent.Alignment = fyne.TextAlignTrailing
	bar := container.NewBorder(nil, nil, nil, sized(44, percent), container.NewVBox(layout.NewSpacer(), newLeftBar(100*part, -10, colAmber), layout.NewSpacer()))
	text := container.New(layout.NewCustomPaddedVBoxLayout(4), name, plainFlow(detail, 12.5, colMuted), bar)
	end := padded(0, 0, 20, 0, container.NewCenter(control))
	return padded(12, 12, 16, 16, container.NewBorder(nil, nil, nil, end, text))
}

// usageChart is the 30 days up to the period's end, the period's own days
// in the accent, on a scale with its peak and its middle marked, a date
// under every seventh day.
func usageChart(report usage.Report) fyne.CanvasObject {
	byTokens := true
	for _, day := range report.Days {
		if day.Cost > 0 {
			byTokens = false
		}
	}
	value := func(day usage.Day) float64 {
		if byTokens {
			return float64(day.Tokens)
		}
		return day.Cost
	}
	scale := func(v float64) string {
		if byTokens {
			return usage.Count(int(v))
		}
		return usage.Money(v)
	}
	highest, sum, active := 0.0, 0.0, 0
	for _, day := range report.Days {
		highest = math.Max(highest, value(day))
		if value(day) > 0 {
			sum += value(day)
			active++
		}
	}
	chart := &dayChart{}
	for _, day := range report.Days {
		date, _ := time.ParseInLocation("2006-01-02", day.Date, time.Local)
		lit := report.Period == "all" || !date.Before(report.From) && date.Before(report.To)
		share := 0.0
		if highest > 0 {
			share = value(day) / highest
		}
		chart.days = append(chart.days, chartDay{date: date, share: share, lit: lit})
	}
	if highest > 0 {
		chart.top, chart.middle = scale(highest), scale(highest/2)
	}
	title := "Cost a day"
	if byTokens {
		title = "Tokens a day"
	}
	heading := textLine(title+" · last 30 days", 13, colText)
	heading.TextStyle = fyne.TextStyle{Bold: true}
	facts := ""
	if active > 0 {
		facts = fmt.Sprintf("peak %s · %s on an active day", scale(highest), scale(sum/float64(active)))
	}
	period := legendSwatch(colAmber, strings.ToLower(report.Label[:1])+report.Label[1:])
	if report.Period == "all" {
		period = nil
	}
	top := container.NewBorder(nil, nil, heading, period, nil)
	content := container.New(layout.NewCustomPaddedVBoxLayout(6), top, textLine(facts, 12, colMuted), newDayChart(chart))
	return padded(0, 6, 0, 0, card(padded(14, 12, 16, 16, content)))
}

// legendSwatch is a square of a colour and what it stands for.
func legendSwatch(ink color.Color, name string) fyne.CanvasObject {
	square := canvas.NewRectangle(ink)
	square.CornerRadius = 2
	return container.NewHBox(container.NewCenter(sized(10, container.NewGridWrap(fyne.NewSize(10, 10), square))), textLine(name, 12, colMuted))
}

type chartDay struct {
	date  time.Time
	share float64
	lit   bool
}

// dayChart is the bars with their scale: lines at the peak, the middle and
// zero, their values on the left, dates below.
type dayChart struct {
	widget.BaseWidget
	days        []chartDay
	top, middle string
}

func newDayChart(chart *dayChart) *dayChart {
	chart.ExtendBaseWidget(chart)
	return chart
}

const (
	dayAxis   = 52
	dayBottom = 20
	dayHeight = 130
)

type dayChartRenderer struct {
	chart   *dayChart
	grid    [3]*canvas.Line
	values  [2]*canvas.Text
	bars    []*canvas.Rectangle
	dates   []*canvas.Text
	dateAt  []int
	objects []fyne.CanvasObject
}

func (chart *dayChart) CreateRenderer() fyne.WidgetRenderer {
	r := &dayChartRenderer{chart: chart}
	for i := range r.grid {
		r.grid[i] = canvas.NewLine(colBorderSoft)
		r.grid[i].StrokeWidth = 1
		r.objects = append(r.objects, r.grid[i])
	}
	for i, text := range []string{chart.top, chart.middle} {
		r.values[i] = textLine(text, 11, colFaint)
		r.values[i].Alignment = fyne.TextAlignTrailing
		r.objects = append(r.objects, r.values[i])
	}
	for i, day := range chart.days {
		ink := colBorder
		if day.lit {
			ink = colAmber
		}
		bar := canvas.NewRectangle(ink)
		bar.CornerRadius = 2
		r.bars = append(r.bars, bar)
		r.objects = append(r.objects, bar)
		// A date under the last day and every seventh before it.
		if (len(chart.days)-1-i)%7 == 0 {
			text := textLine(day.date.Format("2 Jan"), 11, colFaint)
			text.Alignment = fyne.TextAlignCenter
			r.dates, r.dateAt = append(r.dates, text), append(r.dateAt, i)
			r.objects = append(r.objects, text)
		}
	}
	return r
}

func (r *dayChartRenderer) MinSize() fyne.Size { return fyne.NewSize(240, dayHeight+dayBottom) }

func (r *dayChartRenderer) Layout(size fyne.Size) {
	const top = 10 // the peak's value stands clear of the line above
	plot := fyne.NewSize(size.Width-dayAxis, size.Height-dayBottom-top)
	for i, line := range r.grid {
		y := top + plot.Height*float32(i)/2
		line.Position1, line.Position2 = fyne.NewPos(dayAxis, y), fyne.NewPos(size.Width, y)
		if i < 2 {
			r.values[i].Resize(fyne.NewSize(dayAxis-8, 14))
			r.values[i].Move(fyne.NewPos(0, y-7))
		}
	}
	if len(r.bars) == 0 {
		return
	}
	slot := plot.Width / float32(len(r.bars))
	gap := float32(math.Min(4, float64(slot)/3))
	for i, bar := range r.bars {
		height := float32(2)
		if share := r.chart.days[i].share; share > 0 {
			height = float32(math.Max(3, share*float64(plot.Height)))
		}
		bar.Resize(fyne.NewSize(slot-gap, height))
		bar.Move(fyne.NewPos(dayAxis+float32(i)*slot, top+plot.Height-height))
	}
	for i, text := range r.dates {
		text.Resize(fyne.NewSize(50, 14))
		text.Move(fyne.NewPos(dayAxis+float32(r.dateAt[i])*slot+(slot-gap)/2-25, top+plot.Height+5))
	}
}

func (r *dayChartRenderer) Refresh()                     { r.Layout(r.chart.Size()) }
func (r *dayChartRenderer) Destroy()                     {}
func (r *dayChartRenderer) Objects() []fyne.CanvasObject { return r.objects }

// openPrices lists the models used and their prices, each one editable.
func (page *usagePage) openPrices() {
	ui := page.ui
	body := container.NewVBox(note("Loading…"))
	var refresh func()
	show := func(rows []usage.PriceRow) {
		var items []fyne.CanvasObject
		for _, row := range rows {
			detail := "No price known: counted in tokens only"
			if row.Price != nil {
				detail = priceText(*row.Price)
				if row.Custom {
					detail = "Your price · " + detail
				} else if row.Listed {
					detail = "The provider's listed price · " + detail
				}
			}
			edit := newRow("Edit", nil, styleChip, func() { page.editPrice(row, refresh) })
			items = append(items, settingRow(row.Model, detail, edit))
		}
		if len(items) == 0 {
			items = append(items, padded(16, 16, 16, 16, note("No model has been used yet.")))
		}
		add := newRow("Price another model", iconPlus, styleChip, func() { page.editPrice(usage.PriceRow{}, refresh) })
		body.Objects = []fyne.CanvasObject{
			note("In US dollars per million tokens. A call is costed at what its provider reported (OpenRouter), else at your price here, else at the price its provider lists (OpenRouter's catalog, or a compatible API that lists prices), else at 1loop's list prices. \":free\" models cost nothing. Your prices apply to past calls too."),
			spacer(0, 8), card(items...), padded(10, 0, 0, 0, container.NewHBox(add)),
		}
		body.Refresh()
	}
	refresh = func() {
		go func() {
			rows, err := page.source.priceTable(ui.ctx)
			fyne.Do(func() {
				if err != nil {
					body.Objects = []fyne.CanvasObject{note(err.Error())}
					body.Refresh()
					return
				}
				show(rows)
			})
		}()
	}
	ui.prefs.push(settingsFrame{content: container.NewVBox(ui.prefs.pageTitle("Prices", "Usage", nil), body), leave: func() { page.load() }})
	refresh()
}

func priceText(price usage.Price) string {
	text := fmt.Sprintf("$%s in · $%s out", priceNumber(price.Input), priceNumber(price.Output))
	if price.CacheRead > 0 {
		text += " · $" + priceNumber(price.CacheRead) + " cached"
	}
	if price.CacheWrite > 0 {
		text += " · $" + priceNumber(price.CacheWrite) + " cache write"
	}
	return text
}

func priceNumber(value float64) string { return strconv.FormatFloat(value, 'f', -1, 64) }

// editPrice sets a model's price, or gives it back its shipped one.
func (page *usagePage) editPrice(row usage.PriceRow, done func()) {
	ui := page.ui
	model := widget.NewEntry()
	model.SetText(row.Model)
	model.SetPlaceHolder("Model, as the connection names it")
	fields := map[string]*widget.Entry{}
	var price usage.Price
	if row.Price != nil {
		price = *row.Price
	}
	values := []struct {
		name  string
		value float64
	}{{"Input", price.Input}, {"Output", price.Output}, {"Cache read", price.CacheRead}, {"Cache write", price.CacheWrite}}
	form := container.New(layout.NewCustomPaddedVBoxLayout(0))
	if row.Model == "" {
		form.Add(settingRow("Model", "", sized(260, model)))
	}
	for _, value := range values {
		entry := widget.NewEntry()
		if row.Price != nil {
			entry.SetText(priceNumber(value.value))
		}
		entry.SetPlaceHolder("0")
		fields[value.name] = entry
		form.Add(settingRow(value.name, "US dollars per million tokens", sized(120, entry)))
	}
	problem := note("")
	parse := func(name string) (float64, bool) {
		text := strings.TrimPrefix(strings.TrimSpace(fields[name].Text), "$")
		if text == "" {
			return 0, true
		}
		number, err := strconv.ParseFloat(text, 64)
		return number, err == nil && number >= 0
	}
	save := func(clear bool) {
		name := strings.TrimSpace(model.Text)
		if name == "" {
			problem.SetText("Name the model.")
			return
		}
		var next *usage.Price
		if !clear {
			next = &usage.Price{}
			for _, target := range []struct {
				name string
				into *float64
			}{{"Input", &next.Input}, {"Output", &next.Output}, {"Cache read", &next.CacheRead}, {"Cache write", &next.CacheWrite}} {
				number, ok := parse(target.name)
				if !ok {
					problem.SetText(target.name + " is not a price.")
					return
				}
				*target.into = number
			}
		}
		go func() {
			err := page.source.setPrice(ui.ctx, name, next)
			fyne.Do(func() {
				if err != nil {
					problem.SetText(err.Error())
					return
				}
				ui.prefs.back()
				done()
			})
		}()
	}
	title := row.Model
	if title == "" {
		title = "Price a model"
	}
	actions := []fyne.CanvasObject{newRow("Cancel", nil, styleOutline, ui.prefs.back)}
	if row.Custom {
		reset := "Use 1loop's price"
		if row.Shipped == nil {
			reset = "Remove my price"
		}
		actions = append(actions, newRow(reset, nil, styleOutline, func() { save(true) }))
	}
	actions = append(actions, newRow("Save", nil, stylePrimary, func() { save(false) }))
	detail := "1loop knows no price for this model."
	if row.Shipped != nil {
		detail = "1loop's price: " + priceText(*row.Shipped)
	} else if row.Price != nil && !row.Custom {
		detail = "1loop's price, as shipped."
	}
	if row.Model == "" {
		detail = "A model 1loop has no price for, or one whose price differs for you (a discount, a reseller)."
	}
	content := container.NewVBox(ui.prefs.pageTitle(title, "Prices", nil), note(detail), spacer(0, 8), card(form), problem)
	ui.prefs.push(settingsFrame{content: content, actions: actions})
}

// todayUsage is what this computer's chats spent today.
func (ui *desktopUI) todayUsage() usage.Totals {
	from, to, _ := usage.Range("day", 0, time.Now())
	return usage.Sum(ui.prices, ui.usage.Between(from, to))
}

// todayText is today's spend in a few characters, "" before the first call.
func todayText(totals usage.Totals) string {
	switch {
	case totals.Calls == 0:
		return ""
	case totals.Cost > 0:
		return usage.Money(totals.Cost)
	}
	return usage.Count(totals.Total()) + " tok"
}

// followUsage keeps today's spend up to date in the sidebar and the tray.
func (ui *desktopUI) followUsage() {
	ui.usage.OnAdd(func(usage.Record) { fyne.Do(ui.usageChanged) })
	ui.usageChanged()
}

func (ui *desktopUI) usageChanged() {
	if ui.usageRow != nil {
		text := ""
		if ui.server == nil { // the server's chats spend on the server
			text = todayText(ui.todayUsage())
		}
		ui.usageRow.SetTrailing(text)
	}
	ui.syncTray()
}
