package io.oneloop.remote

import kotlinx.coroutines.flow.StateFlow

enum class ExecutionTarget { RemoteServer, RemoteDesktop, LocalPhone }

enum class StepState { Missing, Running, Done, Failed }

data class SetupStep(
    val id: String,
    val title: String,
    val detail: String,
    val state: StepState,
    val required: Boolean,
    val canPrepare: Boolean = true,
)

data class LocalStatus(val ready: Boolean, val steps: List<SetupStep>)

/** Settings → Linux: what Debian takes on this phone, in bytes. */
data class LinuxUsage(
    val total: Long, val projects: Long, val packageFiles: Long, val agentData: Long, val phoneFree: Long, val phoneTotal: Long,
)

/** An installed Debian package; a protected one is needed by Debian or by 1loop. */
data class LinuxPackage(val name: String, val version: String, val size: Long, val manual: Boolean, val protected: Boolean, val summary: String)

/** What Clean up removes: the downloaded package files, 1loop's older agents, the packages nothing needs any more. */
data class LinuxCleanup(val packageFiles: Long, val oldVersions: Long, val packages: List<String>)

data class LocalAdbState(
    val allowed: Boolean = false,
    val connected: Boolean = false,
    val busy: Boolean = false,
    val problem: String? = null,
    val installed: Boolean = false,
    val serviceRunning: Boolean = false,
)

/** The UI uses the existing authenticated HTTP API after start succeeds. */
interface LocalRuntime {
    val status: StateFlow<LocalStatus>
    val adb: StateFlow<LocalAdbState>
    suspend fun prepare(stepId: String)
    suspend fun start(): Server
    fun stop()
    suspend fun setAdbAllowed(allowed: Boolean)
    fun openShizuku()
}
