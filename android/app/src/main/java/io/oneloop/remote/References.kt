// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import android.content.Intent
import android.net.Uri
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.CancellationException
import org.json.JSONArray
import org.json.JSONObject

// An issue or pull request the conversation cites (#698, the desktop's
// references.go): its page, as on GitHub, read by the target with its
// GitHub sign-in (GET /api/git/github/item?detail=1): description, comments
// and reviews, checks, commits and each file's diff, so the user sees what
// the agent did. #12 is the chat's project's repository.

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ReferenceSheet(app: RemoteApp, project: String, ref: String, onDone: () -> Unit) {
    val context = LocalContext.current
    var page by remember(ref) { mutableStateOf<JSONObject?>(null) }
    var problem by remember(ref) { mutableStateOf<String?>(null) }
    LaunchedEffect(project, ref) {
        try {
            page = app.githubItem(project, ref)
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (failure: Exception) {
            problem = failure.message ?: "$ref could not be read."
        }
    }
    GlassBackdrop()
    ModalBottomSheet(onDismissRequest = onDone, containerColor = Palette.surface, scrimColor = GlassScrim, sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)) {
        Column(Modifier.verticalScroll(rememberScrollState()).padding(horizontal = 20.dp).padding(bottom = 24.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            val item = page
            if (item == null) {
                Text(ref, style = Type.title)
                problem?.let { Text(it, style = Type.small.copy(color = Palette.danger)) } ?: WorkRing("wait", 28.dp)
                return@Column
            }
            val pull = item.optBoolean("pull")
            val refObject = item.optJSONObject("ref")
            val name = refObject?.let { "${it.optString("owner")}/${it.optString("name")}#${it.optInt("number")}" } ?: ref
            Text(item.optString("title"), style = Type.title)
            val state = item.optString("state")
            Text(
                listOf(name, (if (pull) "Pull request " else "Issue ") + state, item.optString("author").takeIf { it.isNotEmpty() }?.let { "by @$it" }.orEmpty())
                    .filter { it.isNotEmpty() }.joinToString(" · "),
                style = Type.small.copy(color = when (state) { "open" -> Palette.added; "merged" -> Palette.amber; else -> Palette.muted }),
            )
            itemStrings(item.optJSONArray("labels")).takeIf { it.isNotEmpty() }?.let { Text(it.joinToString(" · "), style = Type.caption) }
            if (pull) Text(
                "${item.optString("head")} → ${item.optString("base")} · +${item.optInt("additions")} −${item.optInt("deletions")} · ${item.optInt("files")} files · ${item.optInt("commits")} commits",
                style = Type.caption,
            )
            item.optString("url").takeIf { it.isNotEmpty() }?.let { url ->
                Pill("Open on GitHub", icon = Icons.globe) {
                    runCatching { context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }
                }
            }
            item.optString("body").takeIf { it.isNotBlank() }?.let { Markdown(it) }
            itemObjects(item.optJSONArray("checks")).takeIf { it.isNotEmpty() }?.let { checks ->
                PageHeading("Checks")
                for (check in checks) {
                    val outcome = check.optString("conclusion").ifEmpty { check.optString("status").replace('_', ' ') }
                    Row {
                        Text(check.optString("name"), style = Type.small.copy(color = Palette.text), modifier = Modifier.weight(1f))
                        Text(outcome, style = Type.small.copy(color = when (outcome) { "success" -> Palette.added; "failure", "cancelled", "timed_out" -> Palette.danger; else -> Palette.muted }))
                    }
                }
            }
            itemObjects(item.optJSONArray("commitList")).takeIf { it.isNotEmpty() }?.let { commits ->
                PageHeading("Commits")
                for (commit in commits) Text("${commit.optString("sha").take(7)}  ${commit.optString("title")}", style = Type.small.copy(color = Palette.text))
            }
            itemObjects(item.optJSONArray("fileList")).takeIf { it.isNotEmpty() }?.let { files ->
                PageHeading("Files")
                for (file in files) {
                    val title = file.optString("name") + "  +${file.optInt("additions")} −${file.optInt("deletions")}"
                    val patch = file.optString("patch")
                    if (patch.isNotEmpty()) CodeWell(patch, title, diff = true)
                    else Text("$title · ${file.optString("status")}", style = Type.small)
                }
            }
            itemObjects(item.optJSONArray("posts")).takeIf { it.isNotEmpty() }?.let { posts ->
                PageHeading("Conversation")
                for (post in posts) Column(
                    Modifier.fillMaxWidth().background(Palette.code, RoundedCornerShape(10.dp)).border(1.dp, Palette.borderSoft, RoundedCornerShape(10.dp)).padding(12.dp),
                    verticalArrangement = Arrangement.spacedBy(4.dp),
                ) {
                    val about = when (post.optString("kind")) {
                        "review" -> "reviewed" + post.optString("state").takeIf { it.isNotEmpty() }?.let { ": $it" }.orEmpty()
                        "line" -> "on ${post.optString("path")}:${post.optInt("line")}"
                        else -> "commented"
                    }
                    Text("@${post.optString("author")} $about", style = Type.caption.copy(color = Palette.muted))
                    post.optString("body").takeIf { it.isNotBlank() }?.let { Markdown(it) }
                }
            }
        }
    }
}

@Composable
private fun PageHeading(text: String) {
    Text(text, style = Type.body.copy(fontWeight = FontWeight.Medium), modifier = Modifier.padding(top = 6.dp))
}

private fun itemObjects(array: JSONArray?) = (0 until (array?.length() ?: 0)).map { array!!.getJSONObject(it) }

private fun itemStrings(array: JSONArray?) = (0 until (array?.length() ?: 0)).map { array!!.optString(it) }
