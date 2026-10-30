package io.oneloop.remote

import android.content.Intent
import android.graphics.BitmapFactory
import android.net.Uri
import android.util.LruCache
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URL

// Settings → Extensions: the desktop's store, installed list and MCP servers
// (internal/ui/extensions_page.go), through the routes of
// internal/extensions/http.go where the agent runs.

private sealed interface ExtensionsPage {
    data object Tabs : ExtensionsPage
    class Details(val listing: JSONObject) : ExtensionsPage
    class Preparing(val repo: String, val from: ExtensionsPage) : ExtensionsPage
    class Confirm(val plan: JSONObject, val from: ExtensionsPage) : ExtensionsPage
    class Server(val server: JSONObject?, val previous: String) : ExtensionsPage
}

/** The store's search, kept while a listing's page is open. */
private class StoreState {
    var query by mutableStateOf("")
    var kind by mutableStateOf("all")
    var order by mutableStateOf("stars")
    val listings = mutableStateListOf<JSONObject>()
    var page by mutableIntStateOf(0)
    var more by mutableStateOf(false)
    var status by mutableStateOf<String?>(null)
    var loading by mutableStateOf(false)
    var search = 0 // the search on screen; an older one's answer is dropped
}

private val storeOrders = listOf("stars" to "Most stars", "updated" to "Recently updated", "best" to "Best match")

@Composable
fun ExtensionsScreen(app: RemoteApp, onBack: () -> Unit) {
    val scope = rememberCoroutineScope()
    var tab by remember { mutableStateOf("store") }
    var page by remember { mutableStateOf<ExtensionsPage>(ExtensionsPage.Tabs) }
    var notice by remember { mutableStateOf<String?>(null) }
    val store = remember(app.server) { StoreState() }
    BackHandler {
        page = when (val shown = page) {
            ExtensionsPage.Tabs -> return@BackHandler onBack()
            is ExtensionsPage.Preparing -> shown.from
            is ExtensionsPage.Confirm -> shown.from
            else -> ExtensionsPage.Tabs
        }
    }
    fun install(repo: String) {
        val preparing = ExtensionsPage.Preparing(repo, page)
        page = preparing
        scope.launch {
            try {
                val plan = app.extensionsCall("/api/extensions/prepare", JSONObject().put("repo", repo), 300000)
                if (page === preparing) page = ExtensionsPage.Confirm(plan, preparing.from)
            } catch (cancelled: CancellationException) { throw cancelled }
            catch (failure: Exception) {
                if (page === preparing) { page = preparing.from; notice = failure.message ?: "Could not download $repo." }
            }
        }
    }
    Column(Modifier.fillMaxSize().background(Palette.background).windowInsetsPadding(WindowInsets.safeDrawing)) {
        Row(Modifier.padding(12.dp), verticalAlignment = Alignment.CenterVertically) {
            RoundButton(Icons.back, Color.Transparent, Palette.muted, description = "Back") {
                page = when (val shown = page) {
                    ExtensionsPage.Tabs -> return@RoundButton onBack()
                    is ExtensionsPage.Confirm -> shown.from
                    else -> ExtensionsPage.Tabs
                }
            }
            Text("Extensions", style = Type.title, modifier = Modifier.padding(start = 12.dp))
        }
        when (val shown = page) {
            ExtensionsPage.Tabs -> Column(Modifier.weight(1f)) {
                Row(Modifier.padding(horizontal = 20.dp).horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    for ((id, label) in listOf("store" to "Store", "installed" to "Installed", "mcp" to "MCP servers")) {
                        Pill(label, tab == id) { tab = id }
                    }
                }
                when (tab) {
                    "installed" -> InstalledTab(app) { notice = it }
                    "mcp" -> McpTab(app, onEdit = { server, previous -> page = ExtensionsPage.Server(server, previous) }) { notice = it }
                    else -> StoreTab(app, store, scope, onOpen = { page = ExtensionsPage.Details(it) }, onInstall = ::install, onInstalled = { tab = "installed" })
                }
            }
            is ExtensionsPage.Details -> ListingDetails(app, shown.listing) { install(shown.listing.optString("repo")) }
            is ExtensionsPage.Preparing -> Column(Modifier.weight(1f).fillMaxWidth().padding(20.dp), verticalArrangement = Arrangement.spacedBy(16.dp, Alignment.CenterVertically), horizontalAlignment = Alignment.CenterHorizontally) {
                Text("Downloading ${shown.repo} to see what it holds…", style = Type.body)
                Pill("Cancel") { page = shown.from }
            }
            is ExtensionsPage.Confirm -> InstallConfirm(app, shown.plan, onCancel = { page = shown.from }) {
                val repo = shown.plan.optString("repo")
                store.listings.replaceAll { if (it.optString("repo").equals(repo, true)) JSONObject(it.toString()).put("installed", true) else it }
                page = ExtensionsPage.Tabs
                tab = "installed"
            }
            is ExtensionsPage.Server -> McpForm(app, shown.server, shown.previous) { page = ExtensionsPage.Tabs; tab = "mcp" }
        }
    }
    notice?.let { text ->
        AlertDialog(
            onDismissRequest = { notice = null }, containerColor = Palette.surface,
            title = { Text("Extensions", style = Type.title) }, text = { Text(text, style = Type.small) },
            confirmButton = { Pill("OK") { notice = null } },
        )
    }
}

