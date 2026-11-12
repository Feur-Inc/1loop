// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.drawText
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.CancellationException
import org.json.JSONObject
import java.net.URLEncoder
import java.text.SimpleDateFormat
import java.util.Locale

// Settings → Usage: where the tokens and the money went, as the desktop's
// Usage page shows it (internal/ui/usage_page.go), from the ledger of the
// computer or the server the agent runs on (internal/usage, GET /api/usage).

data class UsageTotals(
    val calls: Int, val input: Int, val output: Int, val cacheRead: Int, val cacheWrite: Int, val reasoning: Int,
    val cost: Double, val planCost: Double, val unpriced: Int,
) {
    val total get() = input + output + cacheRead + cacheWrite

    companion object {
        fun from(o: JSONObject?) = UsageTotals(
            o?.optInt("calls") ?: 0, o?.optInt("in") ?: 0, o?.optInt("out") ?: 0, o?.optInt("cr") ?: 0, o?.optInt("cw") ?: 0,
            o?.optInt("rs") ?: 0, o?.optDouble("cost", 0.0) ?: 0.0, o?.optDouble("planCost", 0.0) ?: 0.0, o?.optInt("unpriced") ?: 0,
        )
    }
}

data class UsageRow(val key: String, val label: String, val detail: String, val totals: UsageTotals, val previous: UsageTotals?)

/** One subscription window and its pace, worded by the computer (internal/usage/limits.go). Times in epoch milliseconds. */
data class UsageLimit(
    val connection: String, val name: String, val window: String, val hours: Double,
    val start: Long, val reset: Long, val checked: Long, val left: Double, val even: Double, val leftAtReset: Double, val emptyAt: Long?,
    val points: List<Pair<Long, Double>>, val title: String, val status: String, val statusText: String,
    val advice: String, val budget: String, val resetText: String, val days: List<UsageLimitDay>,
) {
    companion object {
        private fun time(text: String?): Long? =
            if (text.isNullOrEmpty()) null else runCatching { java.time.Instant.parse(text).toEpochMilli() }.getOrNull()

        fun from(o: JSONObject): UsageLimit {
            val points = o.optJSONArray("points")
            return UsageLimit(
                o.optString("connection"), o.optString("name"), o.optString("window"), o.optDouble("hours", 0.0),
                time(o.optString("start")) ?: 0, time(o.optString("reset")) ?: 0, time(o.optString("checked")) ?: 0,
                o.optDouble("left", 0.0), o.optDouble("even", 0.0), o.optDouble("leftAtReset", 0.0), time(o.optString("emptyAt")),
                (0 until (points?.length() ?: 0)).mapNotNull { i -> points!!.getJSONObject(i).let { p -> time(p.optString("t"))?.let { it to p.optDouble("left", 0.0) } } },
                o.optString("title"), o.optString("status"), o.optString("statusText"), o.optString("advice"), o.optString("budget"), o.optString("resetText"),
                o.optJSONArray("days").let { a ->
                    (0 until (a?.length() ?: 0)).map { i ->
                        a!!.getJSONObject(i).let { d -> UsageLimitDay(d.optString("label"), d.optDouble("used", 0.0), d.optDouble("even", 0.0), d.optDouble("plan", 0.0), d.optBoolean("today"), d.optBoolean("ahead")) }
                    }
                },
            )
        }
    }
}

/** One calendar day of a window: used, an even share, and the most to use for it to last (usage.LimitDay). */
data class UsageLimitDay(val label: String, val used: Double, val even: Double, val plan: Double, val today: Boolean, val ahead: Boolean)

