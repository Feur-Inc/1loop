package io.oneloop.remote

import android.content.Context

// What the user types is never lost: each conversation keeps its own draft,
// saved as it is typed (closing the app or a crash loses nothing), and its
// own undo history while the app runs. The desktop does the same
// (prompt_drafts.go).

class PromptDrafts(context: Context) {
    private val prefs = context.getSharedPreferences("1loop-drafts", Context.MODE_PRIVATE)
    private val histories = HashMap<String, History>()

    init {
        // A send interrupted before acknowledgement stays recoverable after restart.
        prefs.all.filterKeys { it.startsWith("pending:") || it.startsWith("sending:") }.forEach { (key, value) ->
            val destination = if (key.startsWith("sending:")) key.substringAfter('|') else key.removePrefix("pending:")
            restore(destination, value as? String ?: "")
            save(key, "")
        }
    }

    /** One draft's earlier and later texts; typing in one burst is one step. */
    class History {
        val undo = ArrayDeque<String>()
        val redo = ArrayDeque<String>()
        var last = 0L

        fun record(before: String, merge: Boolean = true) {
            val now = System.currentTimeMillis()
            redo.clear()
            if (merge && undo.isNotEmpty() && now - last < 700) {
                last = now
                return
            }
            last = if (merge) now else 0L
            undo.addLast(before)
            if (undo.size > 200) undo.removeFirst()
        }
    }

    fun get(key: String): String = prefs.getString(key, null).orEmpty()

    fun history(key: String): History = histories.getOrPut(key) { History() }

    fun save(key: String, text: String) {
        if (text.isEmpty()) prefs.edit().remove(key).commit() else prefs.edit().putString(key, text).commit()
    }

    /** A text the user wrote comes back (a send that failed): kept, never over what was typed since. */
    fun restore(key: String, text: String) {
        val now = get(key)
        if (now == text) return
        history(key).record(now, merge = false)
        save(key, if (now.isEmpty()) text else "$now\n\n$text")
    }

    fun undo(key: String, current: String): String? {
        val history = history(key)
        val text = history.undo.removeLastOrNull() ?: return null
        history.redo.addLast(current)
        history.last = 0L
        save(key, text)
        return text
    }

    fun redo(key: String, current: String): String? {
        val history = history(key)
        val text = history.redo.removeLastOrNull() ?: return null
        history.undo.addLast(current)
        history.last = 0L
        save(key, text)
        return text
    }
}