// ---------------------------------------------------------------------------
// The store.

private fun RemoteApp.searchStore(store: StoreState, scope: CoroutineScope, number: Int) {
    val search = ++store.search
    store.loading = true
    store.status = "Searching GitHub…"
    scope.launch {
        try {
            val body = JSONObject().put("kind", store.kind).put("query", store.query.trim()).put("sort", store.order).put("page", number)
            val result = extensionsCall("/api/extensions/search", body, 45000)
            if (search != store.search) return@launch
            if (number == 1) store.listings.clear()
            // A repository with both topics can come back on a later page; the list keys by repository.
            val shown = store.listings.mapTo(HashSet()) { it.optString("repo") }
            result.optJSONArray("listings")?.let { for (i in 0 until it.length()) if (shown.add(it.getJSONObject(i).optString("repo"))) store.listings.add(it.getJSONObject(i)) }
            store.page = number
            store.more = result.optBoolean("more")
            store.status = if (store.listings.isEmpty()) "Nothing found. Try other words, or another kind."
            else "${countText(result.optInt("total"), "result")} on GitHub · showing ${store.listings.size}"
        } catch (cancelled: CancellationException) { throw cancelled }
        catch (failure: Exception) { if (search == store.search) store.status = failure.message ?: "GitHub cannot be reached." }
        finally { if (search == store.search) store.loading = false }
    }
}

@Composable
private fun StoreTab(app: RemoteApp, store: StoreState, scope: CoroutineScope, onOpen: (JSONObject) -> Unit, onInstall: (String) -> Unit, onInstalled: () -> Unit) {
    LaunchedEffect(store) { if (store.listings.isEmpty() && !store.loading) app.searchStore(store, scope, 1) }
    var ordering by remember { mutableStateOf(false) }
    val focus = LocalFocusManager.current
    LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(20.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        item {
            Text("Skills teach the agent a task; plugins bundle skills, MCP servers and hooks. Both work with 1loop's agent and with Claude Code.", style = Type.small)
        }
        item {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Field(store.query, { store.query = it }, "Search skills and plugins…", KeyboardType.Text, Modifier.weight(1f))
                Pill("Search", primary = true, icon = Icons.globe) { focus.clearFocus(); app.searchStore(store, scope, 1) }
            }
        }
        item {
            Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                for ((id, label) in listOf("all" to "All", "skill" to "Skills", "plugin" to "Plugins")) {
                    Pill(label, store.kind == id) { store.kind = id; app.searchStore(store, scope, 1) }
                }
            }
        }
        item {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(store.status.orEmpty(), style = Type.small, modifier = Modifier.weight(1f))
                Box {
                    Pill(storeOrders.first { it.first == store.order }.second, icon = Icons.chevron) { ordering = true }
                    DropdownMenu(ordering, { ordering = false }, containerColor = Palette.popover) {
                        for ((id, label) in storeOrders) DropdownMenuItem(
                            text = { Text(label, style = Type.body) },
                            trailingIcon = { if (store.order == id) Icon(Icons.check, null, tint = Palette.amber, modifier = Modifier.size(18.dp)) },
                            onClick = { ordering = false; store.order = id; app.searchStore(store, scope, 1) },
                        )
                    }
                }
            }
        }
        items(store.listings, key = { it.optString("repo") }) { listing ->
            ListingCard(listing, onOpen = { onOpen(listing) }, onInstall = { onInstall(listing.optString("repo")) }, onInstalled = onInstalled)
        }
        if (store.more) item {
            Box(Modifier.fillMaxWidth(), contentAlignment = Alignment.Center) {
                Pill(if (store.loading) "Loading…" else "Load more", enabled = !store.loading) { app.searchStore(store, scope, store.page + 1) }
            }
        }
    }
}

