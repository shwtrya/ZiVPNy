#!/bin/bash

# Colors
GREEN="\033[1;32m"
YELLOW="\033[1;33m"
CYAN="\033[1;36m"
RED="\033[1;31m"
BLUE="\033[1;34m"
RESET="\033[0m"
BOLD="\033[1m"
GRAY="\033[1;30m"

print_task() {
  echo -ne "${GRAY}•${RESET} $1..."
}

print_done() {
  echo -e "\r${GREEN}✓${RESET} $1      "
}

print_fail() {
  echo -e "\r${RED}✗${RESET} $1      "
  exit 1
}

run_silent() {
  local msg="$1"
  local cmd="$2"
  
  print_task "$msg"
  bash -c "$cmd" &>/tmp/zivpn_install.log
  if [ $? -eq 0 ]; then
    print_done "$msg"
  else
    print_fail "$msg (Check /tmp/zivpn_install.log)"
  fi
}

clear
echo -e "${BOLD}ZiVPN UDP Installer${RESET}"
echo -e "${GRAY}AutoFTbot Edition${RESET}"
echo ""

if [[ "$(uname -s)" != "Linux" ]] || [[ "$(uname -m)" != "x86_64" ]]; then
  print_fail "System not supported (Linux AMD64 only)"
fi

if [ -f /usr/local/bin/zivpn ]; then
  echo -e "${YELLOW}! ZiVPN detected. Reinstalling...${RESET}"
  systemctl stop zivpn.service &>/dev/null
  systemctl stop zivpn-api.service &>/dev/null
  systemctl stop zivpn-bot.service &>/dev/null
fi

run_silent "Updating system" "sudo apt-get update"
run_silent "Setting Timezone" "sudo timedatectl set-timezone Asia/Jakarta"

if ! command -v go &> /dev/null; then
  run_silent "Installing dependencies" "sudo apt-get install -y golang git net-tools"
else
  print_done "Dependencies ready"
fi

run_silent "Installing Fail2ban" "sudo apt-get install -y fail2ban"

cat <<'EOF' > /etc/fail2ban/filter.d/zivpn.conf
[Definition]
failregex = ^.*(?:auth|authentication)\s*(?:failed|failure|invalid|error).*?(?:from|ip|addr|remote)\s*[:=]?\s*<HOST>.*$
            ^.*(?:bad|invalid)\s*(?:password|credentials).*?(?:from|ip|addr|remote)\s*[:=]?\s*<HOST>.*$
            ^.*(?:login|account)\s*(?:failed|invalid).*?(?:from|ip|addr|remote)\s*[:=]?\s*<HOST>.*$
ignoreregex =
EOF

cat <<'EOF' > /etc/logrotate.d/zivpn
/var/log/zivpn.log {
  daily
  size 50M
  rotate 7
  compress
  delaycompress
  missingok
  notifempty
  copytruncate
  sharedscripts
  postrotate
    systemctl reload fail2ban >/dev/null 2>&1 || true
  endscript
}
EOF

cat <<'EOF' > /etc/fail2ban/jail.d/zivpn.local
[sshd]
enabled = true
backend = systemd
maxretry = 5
findtime = 10m
bantime = 1h

[zivpn-udp]
enabled = true
filter = zivpn
backend = auto
journalmatch = _SYSTEMD_UNIT=zivpn.service
logpath = /var/log/zivpn.log
port = 5667
protocol = udp
maxretry = 5
findtime = 10m
bantime = 1h
EOF

run_silent "Enabling Fail2ban" "sudo systemctl enable --now fail2ban"
run_silent "Restarting Fail2ban" "sudo systemctl restart fail2ban"

echo ""
echo -ne "${BOLD}Domain Configuration${RESET}\n"
while true; do
  read -p "Enter Domain: " domain
  if [[ -n "$domain" ]]; then
    break
  fi
done
echo ""

