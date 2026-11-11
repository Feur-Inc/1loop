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

data class UsageReport(
    val period: String, val offset: Int, val group: String, val label: String, val from: String, val to: String,
    val totals: UsageTotals, val rows: List<UsageRow>, val days: List<Pair<String, Double>>, val dayTokens: List<Int>,
    val compareLabel: String?, val compare: UsageTotals?, val paceSpent: Double, val paceProjected: Double, val paceDay: Int, val paceDays: Int,
    val pricesAsOf: String,
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
private val groupNames = listOf("model" to "Model", "connection" to "Connection", "project" to "Project", "branch" to "Branch", "chat" to "Chat")

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
                    Column(Modifier.weight(1f)) {
                        Text(row.label, style = Type.body, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        val calls = if (chat != null) (if (row.totals.calls == 1) "1 model call" else "${row.totals.calls} model calls") + " · " else ""
                        Text(calls + listOf(row.detail, breakdown(row.totals)).filter { it.isNotEmpty() }.joinToString(" · "), style = Type.caption.copy(color = Palette.muted))
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
                    "Costs use each model's API list price (as of ${shown.pricesAsOf}) or the price set on the desktop. Subscription connections show what the same tokens would cost on the API; they are not billed by the token. Models with no known price count in tokens only.",
                    style = Type.caption, modifier = Modifier.padding(top = 4.dp, bottom = 24.dp),
                )
            }
        }
    }
}

@Composable
private fun UsageSummary(report: UsageReport) {
    val t = report.totals
    UsageCard {
        Row(verticalAlignment = Alignment.Bottom) {
            Text(money(t.cost), style = Type.headline.copy(fontSize = 30.sp, fontWeight = FontWeight.SemiBold))
            Text("${tokenCount(t.total)} tokens · ${t.calls} calls", style = Type.small, modifier = Modifier.padding(start = 10.dp, bottom = 4.dp))
        }
        Text(breakdown(t), style = Type.caption.copy(color = Palette.muted))
        if (t.planCost > 0) Text("${money(t.planCost)} of it is API-equivalent: subscription calls, priced as if they had been made on the API.", style = Type.caption.copy(color = Palette.muted))
        if (t.unpriced > 0) Text("${tokenCount(t.unpriced)} tokens are on models with no known price and are not in the cost.", style = Type.caption.copy(color = Palette.muted))
        if (report.compare != null) Text("${report.compareLabel}: ${money(report.compare.cost)}, ${tokenCount(report.compare.total)} tokens${change(t.cost, report.compare.cost)}", style = Type.small.copy(color = Palette.text))
        if (report.paceDays > 0 && report.paceDay < report.paceDays) {
            Text("Pace: ${money(report.paceSpent)} by day ${report.paceDay} of ${report.paceDays}, heading for ${money(report.paceProjected)} this month.", style = Type.small.copy(color = Palette.text))
        }
    }
}

@Composable
private fun UsageChart(report: UsageReport) {
    val byTokens = report.days.none { it.second > 0 }
    val values = if (byTokens) report.dayTokens.map { it.toDouble() } else report.days.map { it.second }
    val highest = values.maxOrNull() ?: 0.0
    val day = SimpleDateFormat("yyyy-MM-dd", Locale.US)
    val from = report.from.take(10)
    val to = report.to.take(10)
    val lit = report.days.map { (date, _) -> report.period == "all" || (date >= from && date < to) }
    val accent = Palette.amber
    val dim = Palette.border
    UsageCard {
        val peak = if (highest <= 0) "" else if (byTokens) " · peak ${tokenCount(highest.toInt())} tokens a day" else " · peak ${money(highest)} a day"
        Text("Last 30 days$peak", style = Type.caption.copy(color = Palette.muted))
        Canvas(Modifier.fillMaxWidth().height(88.dp)) {
            if (values.isEmpty()) return@Canvas
            val slot = size.width / values.size
            val gap = minOf(4.dp.toPx(), slot / 3)
            values.forEachIndexed { i, value ->
                val height = if (highest > 0 && value > 0) maxOf(3.dp.toPx(), (value / highest * size.height).toFloat()) else 2.dp.toPx()
                drawRoundRect(if (lit[i]) accent else dim, Offset(i * slot, size.height - height), Size(slot - gap, height), CornerRadius(2.dp.toPx()))
            }
        }
        fun short(text: String) = runCatching { SimpleDateFormat("d MMM", Locale.US).format(day.parse(text)!!) }.getOrDefault(text)
        Row {
            Text(short(report.days.firstOrNull()?.first ?: ""), style = Type.caption)
            Spacer(Modifier.weight(1f))
            Text(short(report.days.lastOrNull()?.first ?: ""), style = Type.caption)
        }
    }
}
