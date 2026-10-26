package io.oneloop.remote

import android.content.Context
import android.content.Intent
import android.net.ConnectivityManager
import android.os.Build
import android.system.Os
import android.util.Base64
import android.util.Log
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.withContext
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.async
import kotlinx.coroutines.delay
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.launch
import kotlinx.coroutines.runInterruptible
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withTimeout
import kotlinx.coroutines.withTimeoutOrNull
import org.json.JSONObject
import java.io.File
import java.io.IOException
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.SimpleFileVisitor
import java.nio.file.FileVisitResult
import java.nio.file.attribute.BasicFileAttributes
import java.security.KeyStore
import java.security.SecureRandom
import java.security.MessageDigest
import java.util.UUID
import java.util.concurrent.atomic.AtomicLong
import java.util.concurrent.atomic.AtomicReference
import java.util.concurrent.ConcurrentHashMap

class AndroidLocalRuntime private constructor(private val context: Context) : LocalRuntime {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val prefs = context.getSharedPreferences("1loop-local", Context.MODE_PRIVATE)
    private val base = File(context.noBackupFilesDir, "local-phone").apply { mkdirs() }
    private val roots = File(base, "roots").apply { mkdirs() }
    private val libraries = File(context.applicationInfo.nativeLibraryDir)
    private val agentDigest by lazy {
        val digest = MessageDigest.getInstance("SHA-256")
        context.assets.open("local/1loop-server").use { input ->
            val buffer = ByteArray(64 * 1024)
            while (true) {
                val count = input.read(buffer)
                if (count < 0) break
                digest.update(buffer, 0, count)
            }
        }
        digest.digest().joinToString("") { "%02x".format(it.toInt() and 255) }
    }
    val browser = LocalBrowser(context)
    private val phone = LocalAdb(context, scope)
    private val preparation = Mutex()
    private val startup = Mutex()
    private val generation = AtomicLong()
    private val lifecycle = Any()
    private val engine = AtomicReference<Process?>()
    private val setupProcess = AtomicReference<Process?>()
    private val terminals = ConcurrentHashMap<String, Process>()
    private val cancelledTerminals = mutableSetOf<String>() // lifecycle owns pending-start cancellations.
    private val bridge = AtomicReference<LocalBridge?>()
    private val mutableServer = MutableStateFlow<Server?>(null)
    val activeServer = mutableServer.asStateFlow()
    private val mutableStatus = MutableStateFlow(LocalStatus(false, emptyList()))
    override val status = mutableStatus.asStateFlow()
    override val adb = phone.state
    @Volatile var appForeground = true
    /**
     * The engine was stopped after a while idle in the background: the app
     * starts it again when shown. Saved, as Android may end the app's
     * process once nothing runs.
     */
    var stoppedWhenIdle: Boolean
        get() = prefs.getBoolean("stoppedWhenIdle", false)
        set(value) { prefs.edit().putBoolean("stoppedWhenIdle", value).commit() }
    @Volatile private var requested: String? = null
    @Volatile private var progress: SetupStep? = null
    @Volatile private var failure: SetupStep? = null
    @Volatile private var stopping: Job? = null

    init {
        refresh()
        scope.launch {
            adb.map { it.allowed && it.connected }.distinctUntilChanged().collect { notifyCapabilities() }
        }
    }

    private suspend fun notifyCapabilities() {
        mutableServer.value?.let { server ->
            val state = adb.value
            runCatching { Api(server).post("/api/local/capabilities", JSONObject().put("phone", state.allowed && state.connected)) }
        }
    }

    private fun root(): File? = prefs.getString("root", null)?.takeIf {
        it.matches(Regex("root-[0-9a-f-]{36}"))
    }?.let { File(roots, it) }?.takeIf { File(it, "usr/bin/bash").isFile }

    private fun supported() = Build.VERSION.SDK_INT >= 31 && File(libraries, "libproot.so").isFile

    private fun baseToolsReady() = root()?.let { File(it, "var/lib/1loop/base-tools.ready").isFile } == true

    private fun claudeInstalled() = root()?.let {
        File(it, "var/lib/1loop/claude.ready").isFile && File(it, "usr/bin/claude").isFile
    } == true

