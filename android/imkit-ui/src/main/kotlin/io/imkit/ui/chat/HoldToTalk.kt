package io.imkit.ui.chat

import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.runtime.getValue
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.Modifier
import androidx.compose.ui.composed
import androidx.compose.ui.input.pointer.pointerInput

/**
 * Press, slide and release as **one** gesture (C8).
 *
 * The source app once split this into a tap detector and a drag detector.
 * The drag detector consumed the moves, which made the tap detector see the
 * press as cancelled the moment the finger drifted past touch slop, and the
 * clip was sent mid-sentence. With a single `awaitEachGesture` nothing else
 * consumes the pointer, so [onRelease] runs exactly once per press: on the
 * real finger-up, or when the system cancels the gesture.
 *
 * [onDrag] gets the vertical offset from the press point (negative upwards,
 * towards the cancel zone).
 */
internal fun Modifier.holdToTalk(
    onPress: () -> Unit,
    onDrag: (Float) -> Unit,
    onRelease: () -> Unit,
): Modifier = composed {
    val press by rememberUpdatedState(onPress)
    val drag by rememberUpdatedState(onDrag)
    val release by rememberUpdatedState(onRelease)
    pointerInput(Unit) {
        awaitEachGesture {
            val down = awaitFirstDown(requireUnconsumed = false)
            down.consume()
            press()
            try {
                while (true) {
                    val event = awaitPointerEvent()
                    val change = event.changes.firstOrNull { it.id == down.id } ?: continue
                    if (!change.pressed) break
                    drag(change.position.y - down.position.y)
                    change.consume()
                }
            } finally {
                release()
            }
        }
    }
}
