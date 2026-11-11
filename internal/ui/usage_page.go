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

var iconChart = lineIcon("chart", `<path d="M2.5 13.5h11"/><path d="M4.5 11V8.5"/><path d="M8 11V4.5"/><path d="M11.5 11V6.5"/>`)

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
	page.body.Objects = []fyne.CanvasObject{
		page.periodBar(report),
		usageSummary(report),
		usageChart(report),
		page.groupBar(report.Group),
		page.rows(report),
		padded(10, 24, 2, 2, note(fmt.Sprintf("Costs use each model's API list price (as of %s) or the price you set. Subscription connections show what the same tokens would cost on the API; they are not billed by the token. Models with no known price count in tokens only.", report.AsOf))),
	}
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
	names := map[string]string{"model": "Model", "connection": "Connection", "project": "Project", "branch": "Branch", "chat": "Chat"}
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
		rows = append(rows, settingRow(row.Label, usageDetail(row.Detail, row.Totals), control))
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

// usageSummary is the period's total, with the compared one and the pace.
func usageSummary(report usage.Report) fyne.CanvasObject {
	totals := report.Totals
	big := textLine(usage.Money(totals.Cost), 30, colText)
	big.TextStyle = fyne.TextStyle{Bold: true}
	calls := fmt.Sprintf("%s tokens · %d model calls", usage.Count(totals.Total()), totals.Calls)
	head := container.NewHBox(big, container.NewCenter(padded(8, 0, 10, 0, textLine(calls, 13, colMuted))))
	lines := container.New(layout.NewCustomPaddedVBoxLayout(4), head, plainFlow(tokenBreakdown(totals.Tokens), 12.5, colMuted))
	if totals.PlanCost > 0 {
		lines.Add(plainFlow(usage.Money(totals.PlanCost)+" of it is API-equivalent: subscription calls, priced as if they had been made on the API.", 12.5, colMuted))
	}
	if totals.Unpriced > 0 {
		lines.Add(plainFlow(usage.Count(totals.Unpriced)+" tokens are on models with no known price and are not in the cost. Set a price under Prices.", 12.5, colMuted))
	}
	if compare := report.Compare; compare != nil {
		lines.Add(plainFlow(fmt.Sprintf("%s: %s, %s tokens%s", compare.Label, usage.Money(compare.Totals.Cost), usage.Count(compare.Totals.Total()), change(totals.Cost, compare.Totals.Cost)), 13, colText))
	}
	if pace := report.Pace; pace != nil && pace.Day < pace.Days {
		lines.Add(plainFlow(fmt.Sprintf("Pace: %s by day %d of %d, heading for %s this month.", usage.Money(pace.Spent), pace.Day, pace.Days, usage.Money(pace.Projected)), 13, colText))
	}
	return padded(0, 10, 0, 0, card(padded(14, 14, 16, 16, lines)))
}

// usageChart is the 30 days up to the period's end, its own days lit.
func usageChart(report usage.Report) fyne.CanvasObject {
	highest := 0.0
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
	for _, day := range report.Days {
		highest = math.Max(highest, value(day))
	}
	bars := container.New(&barsLayout{height: 96})
	for _, day := range report.Days {
		date, _ := time.ParseInLocation("2006-01-02", day.Date, time.Local)
		lit := report.Period == "all" || !date.Before(report.From) && date.Before(report.To)
		ink := colBorder
		if lit {
			ink = colAmber
		}
		bar := canvas.NewRectangle(ink)
		bar.CornerRadius = 2
		share := 0.0
		if highest > 0 {
			share = value(day) / highest
		}
		bars.Add(&usageBar{Rectangle: bar, share: share})
	}
	top := "Last 30 days"
	if highest > 0 {
		if byTokens {
			top += " · peak " + usage.Count(int(highest)) + " tokens a day"
		} else {
			top += " · peak " + usage.Money(highest) + " a day"
		}
	}
	first, last := "", ""
	if len(report.Days) > 0 {
		first, last = shortDate(report.Days[0].Date), shortDate(report.Days[len(report.Days)-1].Date)
	}
	axis := container.NewBorder(nil, nil, textLine(first, 11.5, colFaint), textLine(last, 11.5, colFaint))
	content := container.New(layout.NewCustomPaddedVBoxLayout(8), textLine(top, 12.5, colMuted), bars, axis)
	return padded(0, 6, 0, 0, card(padded(14, 12, 16, 16, content)))
}

func shortDate(text string) string {
	date, err := time.Parse("2006-01-02", text)
	if err != nil {
		return text
	}
	return date.Format("2 Jan")
}

// usageBar is one day's bar: its share of the tallest day.
type usageBar struct {
	*canvas.Rectangle
	share float64
}

// barsLayout sets bars side by side, standing on the bottom.
type barsLayout struct{ height float32 }

func (bars *barsLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.NewSize(120, bars.height) }

func (bars *barsLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) == 0 {
		return
	}
	slot := size.Width / float32(len(objects))
	gap := float32(math.Min(4, float64(slot)/3))
	for i, object := range objects {
		bar, _ := object.(*usageBar)
		height := float32(2)
		if bar != nil && bar.share > 0 {
			height = float32(math.Max(3, bar.share*float64(size.Height)))
		}
		object.Resize(fyne.NewSize(slot-gap, height))
		object.Move(fyne.NewPos(float32(i)*slot, size.Height-height))
	}
}

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
			note("In US dollars per million tokens. 1loop ships the providers' list prices; a price you set here wins over them, and applies to past calls too."),
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