data class UsageReport(
    val period: String, val offset: Int, val group: String, val label: String, val from: String, val to: String,
    val totals: UsageTotals, val rows: List<UsageRow>, val days: List<Pair<String, Double>>, val dayTokens: List<Int>,
    val compareLabel: String?, val compare: UsageTotals?, val paceSpent: Double, val paceProjected: Double, val paceDay: Int, val paceDays: Int,
    val pricesAsOf: String, val limits: List<UsageLimit>,
) {
    companion object {
        fun from(o: JSONObject): UsageReport {
            val rows = o.optJSONArray("rows")
            val days = o.optJSONArray("days")
            val pace = o.optJSONObject("pace")
            val compare = o.optJSONObject("compare")
            return UsageReport(
                o.optString("period"), o.optInt("offset"), o.optString("group"), o.optString("label"),
                o.optString("from"), o.optString("to"), UsageTotals.from(o.optJSONObject("totals")),
                (0 until (rows?.length() ?: 0)).map { i ->
                    rows!!.getJSONObject(i).let { UsageRow(it.optString("key"), it.optString("label"), it.optString("detail"), UsageTotals.from(it), it.optJSONObject("previous")?.let(UsageTotals::from)) }
                },
                (0 until (days?.length() ?: 0)).map { i -> days!!.getJSONObject(i).let { it.optString("date") to it.optDouble("cost", 0.0) } },
                (0 until (days?.length() ?: 0)).map { i -> days!!.getJSONObject(i).optInt("tokens") },
                compare?.optString("label"), compare?.optJSONObject("totals")?.let(UsageTotals::from),
                pace?.optDouble("spent", 0.0) ?: 0.0, pace?.optDouble("projected", 0.0) ?: 0.0, pace?.optInt("day") ?: 0, pace?.optInt("days") ?: 0,
                o.optString("pricesAsOf"),
                o.optJSONArray("limits").let { a -> (0 until (a?.length() ?: 0)).map { i -> UsageLimit.from(a!!.getJSONObject(i)) } },
            )
        }
    }
}

/** A cost as the desktop writes it (usage.Money). */
fun money(cost: Double): String = when {
    cost == 0.0 -> "$0"
    cost < 0.01 -> "<$0.01"
    cost < 100 -> String.format(Locale.US, "$%.2f", cost)
    else -> String.format(Locale.US, "$%.0f", cost)
}

/** Tokens as the desktop counts them (usage.Count): 950, 12.3k, 4.1M. */
fun tokenCount(n: Int): String = when {
    n < 1000 -> "$n"
    n < 1_000_000 -> String.format(Locale.US, "%.1f", n / 1e3).removeSuffix(".0") + "k"
    else -> String.format(Locale.US, "%.1f", n / 1e6).removeSuffix(".0") + "M"
}

private fun costText(totals: UsageTotals) =
    if (totals.cost == 0.0 && totals.unpriced > 0) "${tokenCount(totals.total)} tokens" else money(totals.cost)

private fun change(now: Double, before: Double): String {
    if (before <= 0 || now == before) return ""
    val percent = (now - before) / before * 100
    return String.format(Locale.US, " · %+.0f%%", percent)
}

private fun breakdown(t: UsageTotals): String = buildList {
    add("${tokenCount(t.input)} in"); add("${tokenCount(t.output)} out")
    if (t.cacheRead > 0) add("${tokenCount(t.cacheRead)} cached")
    if (t.cacheWrite > 0) add("${tokenCount(t.cacheWrite)} cache writes")
    if (t.reasoning > 0) add("${tokenCount(t.reasoning)} reasoning")
}.joinToString(", ")

/** A chat's line under its context breakdown, from its meta's "spent" (usage.ChatSpend.Text). */
fun spentText(o: JSONObject?): String {
    o ?: return ""
    fun amount(t: UsageTotals) = when {
        t.cost == 0.0 && t.unpriced > 0 -> "${tokenCount(t.total)} tokens, no known price"
        t.planCost > 0 && t.planCost >= t.cost * 0.99 -> money(t.cost) + " API-equivalent"
        else -> money(t.cost)
    }
    val own = UsageTotals.from(o)
    var text = "Spent in this chat: " + amount(own)
    if (own.calls > 0) text += " · ${tokenCount(own.total)} tokens in ${own.calls} model calls"
    o.optJSONObject("subchats")?.let { text += " · its subchats: " + amount(UsageTotals.from(it)) }
    return text
}

suspend fun RemoteApp.usageReport(period: String, offset: Int, group: String, chat: String = "", compare: Int? = null): UsageReport {
    var query = "period=$period&group=$group"
    if (offset != 0) query += "&offset=$offset"
    if (chat.isNotEmpty()) query += "&chat=" + URLEncoder.encode(chat, "UTF-8")
    if (compare != null) query += "&compare=$compare"
    return UsageReport.from(extensionsCall("/api/usage?$query"))
}

private val periodNames = listOf("day" to "Today", "week" to "Week", "month" to "Month", "all" to "All time")
private val groupNames = listOf("day" to "Day", "model" to "Model", "connection" to "Connection", "project" to "Project", "branch" to "Branch", "chat" to "Chat")

@Composable
private fun Chip(text: String, selected: Boolean, onClick: () -> Unit) {
    val shape = RoundedCornerShape(50)
    Box(
        Modifier.clip(shape).background(if (selected) Palette.selected else Color.Transparent, shape)
            .border(1.dp, if (selected) Palette.borderHot else Palette.border, shape).clickable(onClick = onClick)
            .height(36.dp).padding(horizontal = 14.dp),
        contentAlignment = Alignment.Center,
    ) { Text(text, style = Type.small.copy(color = if (selected) Palette.text else Palette.muted, fontWeight = FontWeight.Medium)) }
}