echo -ne "${BOLD}API Key Configuration${RESET}\n"
generated_key=$(openssl rand -hex 16)
echo -e "Generated Key: ${CYAN}$generated_key${RESET}"
read -p "Enter API Key (Press Enter to use generated): " input_key
if [[ -z "$input_key" ]]; then
  api_key="$generated_key"
else
  api_key="$input_key"
fi
echo -e "Using Key: ${GREEN}$api_key${RESET}"
echo ""

echo -ne "${BOLD}Torrent Blocker Configuration${RESET}\n"
read -p "Enable torrent blocker (iptables/ufw) [y/N]: " enable_torrent_blocker
enable_torrent_blocker=${enable_torrent_blocker:-N}
echo ""

echo -ne "${BOLD}Automation Integration${RESET}\n"
read -p "Enable Auto ModPES (Android >= 11) [y/N]: " enable_auto_modpes
enable_auto_modpes=${enable_auto_modpes:-N}
read -p "Auto ModPES interval seconds [default: 300]: " auto_modpes_interval
auto_modpes_interval=${auto_modpes_interval:-300}
read -p "Enable Auto Airplane Mode [y/N]: " enable_auto_airplane
enable_auto_airplane=${enable_auto_airplane:-N}
read -p "Auto Airplane interval seconds [default: 300]: " auto_airplane_interval
auto_airplane_interval=${auto_airplane_interval:-300}
echo ""

systemctl stop zivpn.service &>/dev/null
run_silent "Downloading Core" "wget -q https://github.com/zahidbd2/udp-zivpn/releases/download/udp-zivpn_1.4.9/udp-zivpn-linux-amd64 -O /usr/local/bin/zivpn && chmod +x /usr/local/bin/zivpn"

mkdir -p /etc/zivpn
echo "$domain" > /etc/zivpn/domain
echo "$api_key" > /etc/zivpn/apikey
run_silent "Configuring" "wget -q https://raw.githubusercontent.com/shwtrya/ZiVPNy/main/config.json -O /etc/zivpn/config.json"
run_silent "Configuring packages" "wget -q https://raw.githubusercontent.com/shwtrya/ZiVPNy/main/packages.json -O /etc/zivpn/packages.json"

run_silent "Generating SSL" "openssl req -new -newkey rsa:4096 -days 365 -nodes -x509 -subj '/C=ID/ST=Jawa Barat/L=Bandung/O=AutoFTbot/OU=IT Department/CN=$domain' -keyout /etc/zivpn/zivpn.key -out /etc/zivpn/zivpn.crt"

automation_dir="/usr/local/bin/zivpn"
if [[ -f /usr/local/bin/zivpn ]]; then
  automation_dir="/usr/local/bin/zivpn-automation.d"
fi
if [[ -d /usr/local/bin/zivpn-automation ]]; then
  legacy_dir="/usr/local/bin/zivpn-automation.d"
  if [[ -e "$legacy_dir" ]]; then
    legacy_dir="/usr/local/bin/zivpn-automation.d-legacy-$(date +%s)"
  fi
  mv /usr/local/bin/zivpn-automation "$legacy_dir"
fi
mkdir -p "$automation_dir"
run_silent "Downloading Auto ModPES Script" "wget -q https://raw.githubusercontent.com/shwtrya/ZiVPNy/main/scripts/auto-modpes-android11.sh -O ${automation_dir}/auto-modpes-android11.sh"
run_silent "Downloading Auto Airplane Script" "wget -q https://raw.githubusercontent.com/shwtrya/ZiVPNy/main/scripts/auto-airplane-mode.sh -O ${automation_dir}/auto-airplane-mode.sh"
run_silent "Installing Automation Helper" "wget -q https://raw.githubusercontent.com/shwtrya/ZiVPNy/main/scripts/zivpn-automation -O /usr/local/bin/zivpn-automation"
chmod +x "${automation_dir}/auto-modpes-android11.sh" "${automation_dir}/auto-airplane-mode.sh" /usr/local/bin/zivpn-automation

