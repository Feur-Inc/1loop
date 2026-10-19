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
