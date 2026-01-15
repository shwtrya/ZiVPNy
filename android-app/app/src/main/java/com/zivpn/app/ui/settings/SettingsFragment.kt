package com.zivpn.app.ui.settings

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import androidx.fragment.app.Fragment
import androidx.fragment.app.viewModels
import androidx.lifecycle.lifecycleScope
import com.google.android.material.snackbar.Snackbar
import com.zivpn.app.R
import com.zivpn.app.data.model.KeepAliveConfig
import com.zivpn.app.data.model.MtuConfig
import com.zivpn.app.databinding.FragmentSettingsBinding
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.launch

@AndroidEntryPoint
class SettingsFragment : Fragment() {

    private var _binding: FragmentSettingsBinding? = null
    private val binding get() = _binding!!

    private val settingsViewModel: SettingsViewModel by viewModels()

    override fun onCreateView(
        inflater: LayoutInflater,
        container: ViewGroup?,
        savedInstanceState: Bundle?
    ): View {
        _binding = FragmentSettingsBinding.inflate(inflater, container, false)
        return binding.root
    }

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)
        setupListeners()
        observeState()
    }

    private fun setupListeners() {
        binding.btnSaveMtu.setOnClickListener {
            settingsViewModel.saveMtu(binding.etMtu.text?.toString().orEmpty())
        }
        binding.btnSaveKeepalive.setOnClickListener {
            settingsViewModel.saveKeepAlive(binding.etKeepalive.text?.toString().orEmpty())
        }
    }

    private fun observeState() {
        viewLifecycleOwner.lifecycleScope.launch {
            settingsViewModel.uiState.collectLatest { state ->
                if (!binding.etMtu.isFocused) {
                    val currentText = binding.etMtu.text?.toString()
                    val desiredText = state.mtu.toString()
                    if (currentText != desiredText) {
                        binding.etMtu.setText(desiredText)
                    }
                }

                if (!binding.etKeepalive.isFocused) {
                    val currentText = binding.etKeepalive.text?.toString()
                    val desiredText = state.keepAliveSeconds.toString()
                    if (currentText != desiredText) {
                        binding.etKeepalive.setText(desiredText)
                    }
                }

                binding.tilMtu.error = when (state.validationError) {
                    MtuValidationError.INVALID_NUMBER -> getString(R.string.mtu_invalid_number)
                    MtuValidationError.OUT_OF_RANGE -> getString(
                        R.string.mtu_invalid_range,
                        MtuConfig.MIN_MTU,
                        MtuConfig.MAX_MTU
                    )
                    null -> null
                }

                binding.tilKeepalive.error = when (state.keepAliveValidationError) {
                    KeepAliveValidationError.INVALID_NUMBER -> getString(R.string.keepalive_invalid_number)
                    KeepAliveValidationError.OUT_OF_RANGE -> getString(
                        R.string.keepalive_invalid_range,
                        KeepAliveConfig.MIN_SECONDS,
                        KeepAliveConfig.MAX_SECONDS
                    )
                    null -> null
                }

                if (state.saved) {
                    Snackbar.make(binding.root, R.string.mtu_saved, Snackbar.LENGTH_SHORT).show()
                    settingsViewModel.clearSavedFlag()
                }

                if (state.keepAliveSaved) {
                    Snackbar.make(binding.root, R.string.keepalive_saved, Snackbar.LENGTH_SHORT).show()
                    settingsViewModel.clearSavedFlag()
                }
            }
        }
    }

    override fun onDestroyView() {
        super.onDestroyView()
        _binding = null
    }
}