mkdir -p /var/log/zivpn
cat <<EOF > /etc/zivpn/automation.conf
AUTO_MODPES_ENABLED=$(if [[ "$enable_auto_modpes" =~ ^[Yy]$ ]]; then echo "true"; else echo "false"; fi)
AUTO_AIRPLANE_ENABLED=$(if [[ "$enable_auto_airplane" =~ ^[Yy]$ ]]; then echo "true"; else echo "false"; fi)
AUTO_MODPES_INTERVAL=${auto_modpes_interval}
AUTO_AIRPLANE_INTERVAL=${auto_airplane_interval}
AUTOMATION_BIN_DIR=${automation_dir}
LOG_DIR=/var/log/zivpn
LOG_FILE=/var/log/zivpn/automation.log
EOF

cat <<'EOF' > /etc/logrotate.d/zivpn-automation
/var/log/zivpn/automation.log {
  daily
  size 20M
  rotate 7
  compress
  delaycompress
  missingok
  notifempty
  copytruncate
}
EOF

run_silent "Configuring Automation Scheduler" "/usr/local/bin/zivpn-automation restart"

mkdir -p /etc/zivpn/protocols
cat <<'EOF' > /etc/zivpn/torrent-block.rules
#!/bin/bash
# ZiVPN torrent blocker rules (edit as needed)
PORTS_TCP=(6881 6882 6883 6884 6885 6886 6887 6888 6889 6969 51413)
PORTS_UDP=(6881 6882 6883 6884 6885 6886 6887 6888 6889 6969 51413)
STRING_PATTERNS=("BitTorrent" "BitTorrent protocol" "peer_id=" "announce" "info_hash" "get_peers" "announce_peer" "find_node")
L7_PROTOCOLS=("bittorrent")
EOF

cat <<'EOF' > /etc/zivpn/torrent-block-apply.sh
#!/bin/bash
set -euo pipefail

RULES_FILE="/etc/zivpn/torrent-block.rules"
if [ ! -f "$RULES_FILE" ]; then
  echo "Rules file not found: $RULES_FILE" >&2
  exit 1
fi

source "$RULES_FILE"

join_by() {
  local IFS=","
  echo "$*"
}

add_rule() {
  if ! iptables -C "$@" &>/dev/null; then
    iptables -A "$@"
  fi
}

add_string_rules() {
  local proto="$1"
  for pattern in "${STRING_PATTERNS[@]:-}"; do
    add_rule INPUT -p "$proto" -m string --string "$pattern" --algo bm -j DROP
    add_rule FORWARD -p "$proto" -m string --string "$pattern" --algo bm -j DROP
  done
}

if [ "${#PORTS_TCP[@]}" -gt 0 ]; then
  tcp_ports="$(join_by "${PORTS_TCP[@]}")"
  add_rule INPUT -p tcp -m multiport --dports "$tcp_ports" -j DROP
  add_rule FORWARD -p tcp -m multiport --dports "$tcp_ports" -j DROP
fi

if [ "${#PORTS_UDP[@]}" -gt 0 ]; then
  udp_ports="$(join_by "${PORTS_UDP[@]}")"
  add_rule INPUT -p udp -m multiport --dports "$udp_ports" -j DROP
  add_rule FORWARD -p udp -m multiport --dports "$udp_ports" -j DROP
fi

add_string_rules tcp
add_string_rules udp

if iptables -m layer7 -h &>/dev/null; then
  for proto in "${L7_PROTOCOLS[@]:-}"; do
    add_rule INPUT -p tcp -m layer7 --l7proto "$proto" -j DROP
    add_rule FORWARD -p tcp -m layer7 --l7proto "$proto" -j DROP
  done
fi

if command -v ufw &>/dev/null && ufw status | grep -q "Status: active"; then
  for port in "${PORTS_TCP[@]}"; do
    ufw deny "$port"/tcp &>/dev/null || true
  done
  for port in "${PORTS_UDP[@]}"; do
    ufw deny "$port"/udp &>/dev/null || true
  done
