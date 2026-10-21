package io.oneloop.remote

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import org.json.JSONObject

// The handoff dialog (the desktop's handoff.go): the first message after a
// change of provider, account or model asks how the new model receives the
// conversation. The plan and its rules come from the server.

private class Summarizer(val id: String, val name: String, val cached: Boolean, val window: Int, val note: String, val problem: String)
private class Level(val id: String, val name: String, val description: String, val tokens: Int)

@OptIn(ExperimentalLayoutApi::class)
@Composable
fun HandoffDialog(ask: RemoteApp.HandoffAsk) {
    val plan = ask.plan
    val summarizers = remember(plan) {
        plan.optJSONArray("summarizers").let { a -> (0 until (a?.length() ?: 0)).map { a!!.getJSONObject(it) } }
            .map { Summarizer(it.optString("id"), it.optString("name"), it.optBoolean("cached"), it.optInt("window"), it.optString("note"), it.optString("problem")) }
    }
    val levels = remember(plan) {
        plan.optJSONArray("levels").let { a -> (0 until (a?.length() ?: 0)).map { a!!.getJSONObject(it) } }
            .map { Level(it.optString("id"), it.optString("name"), it.optString("description"), it.optInt("tokens")) }
    }
    val recommended = plan.optJSONObject("recommended") ?: JSONObject()
    var mode by remember(plan) { mutableStateOf(recommended.optString("mode", "summary")) }
    var by by remember(plan) { mutableStateOf(recommended.optString("summarizer").ifEmpty { "previous" }) }
    var level by remember(plan) { mutableStateOf(recommended.optString("level").ifEmpty { "no_outputs" }) }
    val from = plan.optString("from")
    val to = plan.optString("to")
    val chosen = summarizers.firstOrNull { it.id == by }
    // The server's rules (HandoffPlan.Problem): what cannot be chosen.
    val problem = when {
        mode == "as_is" -> plan.optString("asIsProblem")
        chosen == null -> "Choose who summarizes."
        chosen.problem.isNotEmpty() -> chosen.problem
        chosen.cached -> ""
        else -> levels.firstOrNull { it.id == level }?.let { l ->
            if (chosen.window > 0 && l.tokens <= chosen.window * 8 / 10) "" else "${chosen.name} cannot read ~${tokens(l.tokens)} tokens: its window is ${tokens(chosen.window)}."
        } ?: "Choose a level."
    }
    AlertDialog(
        onDismissRequest = ask.cancel,
        containerColor = Palette.surface,
        title = { Text("Continue with $to", style = Type.title) },
        text = {
            Column(Modifier.verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                Text("This conversation last ran with $from. $to starts without its cache: choose how it receives the conversation.", style = Type.small)
                Text("Recommended by 1loop: " + plan.optString("reason"), style = Type.small.copy(color = Palette.amber))
                FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    Pill("Continue as is", primary = mode == "as_is", enabled = plan.optString("asIsProblem").isEmpty()) { mode = "as_is" }
                    Pill("Summarize first", primary = mode == "summary") { mode = "summary" }
                }
                if (mode == "as_is") Text(plan.optString("asIsNote"), style = Type.small)
                else {
                    Text("Summarized by", style = Type.body.copy(fontWeight = FontWeight.Medium))
                    FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        summarizers.forEach { s ->
                            val tag = when { s.problem.isNotEmpty() -> "unavailable"; s.cached -> "cached"; else -> "no cache" }
                            Pill("${s.name} ($tag)", primary = by == s.id, enabled = s.problem.isEmpty()) { by = s.id }
                        }
                    }
                    chosen?.let { Text(it.problem.ifEmpty { it.note }, style = Type.small) }
                    if (chosen != null && !chosen.cached) {
                        Text("Level", style = Type.body.copy(fontWeight = FontWeight.Medium))
                        FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                            levels.forEach { l -> Pill("${l.name} · ~${tokens(l.tokens)}", primary = level == l.id, enabled = chosen.window > 0 && l.tokens <= chosen.window * 8 / 10) { level = l.id } }
                        }
                        levels.firstOrNull { it.id == level }?.let { Text(it.description, style = Type.small) }
                    }
                }
                if (problem.isNotEmpty()) Text(problem, style = Type.small.copy(color = Palette.danger))
            }
        },
        confirmButton = {
            Pill("Continue", primary = true, enabled = problem.isEmpty()) {
                val choice = JSONObject().put("mode", mode).put("target", plan.optJSONObject("target"))
                if (mode == "summary") choice.put("summarizer", by).put("level", level)
                ask.choose(choice)
            }
        },
        dismissButton = { Pill("Cancel", onClick = ask.cancel) },
    )
}
