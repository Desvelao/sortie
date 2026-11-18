package app.sortie.core

/** A problem the user can read and act on (invalid profile, bad template, unusable path...). */
open class SorterException(message: String, cause: Throwable? = null) : Exception(message, cause)

/** Thrown inside long operations when their [Cancel] token was cancelled. */
class CancelledException : Exception("cancelled")

/** Cooperative cancellation token for scans, hashing and copies. */
class Cancel {
    @Volatile
    var cancelled: Boolean = false
        private set

    fun cancel() {
        cancelled = true
    }

    fun check() {
        if (cancelled) throw CancelledException()
    }

    companion object {
        /** A token that is never cancelled. */
        val NONE: Cancel get() = Cancel()
    }
}
