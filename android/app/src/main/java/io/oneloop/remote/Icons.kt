package io.oneloop.remote

import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.PathParser
import androidx.compose.ui.unit.dp

// The desktop's thin line icons (ui.go, lineIcon): the same 16 unit paths,
// stroked at 1.5 with round ends. Rectangles are written as paths here.
private fun lineIcon(name: String, vararg paths: Pair<String, Float>): ImageVector {
    val builder = ImageVector.Builder(name, 24.dp, 24.dp, 16f, 16f)
    for ((data, width) in paths) {
        builder.addPath(
            pathData = PathParser().parsePathString(data).toNodes(),
            stroke = SolidColor(Color.White), strokeLineWidth = width,
            strokeLineCap = StrokeCap.Round, strokeLineJoin = StrokeJoin.Round,
        )
    }
    return builder.build()
}

private fun line(d: String, width: Float = 1.5f) = d to width

object Icons {
    val chevron = lineIcon("chevron", line("M4.5 6.5 8 10l3.5-3.5"))
    val chevronRight = lineIcon("chevron-right", line("M6.5 4.5 10 8l-3.5 3.5"))
    val check = lineIcon("check", line("M3.5 8.4 6.6 11.4 12.5 4.8"))
    val close = lineIcon("close", line("M4.5 4.5l7 7M11.5 4.5l-7 7"))
    val chats = lineIcon("chats", line("M4 3h8a1.5 1.5 0 0 1 1.5 1.5v6A1.5 1.5 0 0 1 12 12H7l-3.5 2v-2A1.5 1.5 0 0 1 2 10.5v-6A1.5 1.5 0 0 1 4 3z"), line("M5 6h6M5 9h4"))
    val plus = lineIcon("plus", line("M8 3v10M3 8h10", 1.8f))
    val copy = lineIcon("copy", line("M7.1 5.5h4.3a1.6 1.6 0 0 1 1.6 1.6v4.3a1.6 1.6 0 0 1-1.6 1.6H7.1a1.6 1.6 0 0 1-1.6-1.6V7.1a1.6 1.6 0 0 1 1.6-1.6z"),
        line("M10.5 5.5V4.2c0-.7-.5-1.2-1.2-1.2H4.2C3.5 3 3 3.5 3 4.2v5.1c0 .7.5 1.2 1.2 1.2h1.3"))
    val folder = lineIcon("folder", line("M2.5 5c0-.8.6-1.5 1.4-1.5h2.7l1.3 1.4h4.2c.8 0 1.4.6 1.4 1.4v4.9c0 .8-.6 1.3-1.4 1.3H3.9c-.8 0-1.4-.5-1.4-1.3z"))
    val send = lineIcon("send", line("M8 13.2V2.9M3.5 7.4 8 2.9l4.5 4.5", 2f))
    val shield = lineIcon("shield", line("M8 2.2l4.6 1.8v3.5c0 3-2 5.1-4.6 6.3-2.6-1.2-4.6-3.3-4.6-6.3V4z"))
    val bolt = lineIcon("bolt", line("M8.9 1.8 3.7 9h4.1l-.7 5.2L12.3 7H8.2z"))
    val shieldCheck = lineIcon("shield-check", line("M8 2.2l4.6 1.8v3.5c0 3-2 5.1-4.6 6.3-2.6-1.2-4.6-3.3-4.6-6.3V4z"), line("M5.9 7.8 7.4 9.3l2.8-3"))
    val plug = lineIcon("plug", line("M6 2.5v3M10 2.5v3M4.5 5.5h7v2.2a3.5 3.5 0 0 1-7 0zM8 11.2v2.3"))
    val terminal = lineIcon("terminal", line("M3.5 4.5 6.5 7.5 3.5 10.5M8 11.5h4.5"))
    val document = lineIcon("document", line("M4 2.8h5l3 3v7.4H4z"), line("M9 2.8v3h3M6 8.6h4M6 10.9h4"))
    val pencil = lineIcon("pencil", line("M3.2 12.8l.7-3 6.6-6.6 2.3 2.3-6.6 6.6zM9.2 4.5l2.3 2.3"))
    val arrowTurn = lineIcon("arrow-turn", line("M4 3.5v3.7c0 1.1.9 2 2 2h6M9.6 6.8 12 9.2l-2.4 2.4"))
    val bookmark = lineIcon("bookmark", line("M4.5 2.5h7v11L8 10.8l-3.5 2.7z"))
    val compact = lineIcon("compact", line("M8 2.5v4M5.8 4.6 8 6.6l2.2-2M8 13.5v-4M5.8 11.4 8 9.4l2.2 2M3 8h10"))
    val computer = lineIcon("computer", line("M3.5 3.5h9a1 1 0 0 1 1 1v6a1 1 0 0 1-1 1h-9a1 1 0 0 1-1-1v-6a1 1 0 0 1 1-1z"), line("M6 13.5h4"))
    val server = lineIcon("server", line("M3.5 2.8h9a1 1 0 0 1 1 1v2.4a1 1 0 0 1-1 1h-9a1 1 0 0 1-1-1V3.8a1 1 0 0 1 1-1zM3.5 8.8h9a1 1 0 0 1 1 1v2.4a1 1 0 0 1-1 1h-9a1 1 0 0 1-1-1V9.8a1 1 0 0 1 1-1z"), line("M5 5h.01M5 11h.01", 2f))
    val phone = lineIcon("phone", line("M5.2 1.8h5.6a1 1 0 0 1 1 1v10.4a1 1 0 0 1-1 1H5.2a1 1 0 0 1-1-1V2.8a1 1 0 0 1 1-1z"), line("M7 12h2"))
    val undo = lineIcon("undo", line("M5.5 4 3 6.5 5.5 9"), line("M3 6.5h6.5a3.5 3.5 0 0 1 0 7H7"))
    val redo = lineIcon("redo", line("M10.5 4 13 6.5 10.5 9"), line("M13 6.5H6.5a3.5 3.5 0 0 0 0 7H9"))
    val settings = lineIcon("settings", line("M7 4.1L7 2.5L9 2.5L9 4.1A4 4 0 0 1 10.8 5.2L12.3 4.4L13.3 6.1L11.9 7A4 4 0 0 1 11.9 9L13.3 9.9L12.3 11.6L10.8 10.8A4 4 0 0 1 9 11.9L9 13.5L7 13.5L7 11.9A4 4 0 0 1 5.2 10.8L3.7 11.6L2.7 9.9L4.1 9A4 4 0 0 1 4.1 7L2.7 6.1L3.7 4.4L5.2 5.2A4 4 0 0 1 7 4.1z"),
        line("M8 6.4a1.6 1.6 0 1 0 0 3.2a1.6 1.6 0 1 0 0-3.2z"))
    val refresh = lineIcon("refresh", line("M12.8 7.2A4.9 4.9 0 1 0 11.4 11"), line("M13 3.6v3.6H9.4"))