@Composable
private fun UsageCard(content: @Composable ColumnScope.() -> Unit) {
    Column(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(14.dp)).background(Palette.surface)
            .border(1.dp, Palette.borderSoft, RoundedCornerShape(14.dp)).padding(horizontal = 16.dp, vertical = 14.dp),
        verticalArrangement = Arrangement.spacedBy(6.dp),
    ) { content() }
}

@Composable
fun UsageScreen(app: RemoteApp, onBack: () -> Unit) {
    var period by rememberSaveable { mutableStateOf("month") }
    var offset by rememberSaveable { mutableIntStateOf(0) }
    var group by rememberSaveable { mutableStateOf("model") }
    var comparing by rememberSaveable { mutableStateOf(false) }
    var chat by remember { mutableStateOf<UsageRow?>(null) } // a chat whose turns are open
    var report by remember { mutableStateOf<UsageReport?>(null) }
    var problem by remember { mutableStateOf<String?>(null) }
    val leave = { if (chat != null) chat = null else onBack() }
    val back = predictiveBack(onBack = leave)
    LaunchedEffect(period, offset, group, comparing, chat, app.server?.key) {
        problem = null
        report = try {
            val open = chat
            if (open != null) app.usageReport(period, offset, "turn", chat = open.key)
            else app.usageReport(period, offset, group, compare = if (comparing && period != "all") offset - 1 else null)
        } catch (e: CancellationException) { throw e } catch (e: Exception) {
            problem = e.message ?: "The usage could not be read."
            null
        }
    }
    Column(back.fillMaxSize().background(Palette.background).windowInsetsPadding(WindowInsets.safeDrawing)) {
        Row(Modifier.padding(12.dp), verticalAlignment = Alignment.CenterVertically) {
            RoundButton(Icons.back, Color.Transparent, Palette.muted, description = "Back", onClick = leave)
            Text(chat?.label ?: "Usage", style = Type.title, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(start = 12.dp))
        }
        LazyColumn(Modifier.weight(1f), contentPadding = PaddingValues(horizontal = 16.dp, vertical = 4.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            val limits = report?.limits.orEmpty()
            if (chat == null && limits.isNotEmpty()) {
                item {
                    Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                        Text("Subscription limits", style = Type.title.copy(fontSize = 15.sp))
                        Text("How much of each subscription window is left, and the pace that makes it last until it resets.", style = Type.caption.copy(color = Palette.muted))
                    }
                }
                items(limits.groupBy { it.connection }.values.toList(), key = { "limits/" + it.first().connection }) { windows -> LimitsCard(windows) }
            }
            if (chat == null) item { Text("Spend", style = Type.title.copy(fontSize = 15.sp), modifier = Modifier.padding(top = if (limits.isEmpty()) 0.dp else 8.dp)) }
            if (chat == null) item {
                Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    for ((id, name) in periodNames) Chip(name, period == id) { period = id; offset = 0 }
                }
            }
            val shown = report
            if (chat == null && period != "all") item {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    RoundButton(Icons.back, Color.Transparent, Palette.muted, description = "Earlier", size = 36.dp) { offset-- }
                    Text(shown?.label ?: "", style = Type.body.copy(fontWeight = FontWeight.SemiBold), modifier = Modifier.padding(horizontal = 4.dp))
                    if (offset < 0) RoundButton(Icons.chevronRight, Color.Transparent, Palette.muted, description = "Later", size = 36.dp) { offset++ }
                    Spacer(Modifier.weight(1f))
                    Chip("Compare", comparing) { comparing = !comparing }
                }
            }
            problem?.let { item { Text(it, style = Type.small.copy(color = Palette.danger)) } }
            if (shown == null) {
                if (problem == null) item { Text("Loading…", style = Type.small) }
                return@LazyColumn
            }
            item { UsageSummary(shown) }
            if (chat == null) item { UsageChart(shown) }
            if (chat == null) item {
                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Text("Where it went", style = Type.title.copy(fontSize = 15.sp))
                    Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        for ((id, name) in groupNames) Chip(name, group == id) { group = id }
                    }
                }
            } else item { Text("Turns, latest first", style = Type.title.copy(fontSize = 15.sp)) }
            if (shown.rows.isEmpty()) item { Text(if (chat == null) "No model calls in this period." else "No turns in this period.", style = Type.small) }
            items(shown.rows, key = { it.key + "/" + it.label }) { row ->
                val opens = chat == null && shown.group == "chat" && row.key.isNotEmpty()
                Row(
                    Modifier.fillMaxWidth().clip(RoundedCornerShape(12.dp)).background(Palette.surface)
                        .border(1.dp, Palette.borderSoft, RoundedCornerShape(12.dp))
                        .then(if (opens) Modifier.clickable { chat = row } else Modifier).padding(horizontal = 14.dp, vertical = 12.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(3.dp)) {
                        Text(row.label, style = Type.body, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        if (chat != null) {
                            val calls = (if (row.totals.calls == 1) "1 model call" else "${row.totals.calls} model calls") + " · "
                            Text(calls + listOf(row.detail, breakdown(row.totals)).filter { it.isNotEmpty() }.joinToString(" · "), style = Type.caption.copy(color = Palette.muted))
                        } else {
                            val calls = if (row.totals.calls == 1) "1 call" else "${row.totals.calls} calls"
                            Text(listOf(row.detail, "${tokenCount(row.totals.total)} tokens", calls).filter { it.isNotEmpty() }.joinToString(" · "), style = Type.caption.copy(color = Palette.muted))
                            val part = share(row.totals, shown.totals)
                            Row(verticalAlignment = Alignment.CenterVertically) {
                                LeftBar(part * 100, null, Palette.amber, Modifier.weight(1f))
                                Text(percent(part * 100), style = Type.caption.copy(color = Palette.muted), modifier = Modifier.width(44.dp).padding(start = 8.dp))
                            }
                        }
                    }
                    Column(horizontalAlignment = Alignment.End, modifier = Modifier.padding(start = 12.dp)) {
                        Text(costText(row.totals), style = Type.body.copy(fontWeight = FontWeight.SemiBold))
                        row.previous?.let { Text("was " + costText(it) + change(row.totals.cost, it.cost), style = Type.caption.copy(color = Palette.muted)) }
                    }
                    if (opens) Icon(Icons.chevronRight, null, tint = Palette.muted, modifier = Modifier.padding(start = 6.dp).size(16.dp))
                }
            }
            item {
                Text(
                    "A call costs what its provider reported (OpenRouter), else the price set on the desktop, else the price its provider lists, else the model's API list price (as of ${shown.pricesAsOf}); \":free\" models cost nothing. Subscriptions are not billed by the token: their cost is what the same tokens would cost on the API.",
                    style = Type.caption, modifier = Modifier.padding(top = 4.dp, bottom = 24.dp),
                )
            }
        }
    }
}

