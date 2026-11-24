// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Test

class BadgeCardTest {
    private val run = RunStatus("c1", "Chat", "Working", "think", emptyList(), phoneUse = false)
    private val approval = Approval("c1", "a1", "Run git push", "git push origin dev")
    private val question = Questions("c1", "q1", listOf(Question("q", "", "Which?", listOf(QuestionOption("A", "")))))
    private val wrap = WrapUp("Added the card.", "Open the PR?", listOf(WrapUpAction("Open it", "")), true)

    @Test
    fun nothingWaitingShowsThePreview() {
        assertEquals(BadgeCard.Preview, run.card)
        assertNull(run.waiting)
    }

    @Test
    fun anApprovalOpensTheBadgeOnIt() {
        val waiting = run.copy(approval = approval)
        assertEquals(BadgeCard.Approval, waiting.card)
        assertEquals("approval:a1", waiting.waiting)
        // A new approval is something new: a badge put away comes back for it.
        assertNotEquals(waiting.waiting, run.copy(approval = approval.copy(id = "a2")).waiting)
    }

    @Test
    fun theNextStepCardShowsOnTheFinishedBadge() {
        val done = run.copy(finished = true, nextStep = wrap)
        assertEquals(BadgeCard.NextStep, done.card)
        assertEquals("next:Open the PR?", done.waiting)
    }

    @Test
    fun aQuestionComesFirst() {
        assertEquals(BadgeCard.Question, run.copy(question = question, approval = approval, nextStep = wrap).card)
        assertEquals(BadgeCard.Approval, run.copy(approval = approval, nextStep = wrap).card)
    }
}