    val panel = lineIcon("panel", line("M2.5 2.5h4v4h-4zM9.5 2.5h4v4h-4zM2.5 9.5h4v4h-4zM9.5 9.5h4v4h-4z"))
    val globe = lineIcon("globe", line("M8 2.5a5.5 5.5 0 1 0 0 11a5.5 5.5 0 1 0 0-11z"), line("M2.5 8h11"),
        line("M8 2.5c1.5 1.6 2.2 3.4 2.2 5.5S9.5 11.9 8 13.5C6.5 11.9 5.8 10.1 5.8 8S6.5 4.1 8 2.5z"))
    val back = lineIcon("back", line("M9.5 4.5 6 8l3.5 3.5"))
    val lock = lineIcon("lock", line("M4.6 7.4h6.8a1.1 1.1 0 0 1 1.1 1.1v4a1.1 1.1 0 0 1-1.1 1.1H4.6a1.1 1.1 0 0 1-1.1-1.1v-4a1.1 1.1 0 0 1 1.1-1.1z"), line("M5.6 7.4V5.5a2.4 2.4 0 0 1 4.8 0v1.9"))
    val unlock = lineIcon("unlock", line("M4.6 7.4h6.8a1.1 1.1 0 0 1 1.1 1.1v4a1.1 1.1 0 0 1-1.1 1.1H4.6a1.1 1.1 0 0 1-1.1-1.1v-4a1.1 1.1 0 0 1 1.1-1.1z"), line("M5.6 7.4V5.5a2.4 2.4 0 0 1 4.6-1"))
    val star = lineIcon("star", line("M8 2.3l1.7 3.6 3.9.5-2.9 2.7.7 3.9L8 11.1 4.6 13l.7-3.9-2.9-2.7 3.9-.5z"))
    val trash = lineIcon("trash", line("M3 4.5h10M6.5 4.5V3.2h3v1.3M4.5 4.5l.6 8.3h5.8l.6-8.3M7 7v3.6M9 7v3.6"))
    val tasks = lineIcon("tasks", line("M2.8 4.6l1.3 1.3 2.3-2.4M8.5 4.7h4.7M2.8 10.6l1.3 1.3 2.3-2.4M8.5 10.7h4.7"))

    fun forTool(tool: String) = when (tool) {
        "shell" -> terminal
        "edit_file", "apply_patch" -> pencil
        "list_directory" -> folder
        "memory" -> bookmark
        else -> document
    }
}
