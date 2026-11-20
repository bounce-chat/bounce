package chat.bounce.ui.theme

import android.content.Context
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/** Which colour scheme to render, independent of what the system is doing. */
enum class ThemeChoice {
    /** Track the system setting. The default, and what most people should keep. */
    SYSTEM,
    LIGHT,
    DARK,
    ;

    companion object {
        fun fromStored(name: String?): ThemeChoice =
            entries.firstOrNull { it.name == name } ?: SYSTEM
    }
}

/**
 * The user's theme override.
 *
 * Deliberately *not* an engine setting, unlike everything else on the settings
 * screen. Engine settings replicate to every device in the group, and a phone
 * forced to dark has no business darkening the desktop client - the whole point
 * of the override is that it is about this screen. So it lives in
 * SharedPreferences and never leaves the device.
 *
 * [load] is called from `BounceApplication.onCreate`, before any composition, so
 * the first frame is already the right scheme. Reading a handful of bytes off
 * disk on the main thread is what `AppCompatDelegate` does for the same reason:
 * deferring it trades a guaranteed flash of the wrong theme for a saving too
 * small to measure.
 */
object ThemePreference {

    private const val PREFS = "bounce.ui"
    private const val KEY_THEME = "theme_choice"

    private val _choice = MutableStateFlow(ThemeChoice.SYSTEM)
    val choice: StateFlow<ThemeChoice> = _choice.asStateFlow()

    private var prefs: android.content.SharedPreferences? = null

    fun load(context: Context) {
        val store = context.applicationContext.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        prefs = store
        _choice.value = ThemeChoice.fromStored(store.getString(KEY_THEME, null))
    }

    fun set(value: ThemeChoice) {
        // State first: the recomposition is the thing the user is waiting on,
        // and apply() writes to disk on a background thread anyway.
        _choice.value = value
        prefs?.edit()?.putString(KEY_THEME, value.name)?.apply()
    }
}
