package io.oneloop.remote

// Worktrees: a Git project's other checkouts show under it in the drawer,
// each with its own chats, and the Repository screen lists its worktrees,
// branches, pull requests and issues. The target reads Git (/api/git).

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch
import org.json.JSONArray
import org.json.JSONObject

internal fun stringMap(json: JSONObject?): Map<String, String> =
    json?.keys()?.asSequence()?.associateWith { json.optString(it) } ?: emptyMap()

data class Worktree(
    val path: String, val branch: String, val main: Boolean, val missing: Boolean, val changes: Int,
    val ahead: Int, val behind: Int, val upstream: String, val subject: String, val updated: String,
) {
    val label get() = branch.ifEmpty { projectName(path) + " (detached)" }
}

data class GitBranch(val name: String, val remote: Boolean, val subject: String, val updated: String, val worktree: String, val ahead: Int, val behind: Int, val upstream: String)

data class PullRequest(
    val number: Int, val title: String, val url: String, val author: String, val branch: String, val base: String,
    val fork: Boolean, val draft: Boolean, val checks: String, val review: String, val comments: Int, val updated: String,
)

data class GitIssue(val number: Int, val title: String, val url: String, val author: String, val labels: List<String>, val comments: Int, val updated: String)

/** What the app knows of a project's repository. */
data class Repository(
    val loaded: Boolean = false,
    val at: Long = 0, // when Git and GitHub were last read (uptime ms)
    val hubAt: Long = 0,
    val error: String? = null,
    val current: String = "",
    val defaultBranch: String = "",
    val owner: String = "",
    val name: String = "",
    val remote: String = "",
    val worktrees: List<Worktree> = emptyList(),
    val branches: List<GitBranch> = emptyList(),
    val hubLoaded: Boolean = false,
    val hubError: String? = null,
    val signed: Boolean = false,
    val description: String = "",
    val pulls: List<PullRequest> = emptyList(),
    val issues: List<GitIssue> = emptyList(),
    val worktreePulls: Map<String, Int> = emptyMap(),
)

private fun <T> JSONArray?.list(item: (JSONObject) -> T): List<T> = (0 until (this?.length() ?: 0)).map { item(this!!.getJSONObject(it)) }

private fun Repository.withGit(json: JSONObject) = copy(
    loaded = true, error = null,
    current = json.optString("current"), defaultBranch = json.optString("defaultBranch"),
    owner = json.optString("owner"), name = json.optString("name"), remote = json.optString("remote"),
    worktrees = json.optJSONArray("worktrees").list {
        Worktree(it.optString("path"), it.optString("branch"), it.optBoolean("main"), it.optBoolean("missing"), it.optInt("changes"),
            it.optInt("ahead"), it.optInt("behind"), it.optString("upstream"), it.optString("subject"), it.optString("updated"))
    },
    branches = json.optJSONArray("branches").list {
        GitBranch(it.optString("name"), it.optBoolean("remote"), it.optString("subject"), it.optString("updated"), it.optString("worktree"),
            it.optInt("ahead"), it.optInt("behind"), it.optString("upstream"))
    },
)

private fun Repository.withHub(json: JSONObject): Repository {
    val hub = json.optJSONObject("hub") ?: JSONObject()
    return copy(
        hubLoaded = true, hubError = null, signed = hub.optBoolean("signed"), description = hub.optString("description"),
        pulls = hub.optJSONArray("pulls").list {
            PullRequest(it.optInt("number"), it.optString("title"), it.optString("url"), it.optString("author"), it.optString("branch"), it.optString("base"),
                it.optBoolean("fork"), it.optBoolean("draft"), it.optString("checks"), it.optString("review"), it.optInt("comments"), it.optString("updated"))
        },
        issues = hub.optJSONArray("issues").list { issue ->
            GitIssue(issue.optInt("number"), issue.optString("title"), issue.optString("url"), issue.optString("author"),
                issue.optJSONArray("labels").let { labels -> (0 until (labels?.length() ?: 0)).map { labels!!.optString(it) } },
                issue.optInt("comments"), issue.optString("updated"))
        },
        worktreePulls = json.optJSONObject("worktreePulls")?.let { pulls -> pulls.keys().asSequence().associateWith { pulls.optInt(it) } } ?: emptyMap(),
    )
}

