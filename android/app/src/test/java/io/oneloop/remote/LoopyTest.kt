// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class LoopyTest {
    private fun message(card: String) = LoopyMessage.from(JSONObject("""{"id":"m1","t":"2026-11-24T22:43:31.300628483+01:00","from":"loopy","card":$card}"""))

    @Test fun aCardWaitsUntilItIsAnswered() {
        val card = message("""{"kind":"choice","title":"Trains","options":[{"label":"9 h","aside":"45 €"},{"label":"11 h"}]}""").card!!
        assertTrue(card.waiting)
        assertEquals(listOf("9 h", "11 h"), card.plainActions)
        assertFalse(card.copy(answer = "9 h").waiting)
        assertTrue(card.copy(answer = "9 h").plainActions.isEmpty())
        assertFalse(message("""{"kind":"summary","title":"Today","options":[{"label":"CI","action":"Open"}]}""").card!!.waiting)
    }

    @Test fun aNotificationNeverAnswersWhatIsTyped() {
        assertTrue(message("""{"kind":"signin","title":"Sign in","fields":[{"label":"password"}],"actions":["Sign in","Not now"]}""").card!!.plainActions.isEmpty())
        assertTrue(message("""{"kind":"needs_you","title":"Code","ask":"The code","actions":["Give it","Drop it"]}""").card!!.plainActions.isEmpty())
    }

    @Test fun anAnswerCarriesWhatWasTyped() {
        val m = message("""{"kind":"mail","title":"To Paul","fields":[{"label":"To","value":"a@b.c"}],"body":"Hi","actions":["Send","Discard"],"editable":true}""")
        val body = loopyAnswer(m, "Send", listOf(LoopyField("To", "d@e.f")), "Hello")
        assertEquals("m1", body.getString("message"))
        assertEquals("d@e.f", body.getJSONArray("fields").getJSONObject(0).getString("value"))
        assertEquals("Hello", body.getString("body"))
        assertFalse(body.has("values"))
        val secret = loopyAnswer(m, "Sign in", values = mapOf("user" to "me", "password" to "x"), keep = true, hint = "work")
        assertEquals("x", secret.getJSONObject("values").getString("password"))
        assertTrue(secret.getBoolean("keep"))
    }

    @Test fun schedulesAndFactsInWords() {
        assertEquals("Monday to Friday at 09:30", describeWhen("weekdays 09:30"))
        assertEquals("Every 2h", describeWhen("every 2h"))
        assertEquals("Mon, Thu at 18:00", describeWhen("mon,thu 18:00"))
        assertEquals("Each month on day 1 at 09:00", describeWhen("monthly 1 09:00"))
        val fact = LoopyFact.from(JSONObject("""{"id":"f","text":"No meat","kind":"preference","since":"2026-11-24T10:00:00Z","said":true,"quote":"je mange pas de viande"}"""))
        assertTrue(fact.detail().startsWith("You said so · Since "))
        assertEquals("👍", loopyEmoji("thumbs_up"))
        assertTrue(loopyMarkup("Voici :\n- un"))
        assertFalse(loopyMarkup("Salut 🙂"))
    }
}