    private fun refresh() {
        if (!supported()) {
            mutableStatus.value = LocalStatus(false, listOf(SetupStep(
                "unsupported", "Local phone is unavailable", "Local execution currently requires Android 12 or later on an ARM64 phone. Remote connections remain available.", StepState.Failed, true, false,
            )))
            return
        }
        val steps = mutableListOf<SetupStep>()
        if (!baseToolsReady()) steps += step("debian", "Prepare Debian and tools", "Download the 28 MB Debian base, update and upgrade its packages, then install Python 3, pip, venv, Git, curl and Claude Code. This needs additional downloads and may take several minutes. Root and Termux are not required.", true)
        if (requested == "claudecode" && !claudeInstalled()) steps += step("claudecode", "Install Claude Code", "Download Claude Code from Anthropic's signed Debian repository, then sign in on this phone.", false)
        progress?.takeIf { active -> steps.none { it.id == active.id } }?.let { steps += it }
        mutableStatus.value = LocalStatus(baseToolsReady(), steps)
    }

    private fun step(id: String, title: String, detail: String, required: Boolean) =
        progress?.takeIf { it.id == id } ?: failure?.takeIf { it.id == id }
        ?: SetupStep(id, title, detail, StepState.Missing, required)

    fun requestComponent(id: String) {
        require(id == "claudecode") { "Unknown optional local component." }
        requested = id
        failure = null
        refresh()
    }

    override suspend fun prepare(stepId: String) {
        val epoch = generation.get()
        scope.async {
            try {
                preparation.withLock {
                    stopping?.join()
                    check(generation.get() == epoch) { "Local setup was stopped." }
                    check(supported()) { "Local execution is not supported on this phone." }
                    cleanupPendingInstallation()
                    require(stepId in listOf("debian", "claudecode")) { "Unknown local preparation step." }
                    LocalRuntimeService.ensureRunning(context)
                    requested = stepId
                    failure = null
                    val title = if (stepId == "debian") "Install Debian" else "Install Claude Code"
                    progress = SetupStep(stepId, title, "Preparing…", StepState.Running, stepId == "debian")
                    refresh()
                    try {
                        if (stepId == "debian" && root() == null) {
                            val bootstrap = File(base, "bootstrap")
                            seed(bootstrap)
                            val name = "root-${UUID.randomUUID()}"
                            check(prefs.edit().putString("pending-root", name).commit()) { "Unable to record the local installation." }
                            runSetup(bootstrap, listOf(agent(bootstrap), "-install-mobile-debian", "/install/$name"), epoch, listOf(roots.absolutePath + ":/install"))
                            check(generation.get() == epoch) { "Local setup was stopped." }
                            val installed = File(roots, name)
                            check(File(installed, "usr/bin/bash").isFile) { "Debian did not install correctly." }
                            prepareBaseTools(installed, epoch)
                            check(prefs.edit().putString("root", name).remove("pending-root").commit()) { "Unable to save the local installation." }
                        } else if (stepId == "debian" && !baseToolsReady()) {
                            prepareBaseTools(root() ?: error("Install Debian first."), epoch)
                        } else if (stepId == "claudecode" && !claudeInstalled()) {
                            val root = root() ?: error("Install Debian first.")
                            runSetup(root, listOf(agent(root), "-install-mobile-claude"), epoch)
                            check(generation.get() == epoch) { "Local setup was stopped." }
                            privateText(File(root, "var/lib/1loop/claude.ready"), "ready\n")
                        }
                    } catch (problem: Exception) {
                        try { cleanupPendingInstallation() } catch (cleanup: Exception) { problem.addSuppressed(cleanup) }
                        val detail = (problem.message ?: "Local setup failed.") + if (problem.suppressed.isEmpty()) "" else " Cleanup could not finish and will be retried before the next installation."
                        failure = progress?.copy(state = StepState.Failed, detail = detail)
                        throw problem
                    } finally {
                        progress = null
                        refresh()
                    }
                }
            } finally { LocalRuntimeService.releaseIfIdle(context) }
        }.await()
    }

    private suspend fun prepareBaseTools(root: File, epoch: Long) {
        seed(root)
        runSetup(root, listOf("/usr/bin/dpkg", "--configure", "-a"), epoch)
        runSetup(root, listOf("/usr/bin/apt-get", "update", "--error-on=any"), epoch)
        runSetup(root, listOf("/usr/bin/apt-get", "upgrade", "-y", "-o", "Dpkg::Options::=--force-confold"), epoch)
        runSetup(root, listOf("/usr/bin/apt-get", "install", "-y", "--no-install-recommends", "ca-certificates", "python3", "python3-pip", "python3-venv", "git", "curl"), epoch)
        runSetup(root, listOf(agent(root), "-install-mobile-claude"), epoch)
        check(generation.get() == epoch) { "Local setup was stopped." }
        privateText(File(root, "var/lib/1loop/claude.ready"), "ready\n")
        privateText(File(root, "var/lib/1loop/base-tools.ready"), "ready\n")
    }

