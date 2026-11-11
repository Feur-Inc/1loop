// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Intent
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.core.net.toUri

/** The last run's crash (CrashReports.kt): its whole report, then what to do with it. */
@OptIn(ExperimentalLayoutApi::class)
@Composable
fun CrashReportDialog(crash: CrashReport, onDismiss: () -> Unit) {
    val context = LocalContext.current
    val report = remember(crash) { crash.report(context) }
    val (link, cut) = remember(report) { report.url() }
    fun copy() = context.getSystemService(ClipboardManager::class.java).setPrimaryClip(ClipData.newPlainText("1loop crash report", report.text))
    GlassBackdrop()
    AlertDialog(
        onDismissRequest = onDismiss,
        containerColor = GlassSurface, modifier = GlassDialogEdge,
        title = { Text("1loop closed unexpectedly", style = Type.title) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                Text("You can report it on GitHub: this opens a new issue in your browser with the report below, which you check and submit from your own account. 1loop sends nothing by itself. Paths, names, addresses, secrets and everything written in your chats were taken out.", style = Type.small)
                SelectionContainer(
                    Modifier.fillMaxWidth().heightIn(max = 260.dp).background(Palette.code, RoundedCornerShape(10.dp))
                        .verticalScroll(rememberScrollState()).padding(10.dp),
                ) { Text(report.text, style = Type.mono) }
                if (cut) Text("The trace is too long for a link: the issue gets its first lines. Copy report copies all of it.", style = Type.small)
                FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Pill("Copy report") { copy() }
                    Pill("Don't ask again") { CrashReports.setEnabled(context, false); onDismiss() }
                }
            }
        },
        dismissButton = { Pill("Not now") { onDismiss() } },
        confirmButton = {
            Pill("Report on GitHub", primary = true) {
                val opened = runCatching { context.startActivity(Intent(Intent.ACTION_VIEW, link.toUri())) }.isSuccess
                if (!opened) copy()
                onDismiss()
            }
        },
    )
}

/** Settings → Advanced: the offer after a crash, back on after Don't ask again. */
@Composable
fun CrashReportsSection() {
    val context = LocalContext.current
    var on by remember { mutableStateOf(CrashReports.enabled(context)) }
    Row(verticalAlignment = androidx.compose.ui.Alignment.CenterVertically) {
        Column(Modifier.weight(1f).padding(end = 12.dp), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Text("Offer to report crashes", style = Type.title)
            Text("After 1loop closes unexpectedly, the next start shows what happened, anonymized, and offers to open a GitHub issue with it that you check and submit from your own account. 1loop sends nothing by itself.", style = Type.small)
        }
        Toggle(on, enabled = true) { on = it; CrashReports.setEnabled(context, it) }
    }
}
