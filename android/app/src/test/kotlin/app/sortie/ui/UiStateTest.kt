package app.sortie.ui

import app.sortie.core.DestCheck
import app.sortie.core.DestStatus
import app.sortie.core.Tag
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class UiStateTest {
    private val work = Tag(1, "Work")
    private val family = Tag(2, "Family")

    private fun state(assigned: Set<Int>, checks: List<DestCheck>) =
        UiState(tags = listOf(work, family), assigned = assigned, checks = checks)

    private fun check(tag: Tag, status: DestStatus) = DestCheck(tagId = tag.id, tagName = tag.name, status = status)

    @Test
    fun copyIsBlockedOnlyWhenNoAssignedDestinationCanBeCopied() {
        assertFalse("nothing tagged", state(emptySet(), emptyList()).currentDestinationBlocked)
        assertFalse("checks still running", state(setOf(1), emptyList()).currentDestinationBlocked)
        assertTrue(state(setOf(1), listOf(check(work, DestStatus.INVALID_PATH))).currentDestinationBlocked)
        assertTrue(state(setOf(1), listOf(check(work, DestStatus.UNRESOLVED_VAR))).currentDestinationBlocked)
        assertFalse(state(setOf(1), listOf(check(work, DestStatus.READY))).currentDestinationBlocked)
        assertFalse(state(setOf(1), listOf(check(work, DestStatus.CONFLICT))).currentDestinationBlocked)
        val bothInvalid = listOf(check(work, DestStatus.INVALID_PATH), check(family, DestStatus.INVALID_PATH))
        assertTrue(state(setOf(1, 2), bothInvalid).currentDestinationBlocked)
        assertFalse(state(setOf(1, 2), listOf(check(work, DestStatus.INVALID_PATH), check(family, DestStatus.READY))).currentDestinationBlocked)
        assertFalse("second check not in yet", state(setOf(1, 2), listOf(check(work, DestStatus.INVALID_PATH))).currentDestinationBlocked)
    }
}