    private fun cleanupPendingInstallation() {
        val name = prefs.getString("pending-root", null) ?: return
        require(name.matches(Regex("root-[0-9a-f-]{36}"))) { "Invalid pending installation." }
        check(setupProcess.get()?.isAlive != true) { "The previous installer is still stopping." }
        if (name != prefs.getString("root", null)) {
            val folder = File(roots, name).toPath()
            if (Files.exists(folder, java.nio.file.LinkOption.NOFOLLOW_LINKS)) {
                // Debian contains symlinks. Never follow them while removing our
                // incomplete installation, especially absolute guest paths.
                Files.walkFileTree(folder, object : SimpleFileVisitor<Path>() {
                    override fun visitFile(file: Path, attributes: BasicFileAttributes): FileVisitResult {
                        Files.delete(file)
                        return FileVisitResult.CONTINUE
                    }
                    override fun postVisitDirectory(directory: Path, failure: IOException?): FileVisitResult {
                        if (failure != null) throw failure
                        Files.delete(directory)
                        return FileVisitResult.CONTINUE
                    }
                })
            }
        }
        Files.deleteIfExists(File(roots, "$name.tar.gz").toPath())
        check(prefs.edit().remove("pending-root").commit()) { "Unable to finish installation cleanup." }
    }

    private suspend fun installPackages(arguments: JSONObject): JSONObject = preparation.withLock {
        val packages = arguments.getJSONArray("packages")
        require(packages.length() in 1..30) { "Choose between 1 and 30 packages." }
        val names = (0 until packages.length()).map { packages.getString(it) }
        require(names.all { it.matches(Regex("[a-z0-9][a-z0-9+.-]*(?::arm64)?")) }) { "Only Debian package names are accepted." }
        val root = root() ?: error("Install Debian first.")
        val epoch = generation.get()
        progress = SetupStep("packages", "Install Debian packages", names.joinToString(", "), StepState.Running, false)
        refresh()
        try {
            runSetup(root, listOf("/usr/bin/dpkg", "--configure", "-a"), epoch)
            runSetup(root, listOf("/usr/bin/apt-get", "update"), epoch)
            val output = runSetup(root, listOf("/usr/bin/apt-get", "install", "-y", "--no-install-recommends", "--") + names, epoch)
            JSONObject().put("text", output)
        } finally {
            progress = null
            refresh()
        }
    }

    private suspend fun runSetup(root: File, arguments: List<String>, epoch: Long, binds: List<String> = emptyList()): String {
        val process = synchronized(lifecycle) {
            check(generation.get() == epoch) { "Local setup was stopped." }
            process(root, arguments, binds).also { setupProcess.set(it) }
        }
        val output = StringBuilder()
        val reader = scope.launch {
            try {
                process.inputStream.bufferedReader().useLines { lines -> lines.forEach { line ->
                    ensureActive()
                    output.append(line).append('\n')
                    if (output.length > 8192) output.delete(0, output.length - 8192)
                    progress = progress?.copy(detail = line.take(240))
                    refresh()
                } }
            } catch (problem: IOException) {
                if (process.isAlive) Log.w("LocalRuntime", "Could not read setup output", problem)
            }
        }
        try {
            val code = runInterruptible { process.waitFor() }
            reader.join()
            check(code == 0) { output.toString().takeLast(4096).ifBlank { "Local setup exited with status $code." } }
            return output.toString()
        } finally {
            try {
                if (process.isAlive) process.destroyForcibly()
                withContext(NonCancellable + Dispatchers.IO) {
                    withTimeout(5000) { runInterruptible { process.waitFor() } }
                }
            } finally {
                if (!process.isAlive) setupProcess.compareAndSet(process, null)
                reader.cancel()
            }
        }
    }

