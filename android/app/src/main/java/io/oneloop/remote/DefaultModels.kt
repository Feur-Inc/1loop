package io.oneloop.remote

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch

@Composable
fun DefaultModelsSection(app: RemoteApp) {
    val scope = rememberCoroutineScope()
    var selected by remember(app.server) { mutableStateOf("") }
    val account = app.accounts.firstOrNull { it.id == selected } ?: app.accounts.firstOrNull { it.id == app.chatModel?.account } ?: app.accounts.firstOrNull()
    var expanded by remember { mutableStateOf(false) }
    var editing by remember(account?.id) { mutableStateOf<String?>(null) }
    val draft = remember(app.server, account?.id, account?.defaultModels) {
        mutableStateMapOf<String, String>().apply { putAll(account?.defaultModels.orEmpty()) }
    }
    var saving by remember { mutableStateOf(false) }
    var notice by remember(account?.id) { mutableStateOf<String?>(null) }
    val models = app.models.filter { it.account == account?.id && it.id != "default" }
    Text("Default model preset", style = Type.headline)
    Text("Optional, for each account. Choose a model per level, then select Default in the composer. Only configured levels appear. Accounts never switch.", style = Type.small)
    Box {
        Pill(account?.name ?: "No account", enabled = !saving && app.accounts.isNotEmpty()) { expanded = true }
        DropdownMenu(expanded, onDismissRequest = { expanded = false }) {
            app.accounts.forEach { choice ->
                DropdownMenuItem(text = { Text(choice.name) }, onClick = { selected = choice.id; expanded = false })
            }
        }
    }
    if (account == null) return
    val levels = listOf("low" to "Low", "medium" to "Medium", "high" to "High", "xhigh" to "Extra high", "max" to "Max")
    Column(Modifier.fillMaxWidth().background(Palette.surface, RoundedCornerShape(12.dp))) {
        levels.forEach { (level, label) ->
            val id = draft[level]
            val model = models.firstOrNull { it.id == id }
            Column(Modifier.fillMaxWidth().clickable(enabled = !saving) { editing = level }.padding(horizontal = 14.dp, vertical = 10.dp)) {
                Text(label, style = Type.small)
                Text(model?.name ?: id?.let { "Unavailable · $it" } ?: "Not configured", style = Type.body)
                if (model != null) {
                    val effort = model.efforts.firstOrNull { it.value == level } ?: model.efforts.firstOrNull { it.value == model.defaultEffort }
                    Text(effort?.let { "Reasoning: ${it.label}" } ?: "No reasoning control", style = Type.small)
                }
            }
        }
    }
    Text("Each model keeps its context settings. If it does not support a level, its default reasoning is used. Leaving all levels unconfigured keeps the provider's usual behavior.", style = Type.small)
    Pill(if (saving) "Saving…" else "Save Default preset", primary = true, enabled = app.connected && !saving && draft.toMap() != account.defaultModels) {
        saving = true
        val accountID = account.id
        val values = draft.toMap()
        scope.launch {
            try { app.saveDefaultModels(accountID, values); notice = "Saved" }
            catch (cancelled: CancellationException) { throw cancelled }
            catch (failure: Exception) { notice = failure.message ?: "Could not save the preset." }
            finally { saving = false }
        }
    }
    notice?.let { Text(it, style = Type.small) }
    editing?.let { level ->
        var search by remember(level) { mutableStateOf("") }
        AlertDialog(
            onDismissRequest = { editing = null },
            title = { Text("${levels.first { it.first == level }.second} · ${account.name}") },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Field(search, { search = it }, "Search this account's models…", KeyboardType.Text)
                    Column(Modifier.heightIn(max = 340.dp).verticalScroll(rememberScrollState())) {
                        DropdownMenuItem(text = { Text("Not configured") }, onClick = { draft.remove(level); editing = null; notice = null })
                        models.filter { "${it.name} ${it.id}".contains(search.trim(), ignoreCase = true) }.forEach { model ->
                            DropdownMenuItem(text = { Column { Text(model.name); Text(model.id, style = Type.small) } }, onClick = {
                                draft[level] = model.id; editing = null; notice = null
                            })
                        }
                        if (models.isEmpty()) Text("Load this account's models first.", style = Type.small)
                    }
                }
            },
            confirmButton = { Pill("Close") { editing = null } },
            containerColor = Palette.surface,
            titleContentColor = Palette.text,
            textContentColor = Palette.text,
        )
    }
}