/** A row's part of the period: of the cost, or of the tokens when nothing has a price. */
private fun share(row: UsageTotals, total: UsageTotals): Double = when {
    total.cost > 0 -> row.cost / total.cost
    total.total > 0 -> row.total.toDouble() / total.total
    else -> 0.0
}

/** A share of a window as the desktop writes it (usage.Percent): "64%", "0.5%". */
fun percent(value: Double): String =
    if (value > 0 && value < 1) String.format(Locale.US, "%.1f%%", value) else String.format(Locale.US, "%.0f%%", value)

private fun points(value: Double): String =
    if (value < 1.5) String.format(Locale.US, "%.1f points", value) else String.format(Locale.US, "%.0f points", value)

@Composable
private fun StatTile(label: String, value: String, under: String, ink: Color = Palette.text, modifier: Modifier = Modifier) {
    Column(modifier, verticalArrangement = Arrangement.spacedBy(2.dp)) {
        Text(label, style = Type.caption.copy(color = Palette.muted))
        Text(value, style = Type.headline.copy(fontSize = 21.sp, fontWeight = FontWeight.SemiBold, color = ink), maxLines = 1)
        if (under.isNotEmpty()) Text(under, style = Type.caption.copy(color = Palette.muted))
    }
}

/** Tiles two by two, as the phone has room for. */
@Composable
private fun Tiles(tiles: List<@Composable (Modifier) -> Unit>) {
    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
        tiles.chunked(2).forEach { pair ->
            Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                pair.forEach { tile -> tile(Modifier.weight(1f)) }
                if (pair.size == 1) Spacer(Modifier.weight(1f))
            }
        }
    }
}