/** A listing: its picture, name, owner, stars, description, and Install. */
@Composable
private fun ListingCard(listing: JSONObject, onOpen: () -> Unit, onInstall: () -> Unit, onInstalled: () -> Unit) {
    val shape = RoundedCornerShape(14.dp)
    Column(
        Modifier.fillMaxWidth().clip(shape).background(Palette.surface).border(1.dp, Palette.border, shape).clickable(onClick = onOpen).padding(10.dp),
        verticalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        WebPicture(listing.optString("image"), Modifier.fillMaxWidth().aspectRatio(2f))
        Row(Modifier.padding(top = 4.dp), verticalAlignment = Alignment.CenterVertically) {
            Text(listing.optString("name"), style = Type.body.copy(fontWeight = FontWeight.SemiBold), maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
            Tag(if (listing.optString("kind") == "plugin") "Plugin" else "Skill")
        }
        Text("by " + listing.optString("owner"), style = Type.small)
        StarsLine(listing)
        Text(listing.optString("description").ifBlank { "No description." }, style = Type.small, maxLines = 3, overflow = TextOverflow.Ellipsis)
        Row(Modifier.padding(top = 4.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            if (listing.optBoolean("installed")) Pill("Installed", icon = Icons.check, onClick = onInstalled)
            else Pill("Install", icon = Icons.plus, onClick = onInstall)
            Pill("Details", onClick = onOpen)
        }
    }
}

@Composable
private fun StarsLine(listing: JSONObject) {
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(4.dp)) {
        Icon(Icons.star, null, tint = Palette.amber, modifier = Modifier.size(15.dp))
        Text(compactCount(listing.optInt("stars")), style = Type.small.copy(color = Palette.text))
        Text("· updated " + ago(listing.optString("updated")), style = Type.caption)
    }
}

@Composable
private fun Tag(text: String) {
    Text(text, style = Type.caption.copy(color = Palette.muted), modifier = Modifier.background(Palette.raised, RoundedCornerShape(6.dp)).padding(horizontal = 6.dp, vertical = 2.dp))
}

/** A listing's page: its picture, numbers, topics and README. */
@Composable
private fun ColumnScope.ListingDetails(app: RemoteApp, listing: JSONObject, onInstall: () -> Unit) {
    val context = LocalContext.current
    val repo = listing.optString("repo")
    var readme by remember(repo) { mutableStateOf<String?>(null) }
    var problem by remember(repo) { mutableStateOf<String?>(null) }
    LaunchedEffect(repo) {
        try { readme = app.extensionsCall("/api/extensions/readme", JSONObject().put("repo", repo), 45000).optString("readme") }
        catch (cancelled: CancellationException) { throw cancelled }
        catch (failure: Exception) { problem = failure.message ?: "Could not load the README." }
    }
    Column(Modifier.weight(1f).verticalScroll(rememberScrollState()).padding(20.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        WebPicture(listing.optString("image"), Modifier.fillMaxWidth().aspectRatio(2f))
        Text(listing.optString("name"), style = Type.headline)
        Text(repo, style = Type.small)
        if (listing.optString("description").isNotBlank()) Text(listing.optString("description"), style = Type.body)
        val facts = mutableListOf("${compactCount(listing.optInt("stars"))} stars", "${compactCount(listing.optInt("forks"))} forks", "updated " + ago(listing.optString("updated")))
        listing.optString("license").takeIf { it.isNotBlank() }?.let { facts.add(it) }
        Text(facts.joinToString(" · "), style = Type.small)
        val topics = listing.optJSONArray("topics")
        if (topics != null && topics.length() > 0) Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
            for (i in 0 until minOf(topics.length(), 8)) Tag(topics.optString(i))
        }
        Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            if (listing.optBoolean("installed")) Pill("Reinstall", icon = Icons.arrowTurn, onClick = onInstall)
            else Pill("Install", primary = true, icon = Icons.plus, onClick = onInstall)
            Pill("Open on GitHub", icon = Icons.globe) {
                runCatching { context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(listing.optString("url")))) }
            }
        }
        Text("README", style = Type.title, modifier = Modifier.padding(top = 8.dp))
        val text = readme
        when {
            problem != null -> Text(problem.orEmpty(), style = Type.small)
            text == null -> Text("Loading the README…", style = Type.small)
            text.isBlank() -> Text("This repository has no README.", style = Type.small)
            else -> Markdown(text)
        }
    }
}

