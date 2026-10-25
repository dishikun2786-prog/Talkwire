// SPDX-License-Identifier: AGPL-3.0-only
package io.imkit.e2ee

/**
 * Why an encrypted operation did not happen, in classes a user can act on
 * (red line C10). Only [OFFLINE] is worth retrying as it is; telling a user
 * to "try again later" for the others sends them round in circles.
 */
public enum class E2eeFailure {
    /**
     * There are no keys we may fetch for the peer. The server deliberately
     * does not say whether they never turned encryption on (never published
     * keys) or are not someone we may reach (section 13.1). Read it as "the
     * peer has not turned encryption on" only where the caller already knows
     * the peer is a contact, such as from an existing chat.
     */
    PEER_KEYS_UNAVAILABLE,

    /** The server refused: not a contact, not the group's owner or a manager (401, `not_owner`). */
    NOT_ALLOWED,

    /** The group has not turned encryption on; its owner has to first (409 `group_not_encrypted`). */
    GROUP_NOT_ENCRYPTED,

    /** Encryption is turned off by the host, or the server does not have the module (409 `e2ee_disabled`, 501). */
    DISABLED,

    /** Not connected, or no reply came; the only class where retrying as is helps. */
    OFFLINE,

    /** Keys or a session on this device could not be used (corrupt, or the peer's bundle does not verify). */
    CRYPTO,

    /** The server answered something this client did not expect. */
    SERVER,
    ;

    public val retryable: Boolean get() = this == OFFLINE
}

/** The outcome of an encrypted operation: a value, or a [E2eeFailure] class. */
public sealed interface E2eeResult<out T> {
    public data class Ok<out T>(val value: T) : E2eeResult<T>

    /** [detail] is for logs, never for the user. */
    public data class Failed(val failure: E2eeFailure, val detail: String? = null) : E2eeResult<Nothing>

    public val valueOrNull: T? get() = (this as? Ok)?.value
}

internal inline fun <T, R> E2eeResult<T>.then(block: (T) -> E2eeResult<R>): E2eeResult<R> = when (this) {
    is E2eeResult.Ok -> block(value)
    is E2eeResult.Failed -> this
}
