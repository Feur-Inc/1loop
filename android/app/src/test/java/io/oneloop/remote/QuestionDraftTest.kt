// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import org.junit.Assert.assertEquals
import org.junit.Test

class QuestionDraftTest {
    private val scope = Question("scope", "Scope", "Which?", listOf(QuestionOption("Both (Recommended)", ""), QuestionOption("Integration", ""), QuestionOption("Add-on", "")))
    private val parts = Question("parts", "Parts", "Which parts?", listOf(QuestionOption("Docs", ""), QuestionOption("Tests", ""), QuestionOption("CI", "")), multiSelect = true)
    private val request = Questions("chat", "r1", listOf(scope, parts))

    @Test
    fun singleChoiceTakesAnOptionOrOwnWords() {
        val draft = QuestionDraft()
        draft.pick(scope, "Integration")
        draft.pick(scope, "Both (Recommended)")
        assertEquals("Both (Recommended)", draft.answer(scope)) // the label as the model wrote it
        draft.write(scope, "  a bit of both ")
        assertEquals(Questions.OTHER + "a bit of both", draft.answer(scope))
        draft.pick(scope, "Add-on")
        assertEquals("Add-on", draft.answer(scope))
        assertEquals("", draft.others[scope.id].orEmpty())
    }

    @Test
    fun multiSelectListsEverythingChosen() {
        val draft = QuestionDraft()
        draft.pick(parts, "CI")
        draft.pick(parts, "Docs")
        draft.pick(parts, "Tests")
        draft.pick(parts, "Tests") // a second tap unticks it
        assertEquals("Docs, CI", draft.answer(parts))
        draft.write(parts, "release notes")
        assertEquals("Docs, CI, release notes", draft.answer(parts))
        draft.pick(parts, "Docs")
        draft.pick(parts, "CI")
        assertEquals(Questions.OTHER + "release notes", draft.answer(parts))
    }

    @Test
    fun sendSaysWhatIsMissing() {
        val draft = QuestionDraft()
        assertEquals("Answer 2 more questions", draft.sendLabel(request))
        draft.pick(parts, "Docs")
        assertEquals("Answer 1 more question", draft.sendLabel(request))
        assertEquals(0, draft.firstMissing(request))
        draft.write(scope, "   ")
        assertEquals("Answer 1 more question", draft.sendLabel(request)) // a blank Other is no answer
        draft.write(scope, "my way")
        assertEquals("Send answers", draft.sendLabel(request))
        assertEquals(-1, draft.firstMissing(request))
        assertEquals(mapOf("scope" to Questions.OTHER + "my way", "parts" to "Docs"), draft.answers(request))
        assertEquals("Choose an answer", QuestionDraft().sendLabel(Questions("chat", "r2", listOf(scope))))
    }

    @Test
    fun recommendedBecomesABadge() {
        assertEquals("Both" to true, recommendedLabel("Both (Recommended)"))
        assertEquals("Both" to true, recommendedLabel("Both (recommended) "))
        assertEquals("Integration" to false, recommendedLabel("Integration"))
        assertEquals("(Recommended)" to false, recommendedLabel("(Recommended)"))
    }
}