// ---------------------------------------------------------------------------
// The confirmation: what installing adds, the folders it writes, the
// commands it may run and the risks.

@Composable
private fun ColumnScope.InstallConfirm(app: RemoteApp, plan: JSONObject, onCancel: () -> Unit, onInstalled: () -> Unit) {
    val scope = rememberCoroutineScope()
    val marketplace = plan.optString("kind") == "marketplace"
    val plugins = remember(plan) { plan.optJSONArray("plugins").objects() }
    val skills = remember(plan) { plan.optJSONArray("skills").objects() }
    val keep = remember(plan) { mutableStateMapOf<String, Boolean>().apply { plugins.forEach { put(it.optString("id"), !marketplace || plugins.size <= 8) } } }
    var busy by remember { mutableStateOf(false) }
    var problem by remember { mutableStateOf<String?>(null) }
    Column(Modifier.weight(1f).verticalScroll(rememberScrollState()).padding(20.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text("Install ${plan.optString("repo")}?", style = Type.headline)
        if (plugins.isNotEmpty()) {
            Text(if (marketplace) "Plugins of this marketplace (${plugins.size}) · choose those to install" else "Plugin", style = Type.title)
            for (plugin in plugins) {
                val parts = listOfNotNull(
                    plugin.optJSONArray("skills")?.length()?.takeIf { it > 0 }?.let { countText(it, "skill") },
                    plugin.optJSONArray("mcp")?.length()?.takeIf { it > 0 }?.let { countText(it, "MCP server") },
                    plugin.optJSONArray("hooks")?.length()?.takeIf { it > 0 }?.let { countText(it, "hook") },
                )
                val id = plugin.optString("id")
                ExtensionRow((plugin.optString("name") + " " + plugin.optString("version")).trim(), listOf(plugin.optString("description"), parts.joinToString(" · "))) {
                    if (marketplace) Toggle(keep[id] == true, enabled = !busy) { keep[id] = it }
                }
            }
        }
        if (skills.isNotEmpty()) {
            Text(countText(skills.size, "skill"), style = Type.title)
            for (skill in skills) ExtensionRow(skill.optString("name"), listOf(skill.optString("description")))
        }
        Text("Folders it writes", style = Type.title)
        CodeWell(plan.optJSONArray("folders").strings().joinToString("\n"), "Folders", wrap = true)
        Text("Commands it may run on this computer", style = Type.title)
        val commands = plan.optJSONArray("commands").strings()
        if (commands.isEmpty()) Text("None: no MCP server and no hook.", style = Type.small)
        else CodeWell(commands.joinToString("\n"), "Commands", wrap = true)
        Text("Risks", style = Type.title)
        for (risk in plan.optJSONArray("risks").strings()) Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Icon(Icons.shield, null, tint = Palette.amber, modifier = Modifier.padding(top = 3.dp).size(16.dp))
            Text(risk, style = Type.body)
        }
        val notes = plan.optJSONArray("notes").strings()
        if (notes.isNotEmpty()) {
            Text("Notes", style = Type.title)
            for (line in notes) Text(line, style = Type.small)
        }
        problem?.let { Text(it, style = Type.small.copy(color = Palette.danger)) }
    }
    Row(Modifier.fillMaxWidth().padding(16.dp), horizontalArrangement = Arrangement.spacedBy(10.dp, Alignment.End)) {
        Pill("Cancel", enabled = !busy, onClick = onCancel)
        Pill(if (busy) "Installing…" else "Install", primary = true, enabled = !busy, icon = Icons.plus) {
            val chosen = if (marketplace) keep.filterValues { it }.keys.toList() else emptyList()
            if (marketplace && chosen.isEmpty()) { problem = "Choose at least one plugin."; return@Pill }
            busy = true
            problem = null
            scope.launch {
                try {
                    app.extensionsCall("/api/extensions/install", JSONObject().put("plan", plan.optString("id")).put("keep", JSONArray(chosen)), 300000)
                    onInstalled()
                } catch (cancelled: CancellationException) { throw cancelled }
                catch (failure: Exception) { problem = failure.message ?: "Could not install." }
                finally { busy = false }
            }
        }
    }
}

// ---------------------------------------------------------------------------
// What is installed.