fi
EOF

cat <<'EOF' > /etc/zivpn/torrent-block-remove.sh
#!/bin/bash
set -euo pipefail

RULES_FILE="/etc/zivpn/torrent-block.rules"
if [ ! -f "$RULES_FILE" ]; then
  exit 0
fi

source "$RULES_FILE"

join_by() {
  local IFS=","
  echo "$*"
}

delete_rule() {
  while iptables -C "$@" &>/dev/null; do
    iptables -D "$@"
  done
}

delete_string_rules() {
  local proto="$1"
  for pattern in "${STRING_PATTERNS[@]:-}"; do
    delete_rule INPUT -p "$proto" -m string --string "$pattern" --algo bm -j DROP
    delete_rule FORWARD -p "$proto" -m string --string "$pattern" --algo bm -j DROP
  done
}

if [ "${#PORTS_TCP[@]}" -gt 0 ]; then
  tcp_ports="$(join_by "${PORTS_TCP[@]}")"
  delete_rule INPUT -p tcp -m multiport --dports "$tcp_ports" -j DROP
  delete_rule FORWARD -p tcp -m multiport --dports "$tcp_ports" -j DROP
fi

if [ "${#PORTS_UDP[@]}" -gt 0 ]; then
  udp_ports="$(join_by "${PORTS_UDP[@]}")"
  delete_rule INPUT -p udp -m multiport --dports "$udp_ports" -j DROP
  delete_rule FORWARD -p udp -m multiport --dports "$udp_ports" -j DROP
fi

delete_string_rules tcp
delete_string_rules udp

if iptables -m layer7 -h &>/dev/null; then
  for proto in "${L7_PROTOCOLS[@]:-}"; do
    delete_rule INPUT -p tcp -m layer7 --l7proto "$proto" -j DROP
    delete_rule FORWARD -p tcp -m layer7 --l7proto "$proto" -j DROP
  done
fi

if command -v ufw &>/dev/null && ufw status | grep -q "Status: active"; then
  for port in "${PORTS_TCP[@]}"; do
    ufw delete deny "$port"/tcp &>/dev/null || true
  done
  for port in "${PORTS_UDP[@]}"; do
    ufw delete deny "$port"/udp &>/dev/null || true
  done
fi
EOF

chmod +x /etc/zivpn/torrent-block-apply.sh /etc/zivpn/torrent-block-remove.sh
cat <<'EOF' > /etc/zivpn/provision-protocol.sh
#!/bin/bash
set -euo pipefail

protocol="${1:-}"
base="/etc/zivpn/protocols"
timestamp="$(date -Is)"

case "$protocol" in
  udp|ssh|dropbear|ws|ssl)
    ;;
  *)
    echo "Unsupported protocol: $protocol" >&2
    exit 1
    ;;
esac

mkdir -p "$base"
config_file="$base/${protocol}.json"

if [ ! -f "$config_file" ]; then
  cat <<JSON > "$config_file"
{
  "protocol": "$protocol",
  "updated_at": "$timestamp",
  "users": []
}
JSON
fi
EOF
chmod +x /etc/zivpn/provision-protocol.sh

for proto in udp ssh dropbear ws ssl; do
  /etc/zivpn/provision-protocol.sh "$proto"
done

# Find a free API port
print_task "Finding available API Port"
API_PORT=8080
while netstat -tuln | grep -q ":$API_PORT "; do
    ((API_PORT++))
done
echo "$API_PORT" > /etc/zivpn/api_port
print_done "API Port selected: ${CYAN}$API_PORT${RESET}"