    override suspend fun start(): Server {
        stoppedWhenIdle = false // an older idle stop must not start it again later
        val epoch = generation.get()
        return scope.async {
            try {
                stopping?.join()
                startup.withLock {
                    check(generation.get() == epoch) { "Local startup was stopped." }
                    mutableServer.value?.takeIf { engine.get()?.isAlive == true }?.let { return@withLock it }
                    check(supported()) { "Local execution is not supported on this phone." }
                    val root = root() ?: error("Prepare Debian before starting the local agent.")
                    LocalRuntimeService.ensureRunning(context)
                    seed(root)
                    val nativeBridge = LocalBridge(scope, phone, browser, ::terminalOperation, ::installPackages)
                    var started: Process? = null
                    try {
                        privateText(File(root, "var/lib/1loop/native-bridge.json"), nativeBridge.configuration().toString())
                        val token = ByteArray(32).also { SecureRandom().nextBytes(it) }.joinToString("") { "%02x".format(it.toInt() and 255) }
                        privateText(File(root, "var/lib/1loop/client-token"), token)
                        val readyName = "ready-${UUID.randomUUID()}.json"
                        val process = synchronized(lifecycle) {
                            check(generation.get() == epoch) { "Local startup was stopped." }
                            bridge.getAndSet(nativeBridge)?.close()
                            process(root, listOf(
                            agent(root), "-workspace", "/projects/default", "-data-dir", "/var/lib/1loop",
                            "-listen", "127.0.0.1:0", "-local-client-token-file", "/var/lib/1loop/client-token",
                            "-local-ready-file", "/var/lib/1loop/$readyName", "-local-bridge-config", "/var/lib/1loop/native-bridge.json",
                            ), environment = mapOf("ONELOOP_PROJECT_LAUNCHER" to "/usr/local/libexec/1loop-project", "ONELOOP_LOCAL_PHONE" to "1")).also { engine.set(it); started = it }
                        }
                        val output = StringBuilder()
                        scope.launch {
                            try {
                                process.inputStream.bufferedReader().useLines { lines -> lines.forEach { line ->
                                    ensureActive()
                                    synchronized(output) {
                                        output.append(line).append('\n')
                                        if (output.length > 8192) output.delete(0, output.length - 8192)
                                    }
                                } }
                            } catch (problem: IOException) {
                                // Android interrupts the pipe read when the child is stopped.
                                if (process.isAlive) Log.w("LocalRuntime", "Could not read agent output", problem)
                            }
                        }
                        val ready = File(root, "var/lib/1loop/$readyName")
                        withTimeout(20_000) {
                            while (!ready.isFile) {
                                check(generation.get() == epoch) { "Local startup was stopped." }
                                check(process.isAlive) { synchronized(output) { output.toString().takeLast(4096).ifBlank { "The local agent stopped during startup." } } }
                                delay(100)
                            }
                        }
                        check(generation.get() == epoch) { "Local startup was stopped." }
                        val endpoint = JSONObject(ready.readText())
                        val server = Server("This phone", "127.0.0.1", endpoint.getInt("port"), token, "https", endpoint.getString("fingerprint"), localPhone = true)
                        synchronized(lifecycle) {
                            check(generation.get() == epoch && engine.get() === process) { "Local startup was stopped." }
                            mutableServer.value = server
                        }
                        notifyCapabilities()
                        scope.launch {
                            runInterruptible { process.waitFor() }
                            synchronized(lifecycle) {
                                if (engine.get() === process) stop()
                            }
                        }
                        server
                    } catch (problem: Exception) {
                        started?.let { process ->
                            if (engine.compareAndSet(process, null)) mutableServer.value = null
                            process.destroyForcibly()
                        }
                        bridge.compareAndSet(nativeBridge, null)
                        nativeBridge.close()
                        if (generation.get() == epoch && engine.get() == null) LocalRuntimeService.releaseIfIdle(context)
                        throw problem
                    }
                }
            } finally { LocalRuntimeService.releaseIfIdle(context) }
        }.await()
    }

