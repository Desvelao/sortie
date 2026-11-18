package app.sortie.copy

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.IBinder
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat
import app.sortie.App
import app.sortie.MainActivity
import app.sortie.R
import app.sortie.core.Cancel
import app.sortie.core.CancelledException
import app.sortie.core.ConflictPolicy
import app.sortie.core.CopyEvent
import app.sortie.core.CopyJob
import app.sortie.core.CopyOptions
import app.sortie.core.CopyResult
import app.sortie.core.DestCheck
import app.sortie.core.Sorter
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

sealed class CopyState {
    data object Idle : CopyState()
    data class Planning(val done: Int, val total: Int) : CopyState()

    /** What a run would do; nothing has been written. */
    data class Planned(val all: Boolean, val jobs: List<CopyJob>, val checks: List<DestCheck>) : CopyState()
    data class Running(val done: Int, val total: Int, val name: String, val bytes: Long, val bytesTotal: Long, val cancelling: Boolean) : CopyState()
    data class Finished(val results: List<CopyResult>) : CopyState()
    data class Failed(val message: String) : CopyState()
}

/**
 * Plans and runs copies outside any screen, so a batch keeps going when the user leaves the app.
 * While a run is active a foreground service ([CopyService]) keeps the process alive and shows
 * the progress.
 */
class CopyController(private val context: Context, private val sorter: Sorter, private val scope: CoroutineScope) {
    private val _state = MutableStateFlow<CopyState>(CopyState.Idle)
    val state: StateFlow<CopyState> = _state

    /** Called after a run changed what is at the destinations. */
    var onFinished: (() -> Unit)? = null

    private var cancel: Cancel? = null
    private var job: Job? = null

    val busy get() = _state.value is CopyState.Running || _state.value is CopyState.Planning

    /** Evaluates the current file's jobs, or everyone's, without writing anything. */
    fun plan(all: Boolean) {
        if (busy) return
        val c = Cancel().also { cancel = it }
        _state.value = CopyState.Planning(0, 0)
        job = scope.launch(Dispatchers.IO) {
            try {
                val jobs = sorter.copyJobs(all)
                val checks = sorter.planCopy(jobs, c) { done, total -> _state.value = CopyState.Planning(done, total) }
                _state.value = if (c.cancelled) CopyState.Idle else CopyState.Planned(all, jobs, checks)
            } catch (e: Exception) {
                _state.value = CopyState.Failed(e.message ?: e.toString())
            }
        }
    }

    fun start(policy: ConflictPolicy, confirmReplace: Boolean) {
        val planned = _state.value as? CopyState.Planned ?: return
        val jobs = planned.jobs
        val c = Cancel().also { cancel = it }
        _state.value = CopyState.Running(0, jobs.size, "", 0, 0, false)
        runCatching { ContextCompat.startForegroundService(context, Intent(context, CopyService::class.java)) }
        job = scope.launch(Dispatchers.IO) {
            try {
                val opts = CopyOptions(policy, confirmReplace) { ev ->
                    _state.update { s ->
                        val cancelling = (s as? CopyState.Running)?.cancelling ?: false
                        when (ev) {
                            is CopyEvent.Start -> CopyState.Running(ev.done, ev.total, ev.job.name, 0, ev.job.size, cancelling)
                            is CopyEvent.Bytes -> CopyState.Running(ev.done, ev.total, ev.job.name, ev.bytes, ev.bytesTotal, cancelling)
                            is CopyEvent.Done -> CopyState.Running(ev.done, ev.total, ev.result.job.name, 0, 0, cancelling)
                        }
                    }
                }
                val sum = sorter.runCopy(jobs, opts, c)
                _state.value = CopyState.Finished(sum.results)
            } catch (e: CancelledException) {
                _state.value = CopyState.Idle
            } catch (e: Exception) {
                _state.value = CopyState.Failed(e.message ?: e.toString())
            }
            onFinished?.invoke()
        }
    }

    /** Stops planning, or aborts a run: running copies remove their temporary files. */
    fun cancel() {
        cancel?.cancel()
        _state.update { if (it is CopyState.Running) it.copy(cancelling = true) else it }
    }

    /** Back to idle once a plan or a result has been read. */
    fun dismiss() {
        if (!busy) _state.value = CopyState.Idle
    }
}

/** Keeps the process alive during a copy run and shows its progress; it does no copying itself. */
class CopyService : Service() {
    private val scope = CoroutineScope(Dispatchers.Main.immediate)

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        val controller = (application as App).container.copy
        val nm = getSystemService(NotificationManager::class.java)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            nm.createNotificationChannel(NotificationChannel(CHANNEL, getString(R.string.copy_channel), NotificationManager.IMPORTANCE_LOW))
        }
        val type = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC else 0
        try {
            ServiceCompat.startForeground(this, NOTIFICATION, notification(controller.state.value), type)
        } catch (_: Exception) {
            // not allowed to start in the foreground right now: the copy still runs while the app is open
            stopSelf()
            return START_NOT_STICKY
        }
        scope.launch {
            controller.state.collectLatest { s ->
                if (s is CopyState.Running) {
                    nm.notify(NOTIFICATION, notification(s))
                } else {
                    ServiceCompat.stopForeground(this@CopyService, ServiceCompat.STOP_FOREGROUND_REMOVE)
                    stopSelf()
                }
            }
        }
        return START_NOT_STICKY
    }

    override fun onDestroy() {
        scope.cancel()
        super.onDestroy()
    }

    private fun notification(s: CopyState): Notification {
        val open = PendingIntent.getActivity(this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE)
        val b = NotificationCompat.Builder(this, CHANNEL)
            .setSmallIcon(R.drawable.ic_copy)
            .setContentTitle(getString(R.string.copy_notification_title))
            .setContentIntent(open)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
        if (s is CopyState.Running) {
            b.setContentText(getString(R.string.copy_progress, s.done, s.total))
            b.setProgress(s.total.coerceAtLeast(1), s.done, s.total == 0)
        }
        return b.build()
    }

    private companion object {
        const val CHANNEL = "copy"
        const val NOTIFICATION = 1
    }
}
