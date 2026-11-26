// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Test

class ServerDevicesTest {
    @Test
    fun readsThePairedDevicesAndWhichIsThisPhone() {
        val listed = JSONObject(
            """{"devices":[{"id":"0123456789abcdef","name":"1loop desktop","added":"2026-11-13T17:01:02.5+01:00","current":false},
               {"id":"fedcba9876543210","name":"Pixel","added":"2026-11-12T09:00:00Z","current":true}]}""",
        )
        assertEquals(
            listOf(
                ServerDevice("0123456789abcdef", "1loop desktop", "2026-11-13", false),
                ServerDevice("fedcba9876543210", "Pixel", "2026-11-12", true),
            ),
            serverDevices(listed),
        )
        assertEquals(emptyList<ServerDevice>(), serverDevices(JSONObject("{}")))
    }

    @Test
    fun aDeviceIdStaysInItsPathSegment() {
        assertEquals("/api/devices/0123456789abcdef/revoke", revokePath("0123456789abcdef"))
        assertEquals("/api/devices/..%2Fstate/revoke", revokePath("../state"))
    }

    // #443: Unlink asks first, and says what it does to this phone and to a computer.
    @Test
    fun unlinkSaysWhatLeaves() {
        assertEquals("This phone's projects and chats leave the menu. Link it again with Link a target.", unlinkText(LinkedTarget(PHONE_TARGET, "This phone", TargetKind.Phone)))
        assertEquals("Office PC is forgotten on this phone: link it again to use it.", unlinkText(LinkedTarget("k", "Office PC", TargetKind.Desktop)))
    }
}