/** GET /api/extensions, again after each change made here or elsewhere. */
@Composable
private fun installedState(app: RemoteApp, onProblem: (String) -> Unit): MutableState<JSONObject?> {
    val installed = remember(app.server) { mutableStateOf<JSONObject?>(null) }
    LaunchedEffect(app.server, app.extensionsRevision) {
        try { installed.value = app.extensionsCall("/api/extensions") }
        catch (cancelled: CancellationException) { throw cancelled }
        catch (failure: Exception) { onProblem(failure.message ?: "Could not read the extensions.") }
    }
    return installed
}

private fun RemoteApp.changeExtensions(scope: CoroutineScope, installed: MutableState<JSONObject?>, onProblem: (String) -> Unit, path: String, body: JSONObject) {
    scope.launch {
        try { installed.value = extensionsCall(path, body) }
        catch (cancelled: CancellationException) { throw cancelled }
        catch (failure: Exception) { onProblem(failure.message ?: "Could not change the extensions.") }
    }
}

@Composable
private fun InstalledTab(app: RemoteApp, onProblem: (String) -> Unit) {
    val scope = rememberCoroutineScope()
    val installed = installedState(app, onProblem)
    var removing by remember { mutableStateOf<Triple<String, String, String>?>(null) } // kind, id, name
    val view = installed.value
    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(20.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        if (view == null) { Text("Loading…", style = Type.small); return@Column }
        fun enable(kind: String, id: String, on: Boolean) =
            app.changeExtensions(scope, installed, onProblem, "/api/extensions/enable", JSONObject().put("kind", kind).put("id", id).put("enabled", on))
        Text("Plugins", style = Type.title)
        val plugins = view.optJSONArray("plugins").objects()
        if (plugins.isEmpty()) Text("No plugin yet: find some in the store.", style = Type.small)
        for (plugin in plugins) {
            val parts = mutableListOf("from " + plugin.optString("repo"))
            plugin.optJSONArray("skills")?.length()?.takeIf { it > 0 }?.let { parts.add(countText(it, "skill")) }
            plugin.optJSONArray("mcp")?.length()?.takeIf { it > 0 }?.let { parts.add(countText(it, "MCP server")) }
            plugin.optInt("hooks").takeIf { it > 0 }?.let { parts.add(countText(it, "hook")) }
            plugin.optInt("commands").takeIf { it > 0 }?.let { parts.add(countText(it, "command") + " (Claude Code only)") }
            val id = plugin.optString("id")
            ExtensionRow((plugin.optString("name") + " " + plugin.optString("version")).trim(), listOf(plugin.optString("description"), parts.joinToString(" · "))) {
                RoundButton(Icons.trash, Palette.raised, Palette.muted, description = "Remove", size = 36.dp, iconSize = 18.dp) { removing = Triple("plugin", id, plugin.optString("name")) }
                Toggle(plugin.optBoolean("enabled"), enabled = app.connected) { enable("plugin", id, it) }
            }
        }
        Text("Skills", style = Type.title, modifier = Modifier.padding(top = 8.dp))
        val skills = view.optJSONArray("skills").objects()
        if (skills.isEmpty()) Text("No skill installed on its own. Plugins' skills show with their plugin.", style = Type.small)
        for (skill in skills) {
            val name = skill.optString("name")
            ExtensionRow(name, listOf(skill.optString("description"), skill.optString("source").takeIf { it.isNotBlank() }?.let { "from $it" }.orEmpty())) {
                RoundButton(Icons.trash, Palette.raised, Palette.muted, description = "Remove", size = 36.dp, iconSize = 18.dp) { removing = Triple("skill", name, name) }
                Toggle(skill.optBoolean("enabled"), enabled = app.connected) { enable("skill", name, it) }
            }
        }
        Text("Installed in ${view.optString("dir")}. Changes apply to the next message.", style = Type.caption, modifier = Modifier.padding(top = 8.dp))
    }
    removing?.let { (kind, id, name) ->
        AlertDialog(
            onDismissRequest = { removing = null }, containerColor = Palette.surface,
            title = { Text("Remove $name?", style = Type.title) },
            text = { Text("Its files are deleted from 1loop's extensions folder.", style = Type.small) },
            confirmButton = {
                Pill("Remove", primary = true) {
                    removing = null
                    app.changeExtensions(scope, installed, onProblem, "/api/extensions/remove", JSONObject().put("kind", kind).put("id", id))
                }
            },
            dismissButton = { Pill("Cancel") { removing = null } },
        )
    }
}

// ---------------------------------------------------------------------------
// MCP servers.