private fun query(project: String) = "project=" + java.net.URLEncoder.encode(project, "UTF-8")

/** The project a folder belongs to: itself, or the project of the worktree it is. */
fun RemoteApp.projectOf(folder: String?): String? = folder?.let { worktrees[it] ?: it }

/** Reads a project's repository again when older than fresh (ms), and GitHub's side when hub. */
suspend fun RemoteApp.loadRepository(project: String, hub: Boolean, fresh: Long = 0) {
    val known = repositories[project] ?: Repository()
    val now = android.os.SystemClock.uptimeMillis()
    val hubFresh = if (fresh == 0L) 0L else maxOf(fresh, 60_000L) // GitHub less often than Git
    if (known.loaded && now - known.at < fresh && (!hub || now - known.hubAt < hubFresh)) return
    val read = try {
        known.withGit(extensionsCall("/api/git?" + query(project))).copy(at = now)
    } catch (cancelled: CancellationException) {
        throw cancelled
    } catch (failure: Exception) {
        repositories[project] = known.copy(loaded = true, error = failure.message ?: "Could not read the repository.")
        return
    }
    repositories[project] = read
    val found = read.worktrees.filter { !it.main }.associate { it.path to project }
    if (found.any { worktrees[it.key] != project }) worktrees = worktrees + found
    if (!hub || read.owner.isEmpty() || known.hubLoaded && now - known.hubAt < hubFresh) return
    repositories[project] = try {
        (repositories[project] ?: read).withHub(extensionsCall("/api/git/github?" + query(project))).copy(hubAt = now)
    } catch (cancelled: CancellationException) {
        throw cancelled
    } catch (failure: Exception) {
        (repositories[project] ?: read).copy(hubLoaded = true, hubAt = now, hubError = failure.message ?: "Could not read GitHub.")
    }
}

/** Makes a worktree, opens it on a new chat, with draft in the composer when given. */
suspend fun RemoteApp.createWorktree(project: String, request: JSONObject, draft: String? = null) {
    val reply = extensionsCall("/api/git/worktrees", request.put("project", project), timeout = 120000)
    val path = reply.getString("path")
    worktrees = worktrees + (path to project)
    selectProject(path)
    if (draft != null) server?.let { drafts.restore("${it.key}|new:$path", draft) }
    loadRepository(project, true)
}

suspend fun RemoteApp.removeWorktree(project: String, worktree: Worktree, deleteBranch: Boolean): String {
    val body = JSONObject().put("project", project).put("path", worktree.path).put("force", worktree.changes > 0).put("deleteBranch", deleteBranch)
    val reply = extensionsCall("/api/git/worktrees/remove", body, timeout = 60000)
    worktrees = worktrees - worktree.path
    if (selectedProject == worktree.path) selectProject(project)
    loadRepository(project, false)
    return reply.optString("note")
}

private val sortNames = listOf("recent" to "Recent", "name" to "Name", "pull" to "Pull request")

fun sortedWorktrees(repo: Repository, by: String): List<Worktree> = repo.worktrees.sortedWith(
    compareBy<Worktree> { !it.main }.thenComparator { a, b ->
        when (by) {
            "name" -> a.label.lowercase().compareTo(b.label.lowercase())
            "pull" -> {
                val pa = repo.worktreePulls[a.path] ?: 0
                val pb = repo.worktreePulls[b.path] ?: 0
                if ((pa > 0) != (pb > 0)) (if (pa > 0) -1 else 1) else if (pa != pb) pb.compareTo(pa) else b.updated.compareTo(a.updated)
            }
            else -> b.updated.compareTo(a.updated)
        }
    },
)

