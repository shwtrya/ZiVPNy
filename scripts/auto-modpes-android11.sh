#!/usr/bin/env bash
set -euo pipefail

SCRIPT_NAME="$(basename "$0")"

usage() {
  cat <<USAGE
Usage:
  ${SCRIPT_NAME} on
  ${SCRIPT_NAME} off
  ${SCRIPT_NAME} toggle
  ${SCRIPT_NAME} interval <seconds>

Contoh:
  ${SCRIPT_NAME} on
  ${SCRIPT_NAME} off
  ${SCRIPT_NAME} toggle
  ${SCRIPT_NAME} interval 5
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

get_airplane_state() {
  adb shell settings get global airplane_mode_on 2>/dev/null || true
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

get_toggle_target() {
  local current
  current="$(get_airplane_state)"
  case "$current" in
    1) echo "disable" ;;
    0) echo "enable" ;;
    *) echo "enable" ;;
  esac
}

run_interval() {
  local seconds="$1"
  local next_action

  if ! [[ "$seconds" =~ ^[0-9]+$ ]] || [[ "$seconds" -le 0 ]]; then
    echo "Interval harus berupa angka > 0." >&2
    return 1
  fi

  next_action="$(get_toggle_target)"
  while true; do
    set_airplane "$next_action"
    if [[ "$next_action" == "enable" ]]; then
      next_action="disable"
    else
      next_action="enable"
    fi
    sleep "$seconds"
  done
}

main() {
  if [[ $# -lt 1 ]]; then
    usage
    exit 1
  fi

  case "$1" in
    on)
      set_airplane enable
      ;;
    off)
      set_airplane disable
      ;;
    toggle)
      set_airplane "$(get_toggle_target)"
      ;;
    interval)
      if [[ $# -ne 2 ]]; then
        usage
        exit 1
      fi
      run_interval "$2"
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
