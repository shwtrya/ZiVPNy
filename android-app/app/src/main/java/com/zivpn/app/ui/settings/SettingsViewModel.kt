package com.zivpn.app.ui.settings

import androidx.lifecycle.ViewModel
import com.zivpn.app.data.local.UserPreferences
import com.zivpn.app.data.model.KeepAliveConfig
import com.zivpn.app.data.model.MtuConfig
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import javax.inject.Inject

enum class MtuValidationError {
    INVALID_NUMBER,
    OUT_OF_RANGE
}

enum class KeepAliveValidationError {
    INVALID_NUMBER,
    OUT_OF_RANGE
}

data class SettingsUiState(
    val mtu: Int = MtuConfig.DEFAULT_MTU,
    val keepAliveSeconds: Int = KeepAliveConfig.DEFAULT_SECONDS,
    val validationError: MtuValidationError? = null,
    val keepAliveValidationError: KeepAliveValidationError? = null,
    val saved: Boolean = false,
    val keepAliveSaved: Boolean = false
)

@HiltViewModel
class SettingsViewModel @Inject constructor(
    private val userPreferences: UserPreferences
) : ViewModel() {

    private val _uiState = MutableStateFlow(
        SettingsUiState(
            mtu = userPreferences.getMtu(),
            keepAliveSeconds = userPreferences.getKeepAliveSeconds()
        )
    )
    val uiState: StateFlow<SettingsUiState> = _uiState.asStateFlow()

    fun saveMtu(rawValue: String) {
        val value = rawValue.trim().toIntOrNull()
        if (value == null) {
            _uiState.update {
                it.copy(
                    validationError = MtuValidationError.INVALID_NUMBER,
                    saved = false,
                    keepAliveSaved = false
                )
            }
            return
        }

        if (value !in MtuConfig.MIN_MTU..MtuConfig.MAX_MTU) {
            _uiState.update {
                it.copy(
                    validationError = MtuValidationError.OUT_OF_RANGE,
                    saved = false,
                    keepAliveSaved = false
                )
            }
            return
        }

        userPreferences.saveMtu(value)
        _uiState.update {
            it.copy(
                mtu = value,
                validationError = null,
                saved = true,
                keepAliveSaved = false
            )
        }
    }

    fun saveKeepAlive(rawValue: String) {
        val value = rawValue.trim().toIntOrNull()
        if (value == null) {
            _uiState.update {
                it.copy(
                    keepAliveValidationError = KeepAliveValidationError.INVALID_NUMBER,
                    keepAliveSaved = false,
                    saved = false
                )
            }
            return
        }

        if (value !in KeepAliveConfig.MIN_SECONDS..KeepAliveConfig.MAX_SECONDS) {
            _uiState.update {
                it.copy(
                    keepAliveValidationError = KeepAliveValidationError.OUT_OF_RANGE,
                    keepAliveSaved = false,
                    saved = false
                )
            }
            return
        }

        userPreferences.saveKeepAliveSeconds(value)
        _uiState.update {
            it.copy(
                keepAliveSeconds = value,
                keepAliveValidationError = null,
                keepAliveSaved = true,
                saved = false
            )
        }
    }

    fun clearSavedFlag() {
        _uiState.update { it.copy(saved = false, keepAliveSaved = false) }
    }
}