/** A Git project's worktrees in the drawer, each with its chats when chosen. */
@Composable
fun WorktreeEntries(app: RemoteApp, project: String, chats: @Composable (String) -> Unit, onActions: (Worktree) -> Unit, onNew: () -> Unit) {
    LaunchedEffect(app.server, project) { app.loadRepository(project, true, fresh = 20_000) }
    val repo = app.repositories[project] ?: return
    if (repo.error != null) return
    for (worktree in sortedWorktrees(repo, app.worktreeSort)) {
        if (worktree.main) continue
        val selected = app.selectedProject == worktree.path
        val running = app.chats.any { it.workspace == worktree.path && it.running }
        NavRow(
            worktree.label, Icons.branch, selected = selected, running = running,
            onLongClick = { onActions(worktree) }, longClickLabel = "Worktree actions",
            trailing = repo.worktreePulls[worktree.path]?.let { "#$it" } ?: if (worktree.missing) "missing" else null,
        ) { app.selectProject(worktree.path) }
        if (selected) Column(Modifier.padding(start = 16.dp)) { chats(worktree.path) }
    }
    NavRow("New worktree", Icons.plus, selected = false, onClick = onNew)
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun NewWorktreeSheet(app: RemoteApp, project: String, onDone: () -> Unit) {
    val repo = app.repositories[project] ?: Repository()
    var branch by remember { mutableStateOf("") }
    var base by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var problem by remember { mutableStateOf<String?>(null) }
    val scope = rememberCoroutineScope()
    ModalBottomSheet(onDismissRequest = { if (!busy) onDone() }, containerColor = Palette.surface, sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)) {
        Column(Modifier.verticalScroll(rememberScrollState()).padding(24.dp).padding(bottom = 24.dp), verticalArrangement = Arrangement.spacedBy(14.dp)) {
            Text("New worktree · ${projectName(project)}", style = Type.title)
            Text("A new branch, checked out in its own folder of the project (.worktrees/), with its own chats. The project's folder keeps its branch.", style = Type.small)
            Field(branch, { branch = it }, "feature/my-change", KeyboardType.Text)
            Text("Starts from", style = Type.small)
            Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Choice((repo.defaultBranch.ifEmpty { "Default branch" }) + " (latest)", base.isEmpty()) { base = "" }
                for (other in repo.branches.filter { it.name != repo.defaultBranch }.take(12)) Choice(other.name, base == other.name) { base = other.name }
            }
            problem?.let { Text(it, style = Type.small.copy(color = Palette.danger)) }
            Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                Pill("Cancel", enabled = !busy, onClick = onDone)
                Pill(if (busy) "Creating…" else "Create", primary = true, enabled = !busy && branch.isNotBlank()) {
                    busy = true
                    problem = null
                    scope.launch {
                        try {
                            val request = JSONObject().put("kind", "new").put("branch", branch.trim())
                            if (base.isNotEmpty()) request.put("base", base)
                            app.createWorktree(project, request)
                            onDone()
                        } catch (cancelled: CancellationException) {
                            throw cancelled
                        } catch (failure: Exception) {
                            problem = failure.message ?: "Could not create the worktree."
                        } finally {
                            busy = false
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun Choice(text: String, selected: Boolean, onClick: () -> Unit) {
    Text(
        text, style = Type.small.copy(color = if (selected) Palette.text else Palette.muted), maxLines = 1,
        modifier = Modifier.clip(RoundedCornerShape(8.dp)).background(if (selected) Palette.selected else Color.Transparent)
            .clickable(onClick = onClick).padding(horizontal = 10.dp, vertical = 7.dp),
    )
}

/** A worktree's actions; removing it reads its changes again first. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun WorktreeSheet(app: RemoteApp, project: String, start: Worktree, onDone: () -> Unit) {
    val context = LocalContext.current
    var worktree by remember { mutableStateOf(start) }
    var removing by remember { mutableStateOf(false) }
    var deleteBranch by remember { mutableStateOf(false) }
    var busy by remember { mutableStateOf(false) }
    var problem by remember { mutableStateOf<String?>(null) }
    val scope = rememberCoroutineScope()
    val repo = app.repositories[project] ?: Repository()
    val pull = repo.worktreePulls[worktree.path]?.let { number -> repo.pulls.firstOrNull { it.number == number } }
    fun open(address: String) = runCatching { context.startActivity(android.content.Intent(android.content.Intent.ACTION_VIEW, android.net.Uri.parse(address))) }
    ModalBottomSheet(onDismissRequest = { if (!busy) onDone() }, containerColor = Palette.surface, sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)) {
        Column(Modifier.verticalScroll(rememberScrollState()).padding(24.dp).padding(bottom = 24.dp), verticalArrangement = Arrangement.spacedBy(14.dp)) {
            Text(if (removing) "Remove worktree ${worktree.label}?" else worktree.label, style = Type.title)
            Text(worktree.path, style = Type.small)
            if (!removing) {
                Pill("New chat", icon = Icons.plus) { app.selectProject(worktree.path); app.open(null); onDone() }
                if (pull != null) Pill("Open pull request #${pull.number}", icon = Icons.globe) { open(pull.url) }
                else if (repo.owner.isNotEmpty() && worktree.branch.isNotEmpty()) Pill("Create a pull request on GitHub", icon = Icons.globe) {
                    open("https://github.com/${repo.owner}/${repo.name}/compare/${android.net.Uri.encode(worktree.branch)}?expand=1")
                }
                Pill("Repository", icon = Icons.branch) { app.repositoryOpen = project; onDone() }
                Pill("Remove worktree…", icon = Icons.trash) {
                    busy = true
                    scope.launch {
                        try {
                            app.loadRepository(project, false)
                            app.repositories[project]?.worktrees?.firstOrNull { it.path == worktree.path }?.let { worktree = it }
                            removing = true
                        } finally {
                            busy = false
                        }
                    }
                }
            } else {
                val chats = app.chats.count { it.workspace == worktree.path }
                Text(
                    buildString {
                        append("The folder will be deleted.")
                        if (chats > 0) append(" Its $chats chat(s) will be removed from 1loop.")
                        if (worktree.changes > 0) append(" It has ${worktree.changes} uncommitted change(s): they will be lost.")
                    },
                    style = Type.body,
                )
                if (worktree.branch.isNotEmpty()) Row(verticalAlignment = Alignment.CenterVertically) {
                    Text("Also delete the branch ${worktree.branch} (only if merged, unless changes are discarded)", style = Type.small, modifier = Modifier.weight(1f))
                    Toggle(deleteBranch, enabled = !busy) { deleteBranch = it }
                }
                Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                    Pill("Cancel", enabled = !busy, onClick = onDone)
                    Pill(if (busy) "Removing…" else "Remove", primary = true, enabled = !busy) {
                        busy = true
                        problem = null
                        scope.launch {
                            try {
                                val note = app.removeWorktree(project, worktree, deleteBranch)
                                if (note.isEmpty()) onDone() else problem = note
                            } catch (cancelled: CancellationException) {
                                throw cancelled
                            } catch (failure: Exception) {
                                problem = failure.message ?: "Could not remove the worktree."
                            } finally {
                                busy = false
                            }
                        }
                    }
                }
            }
            problem?.let { Text(it, style = Type.small.copy(color = Palette.danger)) }
        }
    }
}

/** The Repository screen: worktrees, branches, pull requests and issues. */
@Composable
fun RepositoryScreen(app: RemoteApp, project: String, onBack: () -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var tab by remember { mutableStateOf("worktrees") }
    var busy by remember { mutableStateOf<String?>(null) }
    var problem by remember { mutableStateOf<String?>(null) }
    var creating by remember { mutableStateOf(false) }
    var actions by remember { mutableStateOf<Worktree?>(null) }
    LaunchedEffect(app.server, project) { app.loadRepository(project, true) }
    val repo = app.repositories[project]
    fun open(address: String) = runCatching { context.startActivity(android.content.Intent(android.content.Intent.ACTION_VIEW, android.net.Uri.parse(address))) }
    fun go(folder: String) { app.selectProject(folder); onBack() }
    fun create(label: String, request: JSONObject, draft: String? = null) {
        busy = label
        problem = null
        scope.launch {
            try {
                app.createWorktree(project, request, draft)
                onBack()
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (failure: Exception) {
                problem = failure.message ?: "Could not create the worktree."
            } finally {
                busy = null
            }
        }
    }
    Column(Modifier.fillMaxSize().background(Palette.background).windowInsetsPadding(WindowInsets.safeDrawing)) {
        Row(Modifier.padding(12.dp), verticalAlignment = Alignment.CenterVertically) {
            RoundButton(Icons.back, Color.Transparent, Palette.muted, description = "Back", onClick = onBack)
            Text("Repository · ${projectName(project)}", style = Type.title, modifier = Modifier.padding(start = 12.dp).weight(1f))
            RoundButton(Icons.refresh, Color.Transparent, Palette.muted, description = "Refresh") { scope.launch { app.loadRepository(project, true) } }
        }
        if (repo == null || !repo.loaded) {
            Text("Reading the repository…", style = Type.small, modifier = Modifier.padding(20.dp))
            return@Column
        }
        if (repo.error != null) {
            Text(repo.error, style = Type.body, modifier = Modifier.padding(20.dp))
            return@Column
        }
        Column(Modifier.padding(horizontal = 20.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text(
                listOfNotNull(if (repo.owner.isNotEmpty()) "${repo.owner}/${repo.name}" else projectName(project),
                    repo.current.takeIf { it.isNotEmpty() }?.let { "on $it" }, repo.defaultBranch.takeIf { it.isNotEmpty() }?.let { "default $it" }).joinToString(" · "),
                style = Type.body,
            )
            if (repo.description.isNotEmpty()) Text(repo.description, style = Type.small)
        }
        Row(Modifier.horizontalScroll(rememberScrollState()).padding(horizontal = 16.dp, vertical = 10.dp), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
            Choice("Worktrees ${repo.worktrees.size}", tab == "worktrees") { tab = "worktrees" }
            Choice("Branches ${repo.branches.size}", tab == "branches") { tab = "branches" }
            Choice("Pull requests" + if (repo.hubLoaded) " ${repo.pulls.size}" else "", tab == "pulls") { tab = "pulls" }
            Choice("Issues" + if (repo.hubLoaded) " ${repo.issues.size}" else "", tab == "issues") { tab = "issues" }
        }
        busy?.let {
            Row(Modifier.padding(horizontal = 20.dp, vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                WorkRing("wait", 22.dp)
                Text(it, style = Type.small, modifier = Modifier.padding(start = 10.dp))
            }
        }
        problem?.let { Text(it, style = Type.small.copy(color = Palette.danger), modifier = Modifier.padding(horizontal = 20.dp, vertical = 4.dp)) }
        LazyColumn(Modifier.weight(1f), contentPadding = PaddingValues(20.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            fun hubNotice(): Boolean {
                val text = when {
                    repo.owner.isEmpty() -> "This repository has no GitHub remote named origin."
                    !repo.hubLoaded -> "Reading GitHub…"
                    repo.hubError != null -> repo.hubError
                    else -> null
                }
                if (text != null) item { Text(text, style = Type.small) }
                else if (!repo.signed) item { Text("Read without a GitHub account: checks are unknown and private repositories are hidden. Connect GitHub in Settings → Connectors.", style = Type.small) }
                return text != null
            }
            when (tab) {
                "worktrees" -> {
                    item {
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            Text("Sort by", style = Type.small)
                            for ((id, label) in sortNames) Choice(label, app.worktreeSort == id) { app.chooseWorktreeSort(id) }
                        }
                    }
                    item { Pill("New worktree", primary = true, icon = Icons.plus, enabled = busy == null) { creating = true } }
                    items(sortedWorktrees(repo, app.worktreeSort)) { worktree ->
                        val pull = repo.worktreePulls[worktree.path]?.let { n -> repo.pulls.firstOrNull { it.number == n } }
                        Entry(
                            worktree.label + if (worktree.main) " · the project's folder" else "",
                            listOfNotNull(
                                when { worktree.missing -> "folder missing"; worktree.changes > 0 -> "${worktree.changes} uncommitted change(s)"; else -> "clean" },
                                if (worktree.ahead > 0 || worktree.behind > 0) "↑${worktree.ahead} ↓${worktree.behind} ${worktree.upstream}" else null,
                                worktree.subject.takeIf { it.isNotEmpty() }?.let { "“$it” " + ago(worktree.updated) },
                                pull?.let { pullLine(it) },
                            ).joinToString(" · "),
                        ) {
                            Pill("Open") { go(worktree.path) }
                            Pill("New chat") { app.selectProject(worktree.path); app.open(null); onBack() }
                            if (!worktree.main) Pill("More") { actions = worktree }
                        }
                    }
                }
                "branches" -> items(repo.branches) { branch ->
                    Entry(
                        branch.name,
                        listOfNotNull(
                            if (branch.remote) "remote branch" else null,
                            branch.subject.takeIf { it.isNotEmpty() }?.let { "“$it” " + ago(branch.updated) },
                            if (branch.ahead > 0 || branch.behind > 0) "↑${branch.ahead} ↓${branch.behind} ${branch.upstream}" else null,
                            branch.worktree.takeIf { it.isNotEmpty() }?.let { "checked out in $it" },
                        ).joinToString(" · "),
                    ) {
                        if (branch.worktree.isNotEmpty()) Pill("Open") { go(branch.worktree) }
                        else Pill("Open in a worktree", enabled = busy == null) { create("Creating the worktree…", JSONObject().put("kind", "branch").put("branch", branch.name)) }
                    }
                }
                "pulls" -> if (!hubNotice()) {
                    val worktreeOf = repo.worktreePulls.entries.associate { it.value to it.key }
                    items(repo.pulls) { pull ->
                        Entry("#${pull.number} ${pull.title}", listOfNotNull(
                            "${pull.branch} → ${pull.base}", pull.author.takeIf { it.isNotEmpty() }?.let { "@$it" },
                            if (pull.draft) "draft" else null, if (pull.fork) "from a fork" else null,
                            when (pull.checks) { "success" -> "✓ checks passed"; "failure" -> "✗ checks failing"; "pending" -> "● checks running"; else -> null },
                            pull.review.takeIf { it.isNotEmpty() }, "${pull.comments} comment(s)", "updated " + ago(pull.updated),
                        ).joinToString(" · ")) {
                            val folder = worktreeOf[pull.number]
                            if (folder != null) Pill("Open") { go(folder) }
                            else Pill("Check out in a worktree", enabled = busy == null) {
                                create("Fetching the pull request…", JSONObject().put("kind", "pull").put("pull", pull.number).put("pullBranch", pull.branch).put("fork", pull.fork))
                            }
                            Pill("View") { open(pull.url) }
                        }
                    }
                    if (repo.pulls.isEmpty()) item { Text("No open pull requests.", style = Type.small) }
                }
                else -> if (!hubNotice()) {
                    items(repo.issues) { issue ->
                        Entry("#${issue.number} ${issue.title}", listOfNotNull(
                            issue.labels.takeIf { it.isNotEmpty() }?.joinToString(", "), issue.author.takeIf { it.isNotEmpty() }?.let { "@$it" },
                            "${issue.comments} comment(s)", "updated " + ago(issue.updated),
                        ).joinToString(" · ")) {
                            Pill("Start a worktree", enabled = busy == null) {
                                create("Creating the worktree…", JSONObject().put("kind", "new").put("branch", issueBranch(issue.number, issue.title)),
                                    "Work on issue #${issue.number}: ${issue.title}\n${issue.url}")
                            }
                            Pill("View") { open(issue.url) }
                        }
                    }
                    if (repo.issues.isEmpty()) item { Text("No open issues.", style = Type.small) }
                }
            }
        }
    }
    if (creating) NewWorktreeSheet(app, project) { creating = false; if (app.selectedProject != project && app.worktrees[app.selectedProject] == project) onBack() }
    actions?.let { worktree -> WorktreeSheet(app, project, worktree) { actions = null } }
}

@Composable
private fun Entry(title: String, detail: String, buttons: @Composable RowScope.() -> Unit) {
    Column(Modifier.fillMaxWidth().clip(RoundedCornerShape(12.dp)).background(Palette.surface).padding(14.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
        Text(title, style = Type.body)
        if (detail.isNotEmpty()) Text(detail, style = Type.small)
        Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp), content = buttons)
    }
}

private fun pullLine(pull: PullRequest) = listOfNotNull(
    "#${pull.number} ${pull.title}",
    when (pull.checks) { "success" -> "checks passed"; "failure" -> "checks failing"; "pending" -> "checks running"; else -> null },
    pull.review.takeIf { it.isNotEmpty() },
).joinToString(" · ")

/** The branch of a worktree started for an issue, named as the desktop names it (gitrepo.IssueBranch). */
fun issueBranch(number: Int, title: String): String {
    var slug = title.replace("/", "-").replace(Regex("[^A-Za-z0-9._-]+"), "-").trim('-', '.').lowercase().ifEmpty { "worktree" }
    if (slug.length > 40) {
        slug = slug.take(40)
        val cut = slug.lastIndexOf('-')
        if (cut > 10) slug = slug.take(cut)
        slug = slug.trimEnd('-', '.')
    }
    return "issue-$number-$slug"
}

/** A folder as the composer names it: a worktree is its project › its branch. */
fun RemoteApp.folderName(path: String): String {
    val project = worktrees[path] ?: return projectName(path)
    val branch = repositories[project]?.worktrees?.firstOrNull { it.path == path }?.label ?: projectName(path)
    return projectName(project) + " › " + branch
}