@Composable
private fun McpTab(app: RemoteApp, onEdit: (JSONObject?, String) -> Unit, onProblem: (String) -> Unit) {
    val scope = rememberCoroutineScope()
    val installed = installedState(app, onProblem)
    var statuses by remember(app.server) { mutableStateOf<Map<String, JSONObject>?>(null) }
    var checking by remember { mutableStateOf(false) }
    var removing by remember { mutableStateOf<String?>(null) }
    fun check() {
        if (checking) return
        checking = true
        scope.launch {
            try {
                val answer = app.extensionsCall("/api/extensions/mcp/status", JSONObject().put("workspace", app.selectedProject.orEmpty()), 70000)
                statuses = answer.optJSONArray("servers").objects().associateBy { it.optString("name") + "\u0000" + it.optString("plugin") }
            } catch (cancelled: CancellationException) { throw cancelled }
            catch (failure: Exception) { onProblem(failure.message ?: "Could not check the servers.") }
            finally { checking = false }
        }
    }
    val view = installed.value
    val servers = view?.optJSONArray("mcp").objects()
    LaunchedEffect(view != null, app.extensionsRevision) { if (view != null) { statuses = null; check() } }
    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(20.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text("1loop connects to these servers as a client: their tools join the agent's, and Claude Code gets them too. A command starts in the chat's project, on the machine the agent runs on.", style = Type.small)
        Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            Pill("Add server", primary = true, icon = Icons.plus, enabled = app.connected) { onEdit(null, "") }
            Pill(if (checking) "Checking…" else "Check connections", icon = Icons.refresh, enabled = !checking) { check() }
        }
        if (view == null) { Text("Loading…", style = Type.small); return@Column }
        fun state(server: JSONObject, plugin: String): String {
            val status = statuses?.get(server.optString("name") + "\u0000" + plugin)
            return when {
                !server.optBoolean("enabled", true) -> "○ Off"
                status == null -> if (checking) "Connecting…" else "Not checked yet"
                status.optBoolean("connected") -> {
                    val tools = status.optJSONArray("tools").strings()
                    "● Connected · " + countText(tools.size, "tool") + if (tools.isEmpty()) "" else ": " + tools.take(8).joinToString(", ") + if (tools.size > 8) ", …" else ""
                }
                else -> "● " + status.optString("error")
            }
        }
        for (server in servers) {
            val name = server.optString("name")
            val run = if (server.optString("type") == "http") server.optString("url")
            else (server.optString("command") + " " + server.optJSONArray("args").strings().joinToString(" ")).trim()
            ExtensionRow(name, listOf(run.shortened(), state(server, ""))) {
                RoundButton(Icons.pencil, Palette.raised, Palette.muted, description = "Edit", size = 36.dp, iconSize = 18.dp) { onEdit(server, name) }
                RoundButton(Icons.trash, Palette.raised, Palette.muted, description = "Remove", size = 36.dp, iconSize = 18.dp) { removing = name }
                Toggle(server.optBoolean("enabled"), enabled = app.connected) {
                    app.changeExtensions(scope, installed, onProblem, "/api/extensions/enable", JSONObject().put("kind", "mcp").put("id", name).put("enabled", it))
                }
            }
        }
        for (plugin in view.optJSONArray("plugins").objects()) {
            if (!plugin.optBoolean("enabled")) continue
            for (name in plugin.optJSONArray("mcp").strings()) {
                val status = statuses?.values?.firstOrNull { it.optString("name") == name && it.optString("plugin").isNotEmpty() }
                val run = status?.let { if (it.optString("type") == "http") it.optString("url") else (it.optString("command") + " " + it.optJSONArray("args").strings().joinToString(" ")).trim() }.orEmpty()
                ExtensionRow(name, listOf(run.shortened(), state(JSONObject().put("name", name).put("enabled", true), status?.optString("plugin").orEmpty()),
                    "from the plugin ${plugin.optString("name")} (turn the plugin off in Installed)"))
            }
        }
        if (servers.isEmpty() && view.optJSONArray("plugins").objects().none { it.optBoolean("enabled") && (it.optJSONArray("mcp")?.length() ?: 0) > 0 }) {
            Text("No server yet. Add one with its command (for example npx -y @modelcontextprotocol/server-filesystem ~/docs) or its address.", style = Type.small)
        }
    }
    removing?.let { name ->
        AlertDialog(
            onDismissRequest = { removing = null }, containerColor = Palette.surface,
            title = { Text("Remove $name?", style = Type.title) },
            text = { Text("1loop stops using this MCP server.", style = Type.small) },
            confirmButton = {
                Pill("Remove", primary = true) {
                    removing = null
                    app.changeExtensions(scope, installed, onProblem, "/api/extensions/mcp/remove", JSONObject().put("name", name))
                }
            },
            dismissButton = { Pill("Cancel") { removing = null } },
        )
    }
}