@Composable
private fun UsageSummary(report: UsageReport) {
    val t = report.totals
    val spentUnder = when {
        t.cost > 0 && t.planCost >= t.cost * 0.99 -> "API-equivalent: subscriptions"
        t.planCost > 0 -> "${money(t.planCost)} of it API-equivalent"
        t.cost == 0.0 && t.calls == 0 -> "no model calls"
        else -> "billed by the token"
    }
    val read = t.input + t.cacheRead + t.cacheWrite
    val cache = if (read > 0) percent(100.0 * t.cacheRead / read) else "—"
    val compare = report.compare
    val fourth: @Composable (Modifier) -> Unit = when {
        compare != null -> { m ->
            val moved = if (compare.cost > 0 && t.cost != compare.cost) String.format(Locale.US, "%+.0f%%", (t.cost - compare.cost) / compare.cost * 100) else "same as"
            StatTile("Against " + (report.compareLabel ?: "").replaceFirstChar { it.lowercase() }, moved, "was " + money(compare.cost), if (t.cost > compare.cost && compare.cost > 0) Palette.amber else Palette.text, m)
        }
        report.paceDays > 0 && report.paceDay < report.paceDays -> { m -> StatTile("Month forecast", money(report.paceProjected), "at this pace · day ${report.paceDay} of ${report.paceDays}", modifier = m) }
        else -> { m -> StatTile("A day", money(t.cost / periodDays(report)), "on average", modifier = m) }
    }
    UsageCard {
        Tiles(listOf(
            { m -> StatTile("Spent", money(t.cost), spentUnder, modifier = m) },
            { m -> StatTile("Tokens", tokenCount(t.total), if (t.calls == 1) "in 1 model call" else "in ${t.calls} model calls", modifier = m) },
            { m -> StatTile("From the cache", cache, "of the prompt tokens read", modifier = m) },
            fourth,
        ))
        if (t.unpriced > 0) Text("${tokenCount(t.unpriced)} tokens are on models with no known price and are not in the cost.", style = Type.caption.copy(color = Palette.muted))
    }
}

/** How many days of the period have begun, at least one (as the desktop counts them). */
private fun periodDays(report: UsageReport): Double {
    val day = SimpleDateFormat("yyyy-MM-dd", Locale.US)
    val now = System.currentTimeMillis()
    val to = runCatching { java.time.Instant.parse(report.to).toEpochMilli() }.getOrNull()
    val end = if (to != null && to < now) to else now
    val from = runCatching { java.time.Instant.parse(report.from).toEpochMilli() }.getOrNull()
        ?: report.days.firstOrNull { it.second > 0 }?.let { runCatching { day.parse(it.first)!!.time }.getOrNull() }
        ?: return 1.0
    return maxOf(1.0, kotlin.math.ceil((end - from) / 86_400_000.0))
}

@Composable
private fun UsageChart(report: UsageReport) {
    val byTokens = report.days.none { it.second > 0 }
    val values = if (byTokens) report.dayTokens.map { it.toDouble() } else report.days.map { it.second }
    val highest = values.maxOrNull() ?: 0.0
    val active = values.filter { it > 0 }
    fun scale(v: Double) = if (byTokens) tokenCount(v.toInt()) else money(v)
    val day = SimpleDateFormat("yyyy-MM-dd", Locale.US)
    val from = report.from.take(10)
    val to = report.to.take(10)
    val lit = report.days.map { (date, _) -> report.period == "all" || (date >= from && date < to) }
    val accent = Palette.amber
    val dim = Palette.border
    val grid = Palette.borderSoft
    val faint = Palette.faint
    val measurer = androidx.compose.ui.text.rememberTextMeasurer()
    val small = Type.caption.copy(color = faint, fontSize = 10.sp)
    UsageCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text((if (byTokens) "Tokens a day" else "Cost a day") + " · last 30 days", style = Type.small.copy(color = Palette.text, fontWeight = FontWeight.SemiBold), modifier = Modifier.weight(1f))
            if (report.period != "all") {
                Box(Modifier.size(9.dp).clip(RoundedCornerShape(2.dp)).background(accent))
                Text(report.label.replaceFirstChar { it.lowercase() }, style = Type.caption.copy(color = Palette.muted), modifier = Modifier.padding(start = 5.dp))
            }
        }
        if (active.isNotEmpty()) Text("peak ${scale(highest)} · ${scale(active.sum() / active.size)} on an active day", style = Type.caption.copy(color = Palette.muted))
        fun short(text: String) = runCatching { SimpleDateFormat("d MMM", Locale.US).format(day.parse(text)!!) }.getOrDefault(text)
        Canvas(Modifier.fillMaxWidth().height(132.dp)) {
            if (values.isEmpty()) return@Canvas
            val axis = 44.dp.toPx()
            val top = 8.dp.toPx()
            val bottom = 18.dp.toPx()
            val plotH = size.height - top - bottom
            val plotW = size.width - axis
            for (i in 0..2) {
                val y = top + plotH * i / 2
                drawLine(grid, Offset(axis, y), Offset(size.width, y), 1.dp.toPx())
                if (i < 2 && highest > 0) {
                    val label = measurer.measure(scale(if (i == 0) highest else highest / 2), small)
                    drawText(label, topLeft = Offset(axis - 6.dp.toPx() - label.size.width, y - label.size.height / 2f))
                }
            }
            val slot = plotW / values.size
            val gap = minOf(3.dp.toPx(), slot / 3)
            values.forEachIndexed { i, value ->
                val height = if (highest > 0 && value > 0) maxOf(3.dp.toPx(), (value / highest * plotH).toFloat()) else 2.dp.toPx()
                drawRoundRect(if (lit[i]) accent else dim, Offset(axis + i * slot, top + plotH - height), Size(slot - gap, height), CornerRadius(2.dp.toPx()))
                if ((values.size - 1 - i) % 7 == 0) {
                    val label = measurer.measure(short(report.days[i].first), small)
                    val x = (axis + i * slot + (slot - gap) / 2 - label.size.width / 2f).coerceIn(axis, size.width - label.size.width)
                    drawText(label, topLeft = Offset(x, top + plotH + 4.dp.toPx()))
                }
            }
        }
    }
}

