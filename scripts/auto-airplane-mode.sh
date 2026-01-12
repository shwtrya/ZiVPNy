#!/usr/bin/env bash
set -euo pipefail

SCRIPT_NAME="$(basename "$0")"

# Catatan kompatibilitas:
# - Android 8–13+ dengan adb aktif.
# - Perintah settings/cmd connectivity dapat membutuhkan WRITE_SECURE_SETTINGS atau akses root.

usage() {
  cat <<USAGE
Usage:
  ${SCRIPT_NAME} once [interval_seconds]
  ${SCRIPT_NAME} loop [interval_seconds]

Env:
  AIRPLANE_INTERVAL  Interval detik untuk jeda antar langkah (default: 5)

Contoh:
  ${SCRIPT_NAME} once 3
  ${SCRIPT_NAME} loop 10
  AIRPLANE_INTERVAL=7 ${SCRIPT_NAME} loop
USAGE
}

has_cmd_connectivity() {
  adb shell cmd connectivity help 2>/dev/null | grep -q "airplane-mode"
}

has_settings() {
  adb shell settings help >/dev/null 2>&1
}

set_airplane_cmd() {
  local state="$1"
  adb shell cmd connectivity airplane-mode "$state"
}

set_airplane_settings() {
  local state="$1"
  local value="$2"
  adb shell settings put global airplane_mode_on "$value"
  adb shell am broadcast -a android.intent.action.AIRPLANE_MODE --ez state "$state" >/dev/null
}

set_airplane() {
  local action="$1"

  if has_cmd_connectivity; then
    set_airplane_cmd "$action"
    return 0
  fi

  if has_settings; then
    if [[ "$action" == "enable" ]]; then
      set_airplane_settings true 1
    else
      set_airplane_settings false 0
    fi
    return 0
  fi

  echo "Tidak ada metode yang tersedia: cmd connectivity atau settings." >&2
  return 1
}

validate_interval() {
  local seconds="$1"
  if ! [[ "$seconds" =~ ^[0-9]+$ ]] || [[ "$seconds" -le 0 ]]; then
    echo "Interval harus berupa angka > 0." >&2
    return 1
  fi
}

run_cycle() {
  local seconds="$1"

  validate_interval "$seconds"

  set_airplane disable
  sleep "$seconds"
  set_airplane enable
  sleep "$seconds"
  set_airplane disable
}

run_loop() {
  local seconds="$1"

  validate_interval "$seconds"

  while true; do
    run_cycle "$seconds"
    sleep "$seconds"
  done
}

main() {
  if [[ $# -lt 1 ]]; then
    usage
    exit 1
  fi

  local interval="${2:-${AIRPLANE_INTERVAL:-5}}"

  case "$1" in
    once)
      run_cycle "$interval"
      ;;
    loop)
      run_loop "$interval"
      ;;
    -h|--help)
      usage
      ;;
    *)
      usage
      exit 1
      ;;
  esac
}

main "$@"