cat <<'END' > /etc/sysctl.d/99-zivpn.conf
net.core.default_qdisc=fq
net.ipv4.tcp_congestion_control=bbr
net.ipv4.ip_forward=1
net.netfilter.nf_conntrack_max=262144
net.netfilter.nf_conntrack_udp_timeout=60
net.netfilter.nf_conntrack_udp_timeout_stream=180
net.core.rmem_max=16777216
net.core.wmem_max=16777216
net.core.rmem_default=16777216
net.core.wmem_default=16777216
net.core.optmem_max=65536
net.core.somaxconn=65535
net.core.netdev_max_backlog=16384
net.core.netdev_budget=600
net.core.rps_sock_flow_entries=65536
net.ipv4.tcp_rmem=4096 87380 16777216
net.ipv4.tcp_wmem=4096 65536 16777216
net.ipv4.tcp_fastopen=3
fs.file-max=1000000
net.ipv4.udp_mem=262144 524288 1048576
net.ipv4.udp_rmem_min=16384
net.ipv4.udp_wmem_min=16384
END
sysctl --system &>/dev/null

cat <<EOF > /etc/systemd/system/zivpn.service
[Unit]
Description=ZIVPN UDP VPN Server
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=/etc/zivpn
ExecStart=/usr/local/bin/zivpn server -c /etc/zivpn/config.json
Restart=always
RestartSec=3
LimitNOFILE=65535
Environment=ZIVPN_LOG_LEVEL=info
CapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_BIND_SERVICE CAP_NET_RAW
AmbientCapabilities=CAP_NET_ADMIN CAP_NET_BIND_SERVICE CAP_NET_RAW
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
EOF

mkdir -p /etc/zivpn/api
run_silent "Setting up API" "wget -q https://raw.githubusercontent.com/shwtrya/ZiVPNy/main/zivpn-api.go -O /etc/zivpn/api/zivpn-api.go"
api_go_version=$(go env GOVERSION 2>/dev/null | sed -E 's/^go([0-9]+\.[0-9]+).*/\1/')
if [[ -z "$api_go_version" ]]; then
  api_go_version="1.18"
fi
cat <<EOF > /etc/zivpn/api/go.mod
module zivpn-api

go ${api_go_version}
EOF

cd /etc/zivpn/api
run_silent "Compiling API" "go build -o zivpn-api zivpn-api.go"

cat <<EOF > /etc/systemd/system/zivpn-api.service
[Unit]
Description=ZiVPN Golang API Service
After=network.target zivpn.service

[Service]
Type=simple
User=root
WorkingDirectory=/etc/zivpn/api
ExecStart=/etc/zivpn/api/zivpn-api
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF

echo ""
echo -ne "${BOLD}Telegram Bot Configuration${RESET}\n"
echo -ne "${GRAY}(Leave empty to skip)${RESET}\n"
read -p "Bot Token: " bot_token
read -p "Admin ID : " admin_id
read -p "Admin IDs tambahan (pisahkan koma, optional): " extra_admin_ids

if [[ -n "$bot_token" ]] && [[ -n "$admin_id" ]]; then
  admin_ids_json="[$admin_id"
  if [[ -n "$extra_admin_ids" ]]; then
    extra_admin_ids=$(echo "$extra_admin_ids" | tr ',' ' ')
    for id in $extra_admin_ids; do
      id=$(echo "$id" | xargs)
      if [[ -n "$id" ]]; then
        admin_ids_json="$admin_ids_json, $id"
      fi
    done
  fi
  admin_ids_json="$admin_ids_json]"

  echo ""
  echo "Select Bot Type:"
  echo "1) Free (Admin Only / Public Mode)"
  echo "2) Paid (Pakasir Payment Gateway)"
  read -p "Choice [1]: " bot_type
  bot_type=${bot_type:-1}

  if [[ "$bot_type" == "2" ]]; then
    read -p "Pakasir Project Slug: " pakasir_slug
    read -p "Pakasir API Key     : " pakasir_key
    read -p "Daily Price (IDR)   : " daily_price
    
    echo "{\"bot_token\": \"$bot_token\", \"admin_id\": $admin_id, \"admin_ids\": $admin_ids_json, \"mode\": \"public\", \"domain\": \"$domain\", \"pakasir_slug\": \"$pakasir_slug\", \"pakasir_api_key\": \"$pakasir_key\", \"daily_price\": $daily_price}" > /etc/zivpn/bot-config.json
    bot_file="zivpn-paid-bot.go"
  else
    read -p "Bot Mode (public/private) [default: private]: " bot_mode
    bot_mode=${bot_mode:-private}
    
    echo "{\"bot_token\": \"$bot_token\", \"admin_id\": $admin_id, \"admin_ids\": $admin_ids_json, \"mode\": \"$bot_mode\", \"domain\": \"$domain\"}" > /etc/zivpn/bot-config.json
    bot_file="zivpn-bot.go"
  fi
  
  run_silent "Downloading Bot" "wget -q https://raw.githubusercontent.com/shwtrya/ZiVPNy/main/$bot_file -O /etc/zivpn/api/$bot_file"
  
  cd /etc/zivpn/api
  run_silent "Downloading Bot Deps" "go get github.com/go-telegram-bot-api/telegram-bot-api/v5"
  
  run_silent "Compiling Bot" "go build -o zivpn-bot \"$bot_file\""

  cat <<EOF > /etc/systemd/system/zivpn-bot.service