    private suspend fun terminalOperation(arguments: JSONObject): JSONObject {
        val id = arguments.getString("id")
        require(id.matches(Regex("[a-zA-Z0-9-]{1,80}"))) { "Invalid terminal session." }
        val caller = kotlinx.coroutines.currentCoroutineContext()
        return when (arguments.getString("action")) {
            "open" -> synchronized(lifecycle) {
                caller.ensureActive()
                check(engine.get()?.isAlive == true) { "The local agent is stopped." }
                check(!cancelledTerminals.remove(id)) { "Terminal startup was cancelled." }
                check(!terminals.containsKey(id)) { "Terminal already exists." }
                val root = root() ?: error("Debian is not installed.")
                val workspace = arguments.getString("workspace")
                require(workspace.startsWith("/projects/")) { "Choose a project directory." }
                val folder = File(root, workspace.removePrefix("/")).canonicalFile
                val projects = File(root.canonicalFile, "projects")
                require(projects.canonicalFile == projects && folder.isDirectory && folder.path.startsWith(projects.path + "/")) { "Choose a project directory." }
                val tty = arguments.getString("tty")
                require(tty.matches(Regex("/dev/pts/[0-9]+"))) { "Invalid terminal device." }
                val command = mutableListOf("/usr/local/libexec/1loop-project")
                if (arguments.optBoolean("yolo")) command.add("--yolo")
                command.addAll(listOf(workspace, "/bin/bash", "-i"))
                terminals[id] = process(root, command, environment = mapOf("TERM" to "xterm-256color", "COLORTERM" to "truecolor"), tty = tty)
                JSONObject()
            }
            "wait" -> {
                val child = terminals[id] ?: error("Terminal is closed.")
                JSONObject().put("exit", runInterruptible { child.waitFor() })
            }
            "close" -> {
                synchronized(lifecycle) {
                    terminals.remove(id).also { if (it == null) cancelledTerminals.add(id) }
                }?.let { child ->
                    child.destroyForcibly()
                    withTimeoutOrNull(2000) { runInterruptible { child.waitFor() } }
                }
                JSONObject()
            }
            else -> error("Unknown terminal operation.")
        }
    }

    private fun process(root: File, arguments: List<String>, binds: List<String> = emptyList(), environment: Map<String, String> = emptyMap(), tty: String? = null): Process {
        val command = mutableListOf(File(libraries, "libguard.so").absolutePath, android.os.Process.myPid().toString())
        if (tty != null) command.addAll(listOf("--pty", tty))
        command.addAll(listOf(File(libraries, "libproot.so").absolutePath, "--link2symlink", "-0", "-r", root.absolutePath, "-b", libraries.absolutePath + ":/oneloop", "-b", "/proc", "-b", "/dev", "-w", "/"))
        for (bind in binds) command.addAll(listOf("-b", bind))
        command.addAll(arguments)
        val child = ProcessBuilder(command).redirectErrorStream(true).apply {
            environment().clear()
            environment().putAll(mapOf(
                "HOME" to "/root", "PATH" to "/root/.local/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "LANG" to "C.UTF-8", "TMPDIR" to "/tmp",
                "XDG_CONFIG_HOME" to "/root/.config", "LD_LIBRARY_PATH" to libraries.absolutePath,
                "PROOT_LOADER" to File(libraries, "libloader.so").absolutePath, "PROOT_TMP_DIR" to File(root, "tmp").absolutePath,
                "DEBIAN_FRONTEND" to "noninteractive",
            ))
            environment().putAll(environment)
        }.start()
        // Read only the guard's header; buffering here would consume agent output.
        val header = StringBuilder()
        while (header.length < 64) {
            val byte = child.inputStream.read()
            if (byte < 0 || byte == '\n'.code) break
            header.append(byte.toChar())
        }
        val pid = header.toString().removePrefix("1loop-child:").toIntOrNull()
        if (!header.startsWith("1loop-child:") || pid == null || pid <= 0) {
            child.destroy()
            error("The local process guard did not start: $header")
        }
        return object : Process() {
            override fun getInputStream() = child.inputStream
            override fun getOutputStream() = child.outputStream
            override fun getErrorStream() = child.errorStream
            override fun waitFor() = child.waitFor()
            override fun exitValue() = child.exitValue()
            override fun destroy() = child.destroy()
            override fun destroyForcibly(): Process {
                // Android 12's Process implementation sends SIGTERM even here.
                synchronized(child) {
                    if (child.isAlive) {
                        try { Os.kill(pid, android.system.OsConstants.SIGKILL) }
                        catch (problem: android.system.ErrnoException) {
                            if (problem.errno != android.system.OsConstants.ESRCH) throw problem
                        }
                    }
                }
                return this
            }
        }
    }