/** The form of a server: its name, and its command or address. */
@Composable
private fun ColumnScope.McpForm(app: RemoteApp, server: JSONObject?, previous: String, onDone: () -> Unit) {
    val scope = rememberCoroutineScope()
    var name by remember { mutableStateOf(server?.optString("name").orEmpty()) }
    var web by remember { mutableStateOf(server?.optString("type") == "http") }
    var command by remember { mutableStateOf(server?.optString("command").orEmpty()) }
    var args by remember { mutableStateOf(server?.optJSONArray("args").strings().joinToString("\n")) }
    var env by remember { mutableStateOf(pairsText(server?.optJSONObject("env"), "=")) }
    var address by remember { mutableStateOf(server?.optString("url").orEmpty()) }
    var headers by remember { mutableStateOf(pairsText(server?.optJSONObject("headers"), ": ")) }
    var problem by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf(false) }
    Column(Modifier.weight(1f).verticalScroll(rememberScrollState()).padding(20.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
        Text(previous.ifEmpty { "Add an MCP server" }, style = Type.headline)
        FormLabel("Name", "How 1loop and the agent call it: letters, digits, - or _.")
        Field(name, { name = it }, "github", KeyboardType.Ascii)
        FormLabel("Connects with", "")
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Pill("Command (stdio)", !web) { web = false }
            Pill("Address (HTTP)", web) { web = true }
        }
        if (!web) {
            FormLabel("Command", "The program that starts the server.")
            Field(command, { command = it }, "npx", KeyboardType.Ascii)
            FormLabel("Arguments", "One per line.")
            LinesField(args, { args = it }, "-y\n@modelcontextprotocol/server-filesystem\n/home/me/docs", 3)
            FormLabel("Environment", "One KEY=value per line. \$NAME takes the variable of the machine the agent runs on.")
            LinesField(env, { env = it }, "API_KEY=…", 2)
        } else {
            FormLabel("Address", "Its Streamable HTTP endpoint.")
            Field(address, { address = it }, "https://mcp.example.com/mcp", KeyboardType.Uri)
            FormLabel("Headers", "One Name: value per line, for example its token.")
            LinesField(headers, { headers = it }, "Authorization: Bearer …", 2)
        }
        problem?.let { Text(it, style = Type.small.copy(color = Palette.danger)) }
    }
    Row(Modifier.fillMaxWidth().padding(16.dp), horizontalArrangement = Arrangement.spacedBy(10.dp, Alignment.End)) {
        Pill("Cancel", enabled = !busy, onClick = onDone)
        Pill(if (busy) "Saving…" else "Save", primary = true, enabled = !busy && app.connected) {
            val edited = JSONObject().put("name", name.trim()).put("enabled", server?.optBoolean("enabled") ?: true)
            if (web) edited.put("type", "http").put("url", address.trim()).put("headers", textPairs(headers, ":"))
            else edited.put("type", "stdio").put("command", command.trim()).put("env", textPairs(env, "="))
                .put("args", JSONArray(args.lines().map { it.trim() }.filter { it.isNotEmpty() }))
            busy = true
            problem = null
            scope.launch {
                try {
                    app.extensionsCall("/api/extensions/mcp", JSONObject().put("server", edited).put("previous", previous))
                    onDone()
                } catch (cancelled: CancellationException) { throw cancelled }
                catch (failure: Exception) { problem = failure.message ?: "Could not save the server." }
                finally { busy = false }
            }
        }
    }
}

@Composable
private fun FormLabel(title: String, detail: String) {
    Column(Modifier.padding(top = 6.dp), verticalArrangement = Arrangement.spacedBy(2.dp)) {
        Text(title, style = Type.body.copy(fontWeight = FontWeight.Medium))
        if (detail.isNotEmpty()) Text(detail, style = Type.small)
    }
}