/** A track filled to [value] percent, with a tick at [mark] percent when there is one. */
@Composable
private fun LeftBar(value: Double, mark: Double?, ink: Color, modifier: Modifier = Modifier) {
    val track = Palette.track
    val tick = Palette.text
    Canvas(modifier.height(10.dp)) {
        val h = 6.dp.toPx()
        val y = (size.height - h) / 2
        drawRoundRect(track, Offset(0f, y), Size(size.width, h), CornerRadius(h / 2))
        drawRoundRect(ink, Offset(0f, y), Size((size.width * value / 100).toFloat().coerceIn(0f, size.width), h), CornerRadius(h / 2))
        if (mark != null) drawRect(tick, Offset((size.width * mark / 100).toFloat() - 1.dp.toPx(), 0f), Size(2.dp.toPx(), size.height))
    }
}

@Composable
private fun statusInk(status: String): Color = when (status) {
    "out" -> Palette.danger
    "fast" -> Palette.amber
    "spare" -> Palette.added
    else -> Palette.textSoft
}

@Composable
private fun StatusPill(limit: UsageLimit) {
    val ink = statusInk(limit.status)
    Box(Modifier.clip(RoundedCornerShape(50)).background(ink.copy(alpha = 0.15f)).padding(horizontal = 10.dp, vertical = 3.dp)) {
        Text(limit.statusText, style = Type.caption.copy(color = ink, fontWeight = FontWeight.SemiBold))
    }
}

/** A subscription's windows: the long ones with their chart, the short ones as a bar. */
@Composable
private fun LimitsCard(windows: List<UsageLimit>) {
    UsageCard {
        Text(windows.first().name.ifEmpty { "Subscription" }, style = Type.body.copy(fontWeight = FontWeight.SemiBold))
        windows.forEachIndexed { i, limit ->
            if (i > 0) Box(Modifier.fillMaxWidth().padding(vertical = 6.dp).height(1.dp).background(Palette.borderSoft))
            if (limit.hours >= 24) LimitPace(limit) else LimitLine(limit)
        }
    }
}

@Composable
private fun LimitPace(limit: UsageLimit) {
    var byDay by rememberSaveable(limit.connection + "/" + limit.window) { mutableStateOf(false) }
    Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(limit.title, style = Type.body, modifier = Modifier.weight(1f))
            StatusPill(limit)
        }
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Chip("Curve", !byDay) { byDay = false }
            Chip("By day", byDay) { byDay = true }
        }
        val gap = limit.left - limit.even
        val evenUnder = when {
            gap <= -1 -> points(-gap) + " more used than even"
            gap >= 1 -> points(gap) + " less used than even"
            else -> "where an even pace would be"
        }
        Tiles(listOf(
            { m -> StatTile("Left", percent(limit.left), "of the window", if (limit.status == "out") Palette.danger else Palette.text, m) },
            { m -> StatTile("Even pace", percent(limit.even), evenUnder, modifier = m) },
            { m -> StatTile("To last until reset", limit.budget, "at most", modifier = m) },
            { m -> StatTile("Resets", limit.resetText.substringBefore(" · "), limit.resetText, modifier = m) },
        ))
        if (byDay) DayBars(limit) else PaceChart(limit)
        Text(limit.advice, style = Type.small.copy(color = Palette.textSoft))
    }
}