[Unit]
Description=ZiVPN Telegram Bot
After=network.target zivpn-api.service

[Service]
Type=simple
User=root
WorkingDirectory=/etc/zivpn/api
ExecStart=/etc/zivpn/api/zivpn-bot
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF
  systemctl enable zivpn-bot.service &>/dev/null
  systemctl start zivpn-bot.service &>/dev/null
  else
  print_task "Skipping Bot Setup"
  echo ""
fi

run_silent "Starting Services" "systemctl enable zivpn.service && systemctl start zivpn.service && systemctl enable zivpn-api.service && systemctl start zivpn-api.service"

# Setup Cron for Auto-Expire
echo -e "${YELLOW}Setting up Cron Job for Auto-Expire...${NC}"
cron_cmd="0 0 * * * /usr/bin/curl -s -X POST -H \"X-API-Key: \$(cat /etc/zivpn/apikey)\" http://127.0.0.1:\$(cat /etc/zivpn/api_port)/api/cron/expire >> /var/log/zivpn-cron.log 2>&1"
(crontab -l 2>/dev/null | grep -v "/api/cron/expire"; echo "$cron_cmd") | crontab -
echo -e "${YELLOW}Setting up Cron Job for Cleanup...${NC}"
cleanup_cmd="10 0 * * * /usr/bin/curl -s -X POST -H \"X-API-Key: \$(cat /etc/zivpn/apikey)\" http://127.0.0.1:\$(cat /etc/zivpn/api_port)/api/cron/cleanup >> /var/log/zivpn-cron.log 2>&1"
(crontab -l 2>/dev/null | grep -v "/api/cron/cleanup"; echo "$cleanup_cmd") | crontab -
print_done "Cron Job Configured"

iface=$(ip -4 route ls | grep default | grep -Po '(?<=dev )(\S+)' | head -1)
iptables -t nat -A PREROUTING -i "$iface" -p udp --dport 6000:19999 -j DNAT --to-destination :5667 &>/dev/null
ufw allow 6000:19999/udp &>/dev/null
ufw allow 5667/udp &>/dev/null
ufw allow $API_PORT/tcp &>/dev/null

if [[ "$enable_torrent_blocker" =~ ^[Yy]$ ]]; then
  run_silent "Applying Torrent Blocker" "/etc/zivpn/torrent-block-apply.sh"
else
  print_task "Skipping Torrent Blocker"
  echo ""
fi

rm -f "$0" install.tmp install.log &>/dev/null

echo ""
echo -e "${BOLD}Installation Complete${RESET}"
echo -e "Domain  : ${CYAN}$domain${RESET}"
echo -e "API     : ${CYAN}$API_PORT${RESET}"
echo -e "Token   : ${CYAN}$api_key${RESET}"
echo -e "Dev     : ${CYAN}https://t.me/AutoFTBot${RESET}"
echo ""