@Composable
private fun LinesField(value: String, onChange: (String) -> Unit, placeholder: String, lines: Int) {
    BasicTextField(
        value, onChange,
        modifier = Modifier.fillMaxWidth().background(Palette.code, RoundedCornerShape(10.dp))
            .border(1.dp, Palette.border, RoundedCornerShape(10.dp)).padding(horizontal = 14.dp, vertical = 12.dp),
        textStyle = Type.mono, minLines = lines, cursorBrush = SolidColor(Palette.amber),
        decorationBox = { inner -> Box { if (value.isEmpty()) Text(placeholder, style = Type.mono.copy(color = Palette.faint)); inner() } },
    )
}

// ---------------------------------------------------------------------------
// Small parts.

/** A row of the lists: a title, lines under it, and its controls. */
@Composable
private fun ExtensionRow(title: String, lines: List<String>, controls: @Composable RowScope.() -> Unit = {}) {
    Row(
        Modifier.fillMaxWidth().background(Palette.surface, RoundedCornerShape(12.dp)).border(1.dp, Palette.borderSoft, RoundedCornerShape(12.dp)).padding(14.dp),
        verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(3.dp)) {
            Text(title, style = Type.body)
            for (line in lines) if (line.isNotBlank()) Text(line.trim(), style = Type.small)
        }
        controls()
    }
}

// Pictures from the web (the listings' social previews), decoded at most
// 1000 px wide and kept for a few screens.
private val webPictures = object : LruCache<String, ImageBitmap>(24 shl 20) {
    override fun sizeOf(key: String, value: ImageBitmap) = value.width * value.height * 4
}

@Composable
private fun WebPicture(address: String, modifier: Modifier) {
    val picture by produceState(webPictures.get(address), address) {
        if (value != null || !address.startsWith("https://")) return@produceState
        value = withContext(Dispatchers.IO) {
            runCatching {
                val connection = URL(address).openConnection() as HttpURLConnection
                connection.connectTimeout = 15000
                connection.readTimeout = 30000
                connection.setRequestProperty("User-Agent", "1loop")
                val bytes = try { connection.inputStream.use { it.readBytes() } } finally { connection.disconnect() }
                val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
                BitmapFactory.decodeByteArray(bytes, 0, bytes.size, bounds)
                var sample = 1
                while (bounds.outWidth / sample > 1000) sample *= 2
                BitmapFactory.decodeByteArray(bytes, 0, bytes.size, BitmapFactory.Options().apply { inSampleSize = sample })?.asImageBitmap()
            }.getOrNull()
        }?.also { webPictures.put(address, it) }
    }
    Box(modifier.clip(RoundedCornerShape(8.dp)).background(Palette.code)) {
        picture?.let { Image(it, null, Modifier.fillMaxSize(), contentScale = ContentScale.Fit) }
    }
}

/** A server's command line, cut: a plugin's can be a whole script. */
private fun String.shortened() = if (length > 160) take(159) + "…" else this

private fun JSONArray?.objects(): List<JSONObject> = if (this == null) emptyList() else (0 until length()).mapNotNull { optJSONObject(it) }

private fun JSONArray?.strings(): List<String> = if (this == null) emptyList() else (0 until length()).map { optString(it) }

private fun pairsText(values: JSONObject?, separator: String): String =
    values?.keys()?.asSequence()?.map { it + separator + values.optString(it) }?.joinToString("\n").orEmpty()

private fun textPairs(text: String, separator: String): JSONObject {
    val values = JSONObject()
    for (line in text.lines()) {
        val at = line.indexOf(separator)
        if (at > 0 && line.substring(0, at).isNotBlank()) values.put(line.substring(0, at).trim(), line.substring(at + separator.length).trim())
    }
    return values
}

private fun compactCount(n: Int): String = when {
    n >= 1_000_000 -> "%.1fM".format(n / 1e6)
    n >= 10_000 -> "${n / 1000}k"
    n >= 1000 -> "%.1fk".format(n / 1000.0)
    else -> n.toString()
}

private fun countText(n: Int, word: String) = if (n == 1) "1 $word" else "${compactCount(n)} ${word}s"

/** How long ago a time (RFC 3339) was, as people say it. */
internal fun ago(text: String): String {
    val at = runCatching { java.time.OffsetDateTime.parse(text).toInstant().toEpochMilli() }.getOrNull() ?: return "a while ago"
    val hours = (System.currentTimeMillis() - at) / 3_600_000
    return when {
        hours < 1 -> "just now"
        hours < 24 -> "$hours h ago"
        hours < 60 * 24 -> "${hours / 24} days ago"
        hours < 730 * 24 -> "${hours / 24 / 30} months ago"
        else -> "${hours / 24 / 365} years ago"
    }
}