@Composable
private fun LimitLine(limit: UsageLimit) {
    Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(limit.title, style = Type.body, modifier = Modifier.weight(1f))
            StatusPill(limit)
        }
        LeftBar(limit.left, limit.even, statusInk(limit.status), Modifier.fillMaxWidth())
        val reset = limit.resetText.split(" · ")
        Text("${percent(limit.left)} left · even pace ${percent(limit.even)} · up to ${limit.budget} · resets ${reset.first()}" + (reset.getOrNull(1)?.let { " ($it)" } ?: ""), style = Type.caption.copy(color = Palette.muted))
    }
}

/** The window from its start to its reset, on 0–100%: the readings, where the pace leads, and the even pace (the desktop's paceChart). */
@Composable
private fun PaceChart(limit: UsageLimit) {
    val ink = statusInk(limit.status)
    val grid = Palette.borderSoft
    val faint = Palette.faint
    val nowInk = Palette.border
    val planInk = Palette.text
    val measurer = androidx.compose.ui.text.rememberTextMeasurer()
    val small = Type.caption.copy(color = faint, fontSize = 10.sp)
    val nowStyle = Type.caption.copy(color = Palette.muted, fontSize = 10.sp, fontWeight = FontWeight.SemiBold)
    val now = remember(limit) { System.currentTimeMillis() }
    Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
        Row(horizontalArrangement = Arrangement.spacedBy(12.dp), verticalAlignment = Alignment.CenterVertically) {
            LegendKey("Recorded", ink, false)
            LegendKey("Forecast", ink, true)
            LegendKey("To last", planInk, true)
            LegendKey("Even pace", faint, true)
        }
        Canvas(Modifier.fillMaxWidth().height(180.dp)) {
            val axis = 36.dp.toPx()
            val top = 16.dp.toPx()
            val bottom = 18.dp.toPx()
            val plotW = size.width - axis - 4.dp.toPx()
            val plotH = size.height - top - bottom
            val total = (limit.reset - limit.start).toFloat().coerceAtLeast(1f)
            fun x(at: Long) = axis + plotW * ((at - limit.start) / total).coerceIn(0f, 1f)
            fun y(left: Double) = top + plotH * (1f - (left / 100).toFloat().coerceIn(0f, 1f))
            val dash = androidx.compose.ui.graphics.PathEffect.dashPathEffect(floatArrayOf(5.dp.toPx(), 4.dp.toPx()))
            for (value in listOf(100.0, 75.0, 50.0, 25.0, 0.0)) {
                drawLine(grid, Offset(axis, y(value)), Offset(axis + plotW, y(value)), 1.dp.toPx())
                val label = measurer.measure(percent(value), small)
                drawText(label, topLeft = Offset(axis - 6.dp.toPx() - label.size.width, y(value) - label.size.height / 2f))
            }
            // A day's name at each midnight inside the window, at most eight.
            val zone = java.time.ZoneId.systemDefault()
            val step = maxOf(1L, kotlin.math.ceil(limit.hours / 24 / 8).toLong())
            var midnight = java.time.Instant.ofEpochMilli(limit.start).atZone(zone).toLocalDate().plusDays(1)
            while (true) {
                val at = midnight.atStartOfDay(zone).toInstant().toEpochMilli()
                if (at >= limit.reset) break
                val label = measurer.measure(midnight.dayOfWeek.getDisplayName(java.time.format.TextStyle.SHORT, Locale.US), small)
                drawText(label, topLeft = Offset(x(at) - label.size.width / 2f, top + plotH + 4.dp.toPx()))
                midnight = midnight.plusDays(step)
            }
            drawLine(faint, Offset(x(limit.start), y(100.0)), Offset(x(limit.reset), y(0.0)), 1.5.dp.toPx(), pathEffect = dash)
            limit.points.zipWithNext().forEach { (a, b) -> drawLine(ink, Offset(x(a.first), y(a.second)), Offset(x(b.first), y(b.second)), 2.5.dp.toPx(), cap = androidx.compose.ui.graphics.StrokeCap.Round) }
            val from = Offset(x(limit.checked), y(limit.left))
            val end = limit.emptyAt?.let { Offset(x(it), y(0.0)) } ?: Offset(x(limit.reset), y(limit.leftAtReset))
            drawLine(ink, from, end, 2.dp.toPx(), pathEffect = dash)
            drawLine(planInk, from, Offset(x(limit.reset), y(0.0)), 1.5.dp.toPx(), pathEffect = dash) // the even pace from now that lasts exactly
            val nowX = x(now)
            drawLine(nowInk, Offset(nowX, top - 2.dp.toPx()), Offset(nowX, top + plotH), 1.dp.toPx())
            val nowLabel = measurer.measure("Now", nowStyle)
            drawText(nowLabel, topLeft = Offset((nowX - nowLabel.size.width / 2f).coerceIn(axis, size.width - nowLabel.size.width), 0f))
            drawCircle(ink, 4.5.dp.toPx(), from)
        }
    }
}

