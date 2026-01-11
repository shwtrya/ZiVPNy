package com.zivpn.app.ui.settings

import androidx.lifecycle.ViewModel
import com.zivpn.app.data.local.UserPreferences
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

data class SettingsUiState(
    val mtu: Int = MtuConfig.DEFAULT_MTU,
    val validationError: MtuValidationError? = null,
    val saved: Boolean = false
)

@HiltViewModel
class SettingsViewModel @Inject constructor(
    private val userPreferences: UserPreferences
) : ViewModel() {

    private val _uiState = MutableStateFlow(SettingsUiState(mtu = userPreferences.getMtu()))
    val uiState: StateFlow<SettingsUiState> = _uiState.asStateFlow()

    fun saveMtu(rawValue: String) {
        val value = rawValue.trim().toIntOrNull()
        if (value == null) {
            _uiState.update {
                it.copy(
                    validationError = MtuValidationError.INVALID_NUMBER,
                    saved = false
                )
            }
            return
        }

        if (value !in MtuConfig.MIN_MTU..MtuConfig.MAX_MTU) {
            _uiState.update {
                it.copy(
                    validationError = MtuValidationError.OUT_OF_RANGE,
                    saved = false
                )
            }
            return
        }

        userPreferences.saveMtu(value)
        _uiState.update {
            it.copy(
                mtu = value,
                validationError = null,
                saved = true
            )
        }
    }

    fun clearSavedFlag() {
        _uiState.update { it.copy(saved = false) }
    }
}
