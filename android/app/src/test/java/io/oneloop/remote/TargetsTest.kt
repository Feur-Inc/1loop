// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class TargetsTest {
    private val desk = Server("Desk", "192.168.1.20", 47100, token = "t1", fingerprint = "aa", desktop = true)
    private val box = Server("Box", "box.example", 47101, token = "t2", fingerprint = "bb")
    private val unpaired = Server("New", "192.168.1.30", 47100, desktop = true)

    private val phone = LinkedTarget(PHONE_TARGET, "This phone", TargetKind.Phone)
    private val deskTarget = LinkedTarget(desk.key, "Desk", TargetKind.Desktop)
    private val boxTarget = LinkedTarget(box.key, "Box", TargetKind.Server)

    private fun chat(id: String, workspace: String, scratch: Boolean = false) = ChatSummary(id, id, workspace, false, scratch)

    @Test
    fun linksThisPhoneThenDesktopsThenServers() {
        assertEquals(listOf(phone, deskTarget, boxTarget), linkedTargets(true, "This phone", listOf(box, unpaired, desk)))
        // Only paired computers are linked; the phone once its engine ran.
        assertEquals(listOf(deskTarget, boxTarget), linkedTargets(false, "This phone", listOf(box, unpaired, desk, desk)))
        assertEquals(emptyList<LinkedTarget>(), linkedTargets(false, "This phone", listOf(unpaired)))
    }

    // Regression for #380: the drawer listed only the open target's projects.
    @Test
    fun listsEveryLinkedTargetsProjectsTogether() {
        val groups = targetGroups(
            listOf(phone, deskTarget, boxTarget), active = desk.key,
            live = TargetListing(listOf("/home/m/app"), listOf(chat("c1", "/home/m/app"))),
            listings = mapOf(PHONE_TARGET to TargetListing(listOf("/projects/notes"), listOf(chat("p1", "/projects/notes")))),
            reaches = mapOf(PHONE_TARGET to Reach.Online, box.key to Reach.Offline),
            remembered = mapOf(box.key to listOf("/srv/site")),
        )
        assertEquals(listOf(PHONE_TARGET, desk.key, box.key), groups.map { it.target.key })
        assertEquals(listOf(listOf("/projects/notes"), listOf("/home/m/app"), listOf("/srv/site")), groups.map { it.projects })
        assertEquals(listOf(false, true, false), groups.map { it.active })
        val drawer = drawerGroups(groups, noFolderSelected = false)
        assertEquals(listOf("notes", "app", "site"), drawer.map { it.name })
        assertEquals(listOf(listOf("p1"), listOf("c1"), emptyList()), drawer.map { group -> group.chats.map { it.chat.id } })
        assertEquals(listOf(PHONE_TARGET, desk.key), drawer.flatMap { group -> group.chats.map { it.target } })
    }

    @Test
    fun keepsAnOfflineTargetListedWithItsRememberedProjects() {
        val groups = targetGroups(listOf(phone, boxTarget), active = PHONE_TARGET, live = TargetListing(), listings = emptyMap(),
            reaches = mapOf(box.key to Reach.Offline), remembered = mapOf(box.key to listOf("/srv/site", "/srv/api")))
        val box = groups.single { it.target.key == boxTarget.key }
        assertEquals(Reach.Offline, box.reach)
        assertFalse(box.known) // its chats show once it is back
        assertEquals(listOf("/srv/site", "/srv/api"), box.projects)
        assertEquals("offline", offlineMark(TargetKind.Server))
        assertEquals("stopped", offlineMark(TargetKind.Phone))
    }

    @Test
    fun leavesOutAnEmptyTargetButTheOpenOne() {
        val groups = targetGroups(listOf(phone, deskTarget), active = PHONE_TARGET, live = TargetListing(), listings = emptyMap(), reaches = emptyMap(), remembered = emptyMap())
        assertEquals(listOf(PHONE_TARGET), groups.map { it.target.key })
    }

    @Test
    fun theOpenTargetShowsItsLastListingUntilItsOwnIsRead() {
        val last = TargetListing(listOf("/home/m/app"))
        val groups = targetGroups(listOf(deskTarget), active = desk.key, live = null, listings = mapOf(desk.key to last),
            reaches = mapOf(desk.key to Reach.Online), remembered = emptyMap())
        assertEquals(listOf("/home/m/app"), groups.single().projects)
    }

    @Test
    fun marksProjectsNotOnThisPhone() {
        assertNull(projectMark(TargetKind.Phone))
        assertEquals(TargetKind.Desktop, projectMark(TargetKind.Desktop))
        assertEquals(TargetKind.Server, projectMark(TargetKind.Server))
    }

    @Test
    fun aChatWithNoFolderRunsOnTheDeviceInUse() {
        assertEquals(PHONE_TARGET, noFolderTarget(listOf(phone, deskTarget), active = desk.key))
        assertEquals(box.key, noFolderTarget(listOf(deskTarget, boxTarget), active = box.key))
        assertEquals(desk.key, noFolderTarget(listOf(deskTarget, boxTarget), active = null))
        assertNull(noFolderTarget(emptyList(), active = null))
    }

    @Test
    fun startsOnTheLastTargetElseTheDeviceInUse() {
        val linked = listOf(phone, deskTarget, boxTarget)
        assertEquals(box.key, startTarget(linked, last = box.key, phoneReady = true))
        assertEquals(PHONE_TARGET, startTarget(linked, last = "gone", phoneReady = true))
        assertEquals(desk.key, startTarget(linked, last = null, phoneReady = false)) // the phone needs its setup: another first
        assertEquals(PHONE_TARGET, startTarget(listOf(phone), last = null, phoneReady = false))
        assertNull(startTarget(emptyList(), last = desk.key, phoneReady = true)) // first-time setup
    }

    @Test
    fun groupsNoFolderChatsOfEveryTargetFirst() {
        val groups = listOf(
            TargetGroup(phone, Reach.Online, true, listOf("/p/a"), listOf(chat("s1", "/scratch/1", scratch = true), chat("a1", "/p/a")), true),
            TargetGroup(deskTarget, Reach.Online, false, listOf("/h/a", "/h/a-wt"), listOf(chat("s2", "/x", scratch = true), chat("w1", "/h/a-wt"), chat("o1", "/elsewhere")), true, mapOf("/h/a-wt" to "/h/a")),
        )
        val drawer = drawerGroups(groups, noFolderSelected = false)
        assertEquals(listOf(SCRATCH_GROUP, "/p/a", "${desk.key}|/h/a", "${desk.key}|$OTHER_GROUP"), drawer.map { it.key })
        assertEquals(listOf(TargetChat(PHONE_TARGET, groups[0].chats[0]), TargetChat(desk.key, groups[1].chats[0])), drawer[0].chats)
        assertEquals(listOf("w1"), drawer[2].chats.map { it.chat.id }) // a worktree's chat under its project
        assertEquals(listOf("o1"), drawer[3].chats.map { it.chat.id })
        assertTrue(drawerGroups(listOf(groups[0].copy(chats = emptyList())), noFolderSelected = true).first().key == SCRATCH_GROUP)
    }

    @Test
    fun remembersProjectsByTarget() {
        val remembered = mapOf(box.key to listOf("/srv/site"), PHONE_TARGET to emptyList())
        assertEquals(remembered, parseRemembered(rememberedText(remembered)))
        assertEquals(emptyMap<String, List<String>>(), parseRemembered(null))
        assertEquals(emptyMap<String, List<String>>(), parseRemembered("not json"))
    }
}