@Composable
private fun LegendKey(name: String, ink: Color, dashed: Boolean) {
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(5.dp)) {
        Canvas(Modifier.width(16.dp).height(8.dp)) {
            val effect = if (dashed) androidx.compose.ui.graphics.PathEffect.dashPathEffect(floatArrayOf(4.dp.toPx(), 3.dp.toPx())) else null
            drawLine(ink, Offset(0f, size.height / 2), Offset(size.width, size.height / 2), 2.dp.toPx(), pathEffect = effect)
        }
        Text(name, style = Type.caption.copy(color = Palette.muted))
    }
}

/** A window day by day: used (solid), the most each day still to come can use (outlined, green), an even day's share (white tick). */
@Composable
private fun DayBars(limit: UsageLimit) {
    val amber = Palette.amber
    val danger = Palette.danger
    val green = Palette.added
    val tick = Palette.text
    val muted = Palette.muted
    val measurer = androidx.compose.ui.text.rememberTextMeasurer()
    val small = Type.caption.copy(color = muted, fontSize = 10.sp)
    val bold = Type.caption.copy(color = Palette.text, fontSize = 10.sp, fontWeight = FontWeight.SemiBold)
    val faint = Type.caption.copy(color = Palette.faint, fontSize = 10.sp)
    Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
        Row(horizontalArrangement = Arrangement.spacedBy(12.dp), verticalAlignment = Alignment.CenterVertically) {
            Row(verticalAlignment = Alignment.CenterVertically) { Box(Modifier.size(9.dp).clip(RoundedCornerShape(2.dp)).background(amber)); Text("Used", style = Type.caption.copy(color = muted), modifier = Modifier.padding(start = 5.dp)) }
            Row(verticalAlignment = Alignment.CenterVertically) { Box(Modifier.size(9.dp).border(1.5.dp, green, RoundedCornerShape(2.dp))); Text("Most to use", style = Type.caption.copy(color = muted), modifier = Modifier.padding(start = 5.dp)) }
            LegendKey("Even share", tick, false)
        }
        Canvas(Modifier.fillMaxWidth().height(180.dp)) {
            val days = limit.days
            if (days.isEmpty()) return@Canvas
            val top = 16.dp.toPx()
            val bottom = 18.dp.toPx()
            val plotH = size.height - top - bottom
            val highest = maxOf(1.0, days.maxOf { maxOf(it.even, it.used + it.plan) })
            fun h(v: Double) = (plotH * v / highest).toFloat()
            val slot = size.width / days.size
            val width = minOf(40.dp.toPx(), slot * 0.62f)
            days.forEachIndexed { i, day ->
                val x = i * slot + (slot - width) / 2
                val base = top + plotH
                val used = h(day.used)
                if (!day.ahead) drawRoundRect(if (day.used > day.even * 1.15) danger else amber, Offset(x, base - maxOf(used, 2f)), Size(width, maxOf(used, 2f)), CornerRadius(3.dp.toPx()))
                val plan = h(day.plan)
                if (day.plan > 0) drawRoundRect(green, Offset(x, base - used - plan), Size(width, plan), CornerRadius(3.dp.toPx()), style = androidx.compose.ui.graphics.drawscope.Stroke(1.5.dp.toPx()))
                val evenY = base - h(day.even)
                drawLine(tick, Offset(x - 3.dp.toPx(), evenY), Offset(x + width + 3.dp.toPx(), evenY), 2.dp.toPx())
                val value = when {
                    day.today -> percent(day.used) + "+" + percent(day.plan)
                    day.ahead -> "≤" + percent(day.plan)
                    else -> percent(day.used)
                }
                val text = measurer.measure(value, small)
                drawText(text, topLeft = Offset(i * slot + (slot - text.size.width) / 2, minOf(base - used - plan, evenY) - text.size.height - 2.dp.toPx()))
                val label = measurer.measure(if (day.today) "Today" else day.label, if (day.today) bold else faint)
                drawText(label, topLeft = Offset(i * slot + (slot - label.size.width) / 2, base + 4.dp.toPx()))
            }
        }
    }
}
