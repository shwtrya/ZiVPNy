package com.zivpn.app.data.local

import android.content.Context
import com.zivpn.app.data.model.KeepAliveConfig
import com.zivpn.app.data.model.MtuConfig
import dagger.hilt.android.qualifiers.ApplicationContext
import javax.inject.Inject
import javax.inject.Singleton

@Singleton
class UserPreferences @Inject constructor(
    @ApplicationContext context: Context
) {
    private val prefs = context.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)

    fun getMtu(): Int {
        return prefs.getInt(KEY_MTU, MtuConfig.DEFAULT_MTU)
            .coerceIn(MtuConfig.MIN_MTU, MtuConfig.MAX_MTU)
    }

    fun saveMtu(value: Int) {
        val clampedValue = value.coerceIn(MtuConfig.MIN_MTU, MtuConfig.MAX_MTU)
        prefs.edit().putInt(KEY_MTU, clampedValue).apply()
    }

    fun getKeepAliveSeconds(): Int {
        return prefs.getInt(KEY_KEEPALIVE, KeepAliveConfig.DEFAULT_SECONDS)
            .coerceIn(KeepAliveConfig.MIN_SECONDS, KeepAliveConfig.MAX_SECONDS)
    }

    fun saveKeepAliveSeconds(value: Int) {
        val clampedValue = value.coerceIn(KeepAliveConfig.MIN_SECONDS, KeepAliveConfig.MAX_SECONDS)
        prefs.edit().putInt(KEY_KEEPALIVE, clampedValue).apply()
    }

    companion object {
        private const val PREFS_NAME = "zivpn_prefs"
        private const val KEY_MTU = "settings_mtu"
        private const val KEY_KEEPALIVE = "settings_keepalive"
    }
}
