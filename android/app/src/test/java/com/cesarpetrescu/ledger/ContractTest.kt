package com.cesarpetrescu.ledger

import org.junit.Assert.*
import org.junit.Test

class ContractTest {
    @Test fun serverAndSessionBoundaries() {
        assertEquals("https://ledger.example.com", serverOrigin(" HTTPS://LEDGER.EXAMPLE.COM:443/ "))
        assertEquals("https://localhost:8443", serverOrigin("https://localhost:8443"))
        assertEquals("https://[::1]:8443", serverOrigin("https://[::1]:8443/"))
        listOf("http://example.com", "https://user:pass@example.com", "https://example.com/admin", "https://example.com?x=1", "https://example.com#x", "https://example.com:0", "https://example.com:65536", "https://example.com\\@evil.test", "javascript:alert(1)").forEach {
            assertThrows(IllegalArgumentException::class.java) { serverOrigin(it) }
        }
        val value = "a".repeat(43)
        assertEquals("ledger_admin_session=$value", sessionCookie(listOf("other=abc; Secure", "ledger_admin_session=$value; Path=/admin; Secure; HttpOnly; SameSite=Strict")))
        listOf("ledger_admin_session=$value; Path=/admin", "ledger_admin_session=$value; Path=/; Secure", "ledger_admin_session=bad; Path=/admin; Secure").forEach {
            assertThrows(IllegalStateException::class.java) { sessionCookie(listOf(it)) }
        }
        assertEquals("a%2Fb%20%26%3F%23", segment("a/b &?#"))
        assertArrayEquals(byteArrayOf(1, 2), byteArrayOf(1, 2).inputStream().readBounded(2))
        assertThrows(IllegalArgumentException::class.java) { byteArrayOf(1, 2, 3).inputStream().readBounded(2) }
    }
    @Test fun formBoundariesMatchServerValidation() {
        listOf("atlas", "a-", "a1", "a".repeat(64)).forEach { assertTrue(validProjectSlug(it)) }
        listOf("-atlas", "--", "a", "a".repeat(65), "Atlas").forEach { assertFalse(validProjectSlug(it)) }
        assertTrue(validFieldText("😀".repeat(100), 100, false))
        assertFalse(validFieldText("😀".repeat(101), 100, false))
        assertFalse(validFieldText("target\nother", 100, false))
        assertTrue(validFieldText("line\nline", 100, true))
        assertTrue(validFieldText("a".repeat(200), 200, false))
        assertFalse(validFieldText("a".repeat(201), 200, false))
        assertTrue(canRetarget("draft"))
        assertTrue(canRetarget("ready"))
        listOf("in_progress", "blocked", "done").forEach { assertFalse(canRetarget(it)) }
        assertFalse(canRetarget("ready", claimed = true))
    }
    @Test fun messageStateTransitionsMatchOwnerConsole() {
        assertEquals(listOf("publish"), messageActions("draft", "unseen"))
        assertEquals(listOf("acknowledge", "claim"), messageActions("ready", "unseen"))
        assertEquals(listOf("block", "complete", "release"), messageActions("in_progress", "seen"))
        assertEquals(listOf("complete", "release"), messageActions("blocked", "seen"))
        assertEquals(listOf("reopen"), messageActions("done", "seen"))
    }
}
