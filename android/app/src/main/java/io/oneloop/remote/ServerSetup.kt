// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.net.Uri
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import org.json.JSONObject

// Add a server, step by step (#443): it asks first whether a server already
// runs. One that does is added by its address; otherwise the user picks how
// to run one (Docker, Docker Compose, the Linux or Windows program) and
// follows a short guide, each command with its Copy button, then adds it.
// The guides are the desktop's: internal/serversetup in Go, copied into
// assets/server_setup.json (its test keeps the copy in step).

data class ServerGuideLink(val label: String, val url: String)
data class ServerGuideStep(val text: String, val command: String, val link: ServerGuideLink?)
data class ServerGuideMethod(val id: String, val title: String, val detail: String, val steps: List<ServerGuideStep>)

/** The guides, from assets/server_setup.json. */
fun serverSetupMethods(json: String): List<ServerGuideMethod> {
    val methods = JSONObject(json).getJSONArray("methods")
    return (0 until methods.length()).map { i ->
        val method = methods.getJSONObject(i)
        val steps = method.getJSONArray("steps")
        ServerGuideMethod(
            method.getString("id"), method.getString("title"), method.getString("detail"),
            (0 until steps.length()).map { j ->
                val step = steps.getJSONObject(j)
                val link = step.optJSONObject("link")
                ServerGuideStep(step.getString("text"), step.optString("command"), link?.let { ServerGuideLink(it.getString("label"), it.getString("url")) })
            },
        )
    }
}

private fun loadServerSetup(context: Context): List<ServerGuideMethod> =
    serverSetupMethods(context.assets.open("server_setup.json").bufferedReader().use { it.readText() })

/** The setup's pages: does a server run, how to run one, its guide. */
private sealed interface SetupPage {
    data object Ask : SetupPage
    data object Methods : SetupPage
    data class Guide(val method: ServerGuideMethod) : SetupPage
}

/** Add a server: onAddress opens the address sheet, once a server runs. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ServerSetupSheet(onDone: () -> Unit, onAddress: () -> Unit) {
    val context = LocalContext.current
    val methods = remember { loadServerSetup(context) }
    var page by remember { mutableStateOf<SetupPage>(SetupPage.Ask) }
    GlassBackdrop()
    ModalBottomSheet(onDismissRequest = onDone, containerColor = Palette.surface, scrimColor = GlassScrim, sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)) {
        Column(Modifier.padding(horizontal = 24.dp).padding(bottom = 24.dp).verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            when (val shown = page) {
                SetupPage.Ask -> {
                    Text("Add a server", style = Type.title)
                    Text("A 1loop server runs the agent and your projects on another machine. Does one run already?", style = Type.small)
                    Spacer(Modifier.height(4.dp))
                    SetupChoice("I have a server", "Add it by its address.") { onDone(); onAddress() }
                    SetupChoice("Set one up", "With Docker, or the server program for Linux or Windows.") { page = SetupPage.Methods }
                }
                SetupPage.Methods -> {
                    Text("How do you want to run it?", style = Type.title)
                    Spacer(Modifier.height(4.dp))
                    for (method in methods) SetupChoice(method.title, method.detail) { page = SetupPage.Guide(method) }
                    Row(Modifier.fillMaxWidth().padding(top = 6.dp)) { Pill("Back") { page = SetupPage.Ask } }
                }
                is SetupPage.Guide -> {
                    Text(shown.method.title, style = Type.title)
                    shown.method.steps.forEachIndexed { i, step -> GuideStep(context, i + 1, step) }
                    Row(Modifier.fillMaxWidth().padding(top = 6.dp), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                        Pill("Back") { page = SetupPage.Methods }
                        Spacer(Modifier.weight(1f))
                        Pill("It runs: add it", primary = true) { onDone(); onAddress() }
                    }
                }
            }
        }
    }
}

/** A card to tap: a title, a line under it and a chevron. */
@Composable
private fun SetupChoice(title: String, detail: String, onClick: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(14.dp)).background(Palette.raised)
            .border(1.dp, Palette.borderSoft, RoundedCornerShape(14.dp)).clickable(onClick = onClick)
            .padding(horizontal = 16.dp, vertical = 14.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f)) {
            Text(title, style = Type.body.copy(fontWeight = FontWeight.Medium))
            Text(detail, style = Type.caption.copy(color = Palette.muted))
        }
        Icon(Icons.chevronRight, null, tint = Palette.faint, modifier = Modifier.size(18.dp))
    }
}

/** One step of a guide: its sentence, then its command with Copy, or its page to open. */
@Composable
private fun GuideStep(context: Context, number: Int, step: ServerGuideStep) {
    Row(Modifier.fillMaxWidth().padding(top = 6.dp)) {
        Text("$number.", style = Type.body.copy(color = Palette.amber), modifier = Modifier.width(24.dp))
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(step.text, style = Type.body)
            if (step.command.isNotEmpty()) {
                Text(
                    step.command, style = Type.mono,
                    // The whole command, wrapped: a phone shows it all without scrolling sideways.
                    modifier = Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).background(Palette.code)
                        .padding(horizontal = 12.dp, vertical = 10.dp),
                )
                var copied by remember { mutableStateOf(false) }
                Pill(if (copied) "Copied" else "Copy", icon = Icons.copy) {
                    context.getSystemService(ClipboardManager::class.java).setPrimaryClip(ClipData.newPlainText("1loop server setup", step.command))
                    copied = true
                }
            }
            step.link?.let { link ->
                Pill(link.label, icon = Icons.globe) {
                    runCatching { context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(link.url))) }
                }
            }
        }
    }
}