    private fun seed(root: File) {
        for (directory in listOf("usr/local/bin", "usr/local/libexec", "etc/ssl/certs", "tmp", "proc", "dev", "root/.config", "install", "var/lib/1loop/sandbox-state", "projects/default/.home")) File(root, directory).mkdirs()
        agent(root)
        asset("local/project-launcher", File(root, "usr/local/libexec/1loop-project"))
        asset("local/open-url", File(root, "usr/local/libexec/1loop-open-url"))
        val store = KeyStore.getInstance("AndroidCAStore").apply { load(null) }
        val certificates = buildString {
            val aliases = store.aliases()
            while (aliases.hasMoreElements()) {
                val alias = aliases.nextElement()
                if (!alias.startsWith("system:")) continue
                val encoded = Base64.encodeToString(store.getCertificate(alias).encoded, Base64.NO_WRAP)
                append("-----BEGIN CERTIFICATE-----\n").append(encoded.chunked(64).joinToString("\n")).append("\n-----END CERTIFICATE-----\n")
            }
        }
        privateText(File(root, "etc/ssl/certs/ca-certificates.crt"), certificates)
        val manager = context.getSystemService(ConnectivityManager::class.java)
        val dns = manager.activeNetwork?.let { manager.getLinkProperties(it)?.dnsServers }.orEmpty()
        if (dns.isNotEmpty()) privateText(File(root, "etc/resolv.conf"), dns.joinToString("\n", postfix = "\n") { "nameserver ${it.hostAddress}" })
    }

    private fun asset(name: String, destination: File) {
        context.assets.open(name).use { input -> destination.outputStream().use { input.copyTo(it) } }
        Os.chmod(destination.absolutePath, 0x1C0) // 0700: executable only by this app.
    }

    private fun agent(root: File): String {
        // A setup can run while the agent is active. Never truncate a running
        // ELF file, and never reuse an older APK's installer after an update.
        val path = "var/lib/1loop/bin/agent-$agentDigest"
        val executable = File(root, path)
        if (!executable.isFile) {
            executable.parentFile!!.mkdirs()
            val pending = File(executable.parentFile, "agent-${UUID.randomUUID()}.partial")
            asset("local/1loop-server", pending)
            Os.rename(pending.absolutePath, executable.absolutePath)
        }
        return "/$path"
    }

    private fun privateText(file: File, content: String) {
        file.parentFile!!.mkdirs()
        file.writeText(content)
        Os.chmod(file.absolutePath, 0x180) // 0600: tokens and state never become public.
    }

    fun hasWork(): Boolean = startup.isLocked || preparation.isLocked || engine.get() != null || setupProcess.get() != null

    override fun stop() {
        phone.revoke()
        synchronized(lifecycle) {
            val process = engine.getAndSet(null)
            val setup = setupProcess.get()
            val nativeBridge = bridge.getAndSet(null)
            val server = mutableServer.value
            mutableServer.value = null
            terminals.values.forEach { it.destroyForcibly() }
            terminals.clear()
            cancelledTerminals.clear()
            nativeBridge?.close()
            setup?.destroyForcibly()
            val previous = stopping
            stopping = scope.launch {
                previous?.join()
                if (setup != null) {
                    withTimeoutOrNull(5000) { runInterruptible { setup.waitFor() } }
                    if (!setup.isAlive) setupProcess.compareAndSet(setup, null)
                }
                if (server != null) runCatching { Api(server).post("/api/local/shutdown", JSONObject()) }
                if (process != null) {
                    // Let the agent stop its tools before terminating PRoot.
                    withTimeoutOrNull(5000) { runInterruptible { process.waitFor() } }
                    if (process.isAlive) {
                        process.destroy()
                        withTimeoutOrNull(1000) { runInterruptible { process.waitFor() } }
                    }
                    if (process.isAlive) process.destroyForcibly()
                }
                browser.close()
                LocalRuntimeService.releaseIfIdle(context)
            }
            // Publish the new epoch only after a subsequent start can see
            // the shutdown job and cannot reuse the old endpoint.
            generation.incrementAndGet()
        }
    }

    override suspend fun setAdbAllowed(allowed: Boolean) = phone.setAllowed(allowed)
    override fun openShizuku() {
        val intent = context.packageManager.getLaunchIntentForPackage("moe.shizuku.privileged.api")
            ?: Intent(Intent.ACTION_VIEW, android.net.Uri.parse("https://shizuku.rikka.app/download/"))
        context.startActivity(intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
    }

    fun refreshPhoneAccess() { phone.refresh() }

    companion object {
        @Volatile private var instance: AndroidLocalRuntime? = null
        fun get(context: Context): AndroidLocalRuntime = instance ?: synchronized(this) {
            instance ?: AndroidLocalRuntime(context.applicationContext).also { instance = it }
        }
    }
}
