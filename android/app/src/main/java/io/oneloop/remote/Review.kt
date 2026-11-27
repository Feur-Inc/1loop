// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.CancellationException
import org.json.JSONObject

// An edit waiting for approval, reviewed before it is approved (#690, the
// desktop's #242): each file's text after the edit, to change before
// approving, or to refuse file by file; a note goes to the agent with a
// refusal. The files come from GET /api/review, the answer goes with
// POST /api/approve as agent.ApprovalAnswer.

/** A file the edit would change (agent.ReviewFile). */
data class ReviewFile(val path: String, val before: String, val after: String, val op: String, val from: String, val fixed: Boolean) {
    companion object {
        fun from(o: JSONObject) = ReviewFile(
            o.optString("path"), o.optString("before"), o.optString("after"), o.optString("op"), o.optString("from"), o.optBoolean("fixed"),
        )
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ReviewSheet(app: RemoteApp, approval: Approval, onBack: () -> Unit) {
    var files by remember(approval.id) { mutableStateOf<List<ReviewFile>?>(null) }
    var problem by remember(approval.id) { mutableStateOf<String?>(null) }
    val edited = remember(approval.id) { mutableStateMapOf<String, String>() } // by path: the text as the user left it
    val refused = remember(approval.id) { mutableStateMapOf<String, Boolean>() }
    var note by remember(approval.id) { mutableStateOf("") }
    LaunchedEffect(approval.id) {
        try {
            files = app.review(approval)
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (failure: Exception) {
            problem = failure.message ?: "The edit could not be read."
        }
    }
    GlassBackdrop()
    ModalBottomSheet(
        onDismissRequest = onBack, containerColor = Palette.surface, scrimColor = GlassScrim,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
    ) {
        Column(
            Modifier.verticalScroll(rememberScrollState()).padding(horizontal = 20.dp).padding(bottom = 24.dp).imePadding(),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            Text("Review the edit", style = Type.title)
            Text(approval.title, style = Type.caption.copy(color = Palette.muted), maxLines = 2, overflow = TextOverflow.Ellipsis)
            problem?.let { Text(it, style = Type.small.copy(color = Palette.danger)) }
            val list = files
            if (list == null && problem == null) WorkRing("wait", 28.dp)
            list?.forEach { file ->
                val keep = refused[file.path] != true
                Column(
                    Modifier.fillMaxWidth().background(Palette.code, RoundedCornerShape(12.dp))
                        .border(1.dp, Palette.borderSoft, RoundedCornerShape(12.dp)).padding(12.dp),
                    verticalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Column(Modifier.weight(1f)) {
                            Text(file.path, style = Type.mono, maxLines = 2, overflow = TextOverflow.Ellipsis)
                            Text(
                                when (file.op) {
                                    "add" -> "New file"
                                    "delete" -> "Deleted"
                                    else -> if (file.from.isNotEmpty()) "Moved from ${file.from}" else "Changed"
                                },
                                style = Type.caption,
                            )
                        }
                        Spacer(Modifier.width(10.dp))
                        Toggle(keep, enabled = true) { refused[file.path] = !it }
                    }
                    when {
                        !keep -> Text("Refused: this file stays as it is.", style = Type.small)
                        file.op == "delete" -> Text("The file is deleted.", style = Type.small)
                        file.fixed -> Text(file.after, style = Type.mono, maxLines = 40, overflow = TextOverflow.Ellipsis)
                        else -> {
                            Text("After the edit: change it before you approve.", style = Type.small)
                            BasicTextField(
                                edited[file.path] ?: file.after, { edited[file.path] = it },
                                Modifier.fillMaxWidth().heightIn(min = 120.dp, max = 420.dp).background(Palette.background, RoundedCornerShape(8.dp))
                                    .border(1.dp, Palette.border, RoundedCornerShape(8.dp)).horizontalScroll(rememberScrollState()).padding(10.dp),
                                textStyle = Type.mono, cursorBrush = SolidColor(Palette.amber),
                            )
                        }
                    }
                }
            }
            Text("A note to the agent, sent with a refusal", style = Type.small)
            Field(note, { note = it }, "Why, or what to do instead", androidx.compose.ui.text.input.KeyboardType.Text)
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(10.dp, Alignment.End)) {
                Pill("Back", onClick = onBack)
                Pill("Deny") { app.answer(approval, false, note = note) }
                Pill("Allow", primary = true, enabled = list != null) {
                    val changed = list.orEmpty().filter { refused[it.path] != true && edited[it.path] != null && edited[it.path] != it.after }
                    app.answer(
                        approval, true,
                        files = changed.associate { it.path to edited[it.path]!! },
                        denied = list.orEmpty().filter { refused[it.path] == true }.map { it.path },
                        note = note,
                    )
                }
            }
        }
    }
}
