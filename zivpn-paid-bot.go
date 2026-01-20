//go:build api
// +build api

package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// ==========================================
// Constants & Configuration
// ==========================================

const (
	BotConfigFile      = "/etc/zivpn/bot-config.json"
	ApiPortFile        = "/etc/zivpn/api_port"
	ApiKeyFile         = "/etc/zivpn/apikey"
	DomainFile         = "/etc/zivpn/domain"
	PortFile           = "/etc/zivpn/port"
	PackagesFile       = "/etc/zivpn/packages.json"
	TorrentRulesFile   = "/etc/zivpn/torrent-block.rules"
	TorrentApplyScript = "/etc/zivpn/torrent-block-apply.sh"
	BotNotifyAddr      = "127.0.0.1:9871"
	MaxAccounts        = 20
)

var ApiUrl = "http://127.0.0.1:" + PortFile + "/api"

var ApiKey = "AutoFtBot-agskjgdvsbdreiWG1234512SDKrqw"

type BotConfig struct {
	BotToken      string            `json:"bot_token"`
	AdminID       int64             `json:"admin_id"`
	AdminIDs      []int64           `json:"admin_ids,omitempty"`
	AdminRoles    map[string]string `json:"admin_roles,omitempty"`
	Mode          string            `json:"mode"`
	Domain        string            `json:"domain"`
	PakasirSlug   string            `json:"pakasir_slug"`
	PakasirApiKey string            `json:"pakasir_api_key"`
	DailyPrice    int               `json:"daily_price"`
}

type IpInfo struct {
	City string `json:"city"`
	Isp  string `json:"isp"`
}

type UserData struct {
	Username  string   `json:"username"`
	Password  string   `json:"password"`
	Expired   string   `json:"expired"`
	Status    string   `json:"status"`
	Protocols []string `json:"protocols"`
	IpLimit   int      `json:"ip_limit"`
}

type Package struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Days      int      `json:"days"`
	IpLimit   int      `json:"ip_limit"`
	Protocols []string `json:"protocols"`
}

type OnlineAccount struct {
	Username string `json:"username"`
	Password string `json:"password"`
	IP       string `json:"ip"`
	LastSeen string `json:"last_seen"`
}

// ==========================================
// Global State
// ==========================================

var userStates = make(map[int64]string)
var tempUserData = make(map[int64]map[string]string)
var lastMessageIDs = make(map[int64]int)
var lastAccountInfos = make(map[int64]string)
var loadingMessages = make(map[int64]int)
var mutex = &sync.Mutex{}

// ==========================================
// Loading Animation
// ==========================================

var loadingFrames = []string{
	"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏",
}

var progressBarFrames = []string{
	"▱▱▱▱▱▱▱▱▱▱",
	"▰▱▱▱▱▱▱▱▱▱",
	"▰▰▱▱▱▱▱▱▱▱",
	"▰▰▰▱▱▱▱▱▱▱",
	"▰▰▰▰▱▱▱▱▱▱",
	"▰▰▰▰▰▱▱▱▱▱",
	"▰▰▰▰▰▰▱▱▱▱",
	"▰▰▰▰▰▰▰▱▱▱",
	"▰▰▰▰▰▰▰▰▱▱",
	"▰▰▰▰▰▰▰▰▰▱",
	"▰▰▰▰▰▰▰▰▰▰",
}

func showLoading(bot *tgbotapi.BotAPI, chatID int64, message string) int {
	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("%s %s", loadingFrames[0], message))
	sentMsg, err := bot.Send(msg)
	if err != nil {
		return 0
	}

	mutex.Lock()
	loadingMessages[chatID] = sentMsg.MessageID
	mutex.Unlock()

	go animateLoading(bot, chatID, sentMsg.MessageID, message)

	return sentMsg.MessageID
}

func animateLoading(bot *tgbotapi.BotAPI, chatID int64, messageID int, message string) {
	for i := 0; i < 20; i++ {
		mutex.Lock()
		currentMsgID, exists := loadingMessages[chatID]
		mutex.Unlock()

		if !exists || currentMsgID != messageID {
			break
		}

		frame := loadingFrames[i%len(loadingFrames)]
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, fmt.Sprintf("%s %s", frame, message))
		bot.Send(editMsg)

		time.Sleep(200 * time.Millisecond)
	}
}

func hideLoading(bot *tgbotapi.BotAPI, chatID int64) {
	mutex.Lock()
	if msgID, exists := loadingMessages[chatID]; exists {
		deleteMsg := tgbotapi.NewDeleteMessage(chatID, msgID)
		bot.Request(deleteMsg)
		delete(loadingMessages, chatID)
	}
	mutex.Unlock()
}

func showProgressBar(percentage int) string {
	if percentage < 0 {
		percentage = 0
	}
	if percentage > 100 {
		percentage = 100
	}

	index := (percentage * (len(progressBarFrames) - 1)) / 100
	return progressBarFrames[index]
}

// ==========================================
// Status Emoji Helper
// ==========================================

func getStatusEmoji(status string) string {
	switch strings.ToLower(status) {
	case "active", "aktif":
		return "🟢"
	case "expired", "kadaluarsa":
		return "🔴"
	case "pending":
		return "🟡"
	case "suspended":
		return "🟠"
	default:
		return "⚪"
	}
}

func getProtocolEmoji(protocol string) string {
	emojiMap := map[string]string{
		"udp":      "🚀",
		"ssh":      "🔐",
		"dropbear": "🐻",
		"ws":       "🌐",
		"ssl":      "🔒",
		"vmess":    "✈️",
		"vless":    "⚡",
		"trojan":   "🗡️",
	}

	if emoji, ok := emojiMap[strings.ToLower(protocol)]; ok {
		return emoji
	}
	return "✓"
}

// ==========================================
// Date Formatting
// ==========================================

func formatDate(dateStr string) string {
	formats := []string{
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05Z",
		"2006-01-02",
	}

	var t time.Time
	var err error

	for _, format := range formats {
		t, err = time.Parse(format, dateStr)
		if err == nil {
			break
		}
	}

	if err != nil {
		return dateStr
	}

	now := time.Now()
	diff := t.Sub(now)

	// Format: "31 Des 2024, 23:59" + relative time
	monthNames := []string{
		"", "Jan", "Feb", "Mar", "Apr", "Mei", "Jun",
		"Jul", "Agt", "Sep", "Oct", "Nov", "Des",
	}

	formatted := fmt.Sprintf("%d %s %d, %02d:%02d",
		t.Day(),
		monthNames[t.Month()],
		t.Year(),
		t.Hour(),
		t.Minute(),
	)

	// Add relative time
	if diff.Hours() < 24 {
		formatted += " (Hari ini)"
	} else if diff.Hours() < 48 {
		formatted += " (Besok)"
	} else if diff.Hours() > 0 {
		days := int(diff.Hours() / 24)
		formatted += fmt.Sprintf(" (%d hari lagi)", days)
	} else if diff.Hours() > -24 {
		formatted += " (Kemarin)"
	} else {
		days := int(-diff.Hours() / 24)
		formatted += fmt.Sprintf(" (%d hari lalu)", days)
	}

	return formatted
}

func formatDuration(seconds int64) string {
	if seconds < 60 {
		return fmt.Sprintf("%d detik", seconds)
	}

	minutes := seconds / 60
	if minutes < 60 {
		return fmt.Sprintf("%d menit", minutes)
	}

	hours := minutes / 60
	if hours < 24 {
		return fmt.Sprintf("%d jam", hours)
	}

	days := hours / 24
	return fmt.Sprintf("%d hari", days)
}

// ==========================================
// Currency Formatting
// ==========================================

func formatRupiah(amount int) string {
	str := strconv.Itoa(amount)
	var result strings.Builder
	n := len(str)

	for i, digit := range str {
		if i > 0 && (n-i)%3 == 0 {
			result.WriteString(".")
		}
		result.WriteRune(digit)
	}

	return result.String()
}

// ==========================================
// Main Entry Point
// ==========================================

func main() {
	if keyBytes, err := ioutil.ReadFile(ApiKeyFile); err == nil {
		ApiKey = strings.TrimSpace(string(keyBytes))
	}

	// Load API Port
	if portBytes, err := ioutil.ReadFile(ApiPortFile); err == nil {
		port := strings.TrimSpace(string(portBytes))
		ApiUrl = fmt.Sprintf("http://127.0.0.1:%s/api", port)
	}

	config, err := loadConfig()
	if err != nil {
		log.Fatal("Gagal memuat konfigurasi bot:", err)
	}

	bot, err := tgbotapi.NewBotAPI(config.BotToken)
	if err != nil {
		log.Panic(err)
	}

	bot.Debug = false
	log.Printf("Authorized on account %s", bot.Self.UserName)

	go startNotificationServer(bot, &config)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	// Start Payment Checker
	go startPaymentChecker(bot, &config)

	for update := range updates {
		if update.Message != nil {
			handleMessage(bot, update.Message, &config)
		} else if update.CallbackQuery != nil {
			handleCallback(bot, update.CallbackQuery, &config)
		}
	}
}

// ==========================================
// Telegram Event Handlers
// ==========================================

func handleMessage(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, config *BotConfig) {
	if state, exists := userStates[msg.From.ID]; exists {
		handleState(bot, msg, state, config)
		return
	}

	// Handle Document Upload (Restore) - Admin Only
	if msg.Document != nil && isAdmin(config, msg.From.ID) {
		if state, exists := userStates[msg.From.ID]; exists && state == "waiting_restore_file" {
			processRestoreFile(bot, msg, config)
			return
		}
	}

	if msg.IsCommand() {
		switch msg.Command() {
		case "start":
			showMainMenu(bot, msg.Chat.ID, config)
		default:
			replyError(bot, msg.Chat.ID, "Perintah tidak dikenal.")
		}
	}
}

func handleCallback(bot *tgbotapi.BotAPI, query *tgbotapi.CallbackQuery, config *BotConfig) {
	chatID := query.Message.Chat.ID
	userID := query.From.ID
	callbackText := ""

	switch {
	case query.Data == "menu_create":
		startCreateUser(bot, chatID, userID, false)
	case query.Data == "menu_check_payment":
		checkPaymentStatus(bot, chatID, userID)
	case query.Data == "menu_support":
		showSupport(bot, chatID)
	case query.Data == "menu_guide":
		showGuide(bot, chatID)
	case query.Data == "menu_admin_create":
		if isAdmin(config, userID) {
			startCreateUser(bot, chatID, userID, true)
		} else {
			callbackText = "Akses Ditolak"
		}
	case query.Data == "menu_admin_delete":
		if isAdmin(config, userID) {
			showUserSelection(bot, chatID, 1, "delete")
		} else {
			callbackText = "Akses Ditolak"
		}
	case query.Data == "menu_admin_renew":
		if isAdmin(config, userID) {
			showUserSelection(bot, chatID, 1, "renew")
		} else {
			callbackText = "Akses Ditolak"
		}
	case query.Data == "menu_admin_list":
		if isAdmin(config, userID) {
			listUsers(bot, chatID)
		} else {
			callbackText = "Akses Ditolak"
		}
	case query.Data == "menu_info":
		if isAdmin(config, userID) {
			systemInfo(bot, chatID, config)
		} else {
			callbackText = "Akses Ditolak"
		}
	case query.Data == "menu_admins":
		if isOwner(config, userID) {
			showAdminMenu(bot, chatID)
		} else {
			callbackText = "Akses Ditolak"
		}
	case query.Data == "menu_admin_add":
		if isOwner(config, userID) {
			startAddAdmin(bot, chatID, userID)
		} else {
			callbackText = "Akses Ditolak"
		}
	case query.Data == "menu_admin_remove":
		if isOwner(config, userID) {
			startRemoveAdmin(bot, chatID, userID)
		} else {
			callbackText = "Akses Ditolak"
		}
	case query.Data == "menu_admins_list":
		if isOwner(config, userID) {
			listAdmins(bot, chatID, config)
		} else {
			callbackText = "Akses Ditolak"
		}
	case query.Data == "cancel":
		cancelOperation(bot, chatID, userID, config)
	case strings.HasPrefix(query.Data, "section_"):
		callbackText = "Pilih menu di bawah header."

	case query.Data == "menu_admin":
		if isAdmin(config, userID) {
			showBackupRestoreMenu(bot, chatID, config, userID)
		} else {
			callbackText = "Anda tidak memiliki hak untuk mengakses Admin Panel."
			replyError(bot, chatID, "Akses Admin Panel ditolak: Anda bukan admin.")
		}
	case query.Data == "back_admin_panel":
		if isAdmin(config, userID) {
			showBackupRestoreMenu(bot, chatID, config, userID)
		} else {
			showMainMenu(bot, chatID, config)
		}
	case query.Data == "menu_online":
		if isAdmin(config, userID) {
			listOnlineUsers(bot, chatID)
		} else {
			callbackText = "Akses Ditolak"
		}
	case query.Data == "menu_backup_action":
		if isAdmin(config, userID) {
			performBackup(bot, chatID)
		} else {
			callbackText = "Akses Ditolak"
		}
	case query.Data == "menu_restore_action":
		if isAdmin(config, userID) {
			startRestore(bot, chatID, userID)
		} else {
			callbackText = "Akses Ditolak"
		}
	case query.Data == "menu_perf_high":
		if isAdmin(config, userID) {
			showLoading(bot, chatID, "Menerapkan High Performance...")
			status, err := applyHighPerformance()
			hideLoading(bot, chatID)
			if err != nil {
				replyError(bot, chatID, "Gagal menerapkan High Performance: "+err.Error())
			} else {
				sendMessage(bot, chatID, "✅ "+status)
			}
			showBackupRestoreMenu(bot, chatID, config, userID)
		} else {
			callbackText = "Akses Ditolak"
		}
	case query.Data == "menu_perf_conservative":
		if isAdmin(config, userID) {
			showLoading(bot, chatID, "Menerapkan Conservative...")
			status, err := applyConservativePerformance()
			hideLoading(bot, chatID)
			if err != nil {
				replyError(bot, chatID, "Gagal menerapkan Conservative: "+err.Error())
			} else {
				sendMessage(bot, chatID, "✅ "+status)
			}
			showBackupRestoreMenu(bot, chatID, config, userID)
		} else {
			callbackText = "Akses Ditolak"
		}
	case query.Data == "menu_perf_revert":
		if isAdmin(config, userID) {
			showLoading(bot, chatID, "Mengembalikan pengaturan...")
			status, err := revertPerformanceSettings()
			hideLoading(bot, chatID)
			if err != nil {
				replyError(bot, chatID, "Gagal revert performance: "+err.Error())
			} else {
				sendMessage(bot, chatID, "♻️ "+status)
			}
			showBackupRestoreMenu(bot, chatID, config, userID)
		} else {
			callbackText = "Akses Ditolak"
		}
	case strings.HasPrefix(query.Data, "page_"):
		if isAdmin(config, userID) {
			handlePagination(bot, chatID, query.Data)
		} else {
			callbackText = "Akses Ditolak"
		}
	case strings.HasPrefix(query.Data, "select_renew:"):
		if isAdmin(config, userID) {
			startRenewUser(bot, chatID, userID, query.Data)
		} else {
			callbackText = "Akses Ditolak"
		}
	case strings.HasPrefix(query.Data, "select_delete:"):
		if isAdmin(config, userID) {
			confirmDeleteUser(bot, chatID, query.Data)
		} else {
			callbackText = "Akses Ditolak"
		}
	case strings.HasPrefix(query.Data, "confirm_delete:"):
		if isAdmin(config, userID) {
			username := strings.TrimPrefix(query.Data, "confirm_delete:")
			deleteUser(bot, chatID, username, config)
		} else {
			callbackText = "Akses Ditolak"
		}
	case query.Data == "copy_account":
		mutex.Lock()
		accountInfo := lastAccountInfos[chatID]
		mutex.Unlock()
		if accountInfo == "" {
			callbackText = "Kode akun tidak ditemukan."
			break
		}
		accountMsg := tgbotapi.NewMessage(chatID, fmt.Sprintf("```\n%s\n```", accountInfo))
		accountMsg.ParseMode = "Markdown"
		bot.Send(accountMsg)
		callbackText = "✅ Detail akun telah disalin"
	}

	bot.Request(tgbotapi.NewCallback(query.ID, callbackText))
}

func handleState(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, state string, config *BotConfig) {
	userID := msg.From.ID
	text := strings.TrimSpace(msg.Text)
	chatID := msg.Chat.ID

	switch state {
	case "create_username":
		if !validateUsername(bot, chatID, text) {
			return
		}
		mutex.Lock()
		tempUserData[userID]["username"] = text
		mutex.Unlock()
		userStates[userID] = "create_password"
		sendMessage(bot, chatID, "🔐 Masukkan password untuk akun:")
	case "create_days":
		days, err := strconv.Atoi(text)
		if err != nil || days <= 0 {
			replyError(bot, chatID, "Durasi harus berupa angka positif (hari).")
			return
		}
		mutex.Lock()
		tempUserData[userID]["days"] = strconv.Itoa(days)
		mutex.Unlock()
		userStates[userID] = "create_ip_limit"
		sendMessage(bot, chatID, "🔢 Masukkan jumlah IP (1-2):")
	case "create_ip_limit":
		ipLimit, err := strconv.Atoi(text)
		if err != nil || ipLimit <= 0 {
			replyError(bot, chatID, "Jumlah IP harus berupa angka positif.")
			return
		}
		if err := validateIpLimit(ipLimit); err != nil {
			replyError(bot, chatID, err.Error())
			return
		}
		mutex.Lock()
		tempUserData[userID]["ip_limit"] = strconv.Itoa(ipLimit)
		mutex.Unlock()
		userStates[userID] = "create_protocols"
		sendMessage(bot, chatID, "🧩 Masukkan protokol (contoh: udp, ssh, ws) atau `all`:")
	case "create_password":
		if !validatePassword(bot, chatID, text) {
			return
		}
		mutex.Lock()
		tempUserData[userID]["password"] = text
		mutex.Unlock()
		userStates[userID] = "create_days"
		sendMessage(bot, chatID, "⏳ Masukkan durasi (hari):")
	case "create_protocols":
		protocols, ok := parseProtocolsInput(bot, chatID, text)
		if !ok {
			return
		}
		mutex.Lock()
		tempUserData[userID]["protocols"] = strings.Join(protocols, ",")
		mutex.Unlock()
		days, err := strconv.Atoi(tempUserData[userID]["days"])
		if err != nil || days <= 0 {
			replyError(bot, chatID, "Durasi paket tidak valid.")
			resetState(userID)
			return
		}
		ipLimit, err := strconv.Atoi(tempUserData[userID]["ip_limit"])
		if err != nil || ipLimit <= 0 {
			replyError(bot, chatID, "IP limit tidak valid.")
			resetState(userID)
			return
		}
		_, skipPayment := tempUserData[userID]["skip_payment"]
		if skipPayment || isAdmin(config, userID) {
			createUser(bot, chatID, tempUserData[userID]["username"], tempUserData[userID]["password"], days, ipLimit, protocols, config)
			delete(tempUserData, userID)
			delete(userStates, userID)
			return
		}
		processPayment(bot, chatID, userID, days, ipLimit, config)

	case "renew_protocols":
		if !isAdmin(config, userID) {
			replyError(bot, chatID, "Akses Ditolak.")
			resetState(userID)
			return
		}
		if text == "" || strings.EqualFold(text, "-") {
			userStates[userID] = "renew_days"
			sendMessage(bot, chatID, "⏳ Masukkan tambahan durasi (hari):")
			return
		}
		protocols, ok := parseProtocolsInput(bot, chatID, text)
		if !ok {
			return
		}
		tempUserData[userID]["protocols"] = strings.Join(protocols, ",")
		userStates[userID] = "renew_days"
		sendMessage(bot, chatID, "⏳ Masukkan tambahan durasi (hari):")
	case "renew_days":
		if !isAdmin(config, userID) {
			replyError(bot, chatID, "Akses Ditolak.")
			resetState(userID)
			return
		}
		days, ok := validateNumber(bot, chatID, text, 1, 9999, "Durasi")
		if !ok {
			return
		}
		renewUser(bot, chatID, tempUserData[userID]["username"], days, tempUserData[userID]["protocols"], config)
		resetState(userID)

	case "admin_add":
		adminID, ok := parseAdminIDInput(bot, chatID, text)
		if !ok {
			return
		}
		if err := addAdmin(config, adminID); err != nil {
			replyError(bot, chatID, err.Error())
			return
		}
		if err := saveConfig(config); err != nil {
			replyError(bot, chatID, "Gagal menyimpan konfigurasi.")
			return
		}
		resetState(userID)
		sendMessage(bot, chatID, fmt.Sprintf("✅ Admin berhasil ditambahkan: %d", adminID))
		showMainMenu(bot, chatID, config)

	case "admin_remove":
		adminID, ok := parseAdminIDInput(bot, chatID, text)
		if !ok {
			return
		}
		if err := removeAdmin(config, adminID); err != nil {
			replyError(bot, chatID, err.Error())
			return
		}
		if err := saveConfig(config); err != nil {
			replyError(bot, chatID, "Gagal menyimpan konfigurasi.")
			return
		}
		resetState(userID)
		sendMessage(bot, chatID, fmt.Sprintf("✅ Admin berhasil dihapus: %d", adminID))
		showMainMenu(bot, chatID, config)
	case "torrent_custom_rules":
		rules := strings.TrimSpace(text)
		if rules == "" {
			replyError(bot, chatID, "Aturan torrent tidak boleh kosong.")
			resetState(userID)
			return
		}
		if err := saveTorrentCustomRules(rules); err != nil {
			replyError(bot, chatID, "Gagal menyimpan aturan torrent: "+err.Error())
			resetState(userID)
			return
		}
		if err := applyTorrentRules(); err != nil {
			replyError(bot, chatID, "Gagal menerapkan aturan torrent: "+err.Error())
			resetState(userID)
			return
		}
		resetState(userID)
		sendMessage(bot, chatID, "✅ Aturan torrent berhasil diperbarui.")
		showMainMenu(bot, chatID, config)
	}
}

// ==========================================
// Feature Implementation
// ==========================================

func startCreateUser(bot *tgbotapi.BotAPI, chatID int64, userID int64, skipPayment bool) {
	mutex.Lock()
	tempUserData[userID] = make(map[string]string)
	tempUserData[userID]["chat_id"] = strconv.FormatInt(chatID, 10)
	if skipPayment {
		tempUserData[userID]["skip_payment"] = "true"
	}
	mutex.Unlock()
	userStates[userID] = "create_username"
	sendMessage(bot, chatID, "👤 Masukkan username untuk akun:")
}

func startRenewUser(bot *tgbotapi.BotAPI, chatID int64, userID int64, data string) {
	username := strings.TrimPrefix(data, "select_renew:")
	tempUserData[userID] = map[string]string{"username": username}
	userStates[userID] = "renew_protocols"
	sendMessage(bot, chatID, fmt.Sprintf("🔄 Renewing %s\n🧩 Masukkan protokol baru (pisahkan koma) atau kosong untuk mempertahankan:", username))
}

func confirmDeleteUser(bot *tgbotapi.BotAPI, chatID int64, data string) {
	username := strings.TrimPrefix(data, "select_delete:")
	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("❓ Yakin ingin menghapus user `%s`?", username))
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Ya, Hapus", "confirm_delete:"+username),
			tgbotapi.NewInlineKeyboardButtonData("❌ Batal", "cancel"),
		),
	)
	sendAndTrack(bot, msg)
}

func handlePagination(bot *tgbotapi.BotAPI, chatID int64, data string) {
	parts := strings.Split(data, ":")
	action := parts[0][5:] // remove "page_"
	page, _ := strconv.Atoi(parts[1])
	showUserSelection(bot, chatID, page, action)
}

func checkPaymentStatus(bot *tgbotapi.BotAPI, chatID int64, userID int64) {
	mutex.Lock()
	data, exists := tempUserData[userID]
	mutex.Unlock()
	if !exists {
		sendMessage(bot, chatID, "❌ Tidak ada pesanan aktif.")
		return
	}

	orderID, ok := data["order_id"]
	if !ok {
		sendMessage(bot, chatID, "❌ Tidak ada pesanan aktif.")
		return
	}

	showLoading(bot, chatID, "Mengecek status pembayaran...")

	config, _ := loadConfig()
	status, err := checkPakasirStatus(&config, orderID, data["price"])

	hideLoading(bot, chatID)

	if err != nil {
		replyError(bot, chatID, "Gagal mengecek status: "+err.Error())
		return
	}

	statusEmoji := getStatusEmoji(status)
	msg := fmt.Sprintf("%s Status Pembayaran: %s\n\nOrder ID: `%s`", statusEmoji, strings.ToUpper(status), orderID)

	reply := tgbotapi.NewMessage(chatID, msg)
	reply.ParseMode = "Markdown"
	bot.Send(reply)
}

func showSupport(bot *tgbotapi.BotAPI, chatID int64) {
	msg := tgbotapi.NewMessage(chatID, `╔════════════════════════╗
║          📞 CUSTOMER SUPPORT             ║
╚════════════════════════╝
💬 Butuh bantuan?
Hubungi kami melalui:
📱 Telegram: @shwtrya
📧 Email: shawavatritya@gmail.com
🌐 Website: https://wapaa.netlify.app
⏰ Jam Operasional:
Senin - Jumat: 09:00 - 20:00 WIB
Sabtu: 09:00 - 15:00 WIB
Minggu: Libur
━━━━━━━━━━━━━━━━━━━━━━━━
Kami siap membantu Anda! 😊`)
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🏠 Menu Utama", "cancel"),
		),
	)
	sendAndTrack(bot, msg)
}

func showGuide(bot *tgbotapi.BotAPI, chatID int64) {
	msg := tgbotapi.NewMessage(chatID, `╔════════════════════════╗
║          📖 PANDUAN SETUP VPN            ║
╚════════════════════════╝
🚀 LANGKAH-LANGKAH:
1️⃣ Download Aplikasi
• Android: ZIVPN
2️⃣ Import Config
• Buka aplikasi
• Import file config
• Atau input manual
3️⃣ Masukkan Akun
• Username: [dari bot]
• Password: [dari bot]
• Server/ip: [domain]
4️⃣ Connect
• Klik tombol connect
• Tunggu hingga tersambung
• Selamat browsing! 🎉
━━━━━━━━━━━━━━━━━━━━━━━━
📹 Video Tutorial:
youtube.com/results?search_query=zivpn
❓ Masih bingung?
Hubungi support kami!`)
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📞 Support", "menu_support"),
			tgbotapi.NewInlineKeyboardButtonData("🏠 Menu Utama", "cancel"),
		),
	)
	sendAndTrack(bot, msg)
}

func processPayment(bot *tgbotapi.BotAPI, chatID int64, userID int64, days int, ipLimit int, config *BotConfig) {
	extraIP := 0
	if ipLimit > 1 {
		extraIP = ipLimit - 1
	}
	basePrice := days * config.DailyPrice
	ipPrice := extraIP * 2000
	totalPrice := basePrice + ipPrice

	if totalPrice < 500 {
		sendMessage(bot, chatID, fmt.Sprintf("❌ Total harga Rp %s\n\n⚠️ Minimal transaksi adalah Rp 500\nSilakan tambah durasi atau IP limit.", formatRupiah(totalPrice)))
		return
	}

	showLoading(bot, chatID, "Membuat invoice pembayaran...")

	orderID := fmt.Sprintf("ZIVPN-%d-%d", userID, time.Now().Unix())

	payment, err := createPakasirTransaction(config, orderID, totalPrice)

	hideLoading(bot, chatID)

	if err != nil {
		replyError(bot, chatID, "Gagal membuat pembayaran: "+err.Error())
		resetState(userID)
		return
	}

	mutex.Lock()
	tempUserData[userID]["order_id"] = orderID
	tempUserData[userID]["price"] = strconv.Itoa(totalPrice)
	mutex.Unlock()

	qrUrl := fmt.Sprintf("https://api.qrserver.com/v1/create-qr-code/?size=400x400&data=%s", payment.PaymentNumber)

	msgText := fmt.Sprintf(`╔════════════════════════╗
║          💳 INVOICE PEMBAYARAN          ║
╚════════════════════════╝
📝 DETAIL PESANAN
━━━━━━━━━━━━━━━━━━━━━━━━
👤 Username    : %s
🔑 Password    : %s
⏰ Durasi      : %d Hari
🌐 IP Limit    : %d Device
💰 RINCIAN HARGA
━━━━━━━━━━━━━━━━━━━━━━━━

Biaya Dasar  : Rp %s
IP Tambahan  : Rp %s
─────────
🔖 TOTAL       : Rp %s

⏱️ BATAS WAKTU
━━━━━━━━━━━━━━━━━━━━━━━━
📅 Expired     : %s
━━━━━━━━━━━━━━━━━━━━━━━━
📱 Scan QR Code di atas untuk
melakukan pembayaran
🔄 Pembayaran akan dicek otomatis
setiap 1 menit
💡 Tips: Screenshot QR code ini
untuk pembayaran nanti`,
		tempUserData[userID]["username"],
		tempUserData[userID]["password"],
		days,
		ipLimit,
		formatRupiah(basePrice),
		formatRupiah(ipPrice),
		formatRupiah(totalPrice),
		formatDate(payment.ExpiredAt),
	)
	photo := tgbotapi.NewPhoto(chatID, tgbotapi.FileURL(qrUrl))
	photo.Caption = msgText

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 Cek Status", "menu_check_payment"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📞 Bantuan", "menu_support"),
			tgbotapi.NewInlineKeyboardButtonData("❌ Batalkan", "cancel"),
		),
	)
	photo.ReplyMarkup = keyboard

	deleteLastMessage(bot, chatID)
	sentMsg, err := bot.Send(photo)
	if err == nil {
		lastMessageIDs[chatID] = sentMsg.MessageID
	}

	delete(userStates, userID)
}

func startPaymentChecker(bot *tgbotapi.BotAPI, config *BotConfig) {
	ticker := time.NewTicker(1 * time.Minute)
	for range ticker.C {
		mutex.Lock()
		for userID, data := range tempUserData {
			if orderID, ok := data["order_id"]; ok {
				price := data["price"]
				chatID, _ := strconv.ParseInt(data["chat_id"], 10, 64)
				status, err := checkPakasirStatus(config, orderID, price)
				if err == nil && (status == "completed" || status == "success") {
					username := data["username"]
					password := data["password"]
					days, err := strconv.Atoi(data["days"])
					if err != nil || days <= 0 {
						log.Printf("Invalid days for user %d: %v", userID, err)
						delete(tempUserData, userID)
						delete(userStates, userID)
						continue
					}
					ipLimit, err := strconv.Atoi(data["ip_limit"])
					if err != nil || ipLimit <= 0 {
						log.Printf("Invalid ip limit for user %d: %v", userID, err)
						delete(tempUserData, userID)
						delete(userStates, userID)
						continue
					}
					protocols := parseProtocolsCSV(data["protocols"])

					// Send notification about successful payment
					successMsg := fmt.Sprintf("🎉 Pembayaran Berhasil!\n\n✅ Order ID: %s\n⏳ Sedang membuat akun...", orderID)
					bot.Send(tgbotapi.NewMessage(chatID, successMsg))

					createUser(bot, chatID, username, password, days, ipLimit, protocols, config)
					delete(tempUserData, userID)
					delete(userStates, userID)
				} else if err != nil {
					log.Printf("Error checking payment for %d: %v", userID, err)
				}
			}
		}
		mutex.Unlock()
	}
}

func createUser(bot *tgbotapi.BotAPI, chatID int64, username string, password string, days int, ipLimit int, protocols []string, config *BotConfig) {
	showLoading(bot, chatID, "Membuat akun VPN...")
	if userCount, err := getUserCount(); err == nil && userCount >= MaxAccounts {
		hideLoading(bot, chatID)
		replyError(bot, chatID, "❌ Stok akun habis\n\n📦 Silakan coba lagi nanti atau hubungi admin.")
		return
	}

	payload := map[string]interface{}{
		"username":  username,
		"password":  password,
		"days":      days,
		"ip_limit":  ipLimit,
		"protocols": protocols,
	}

	res, err := apiCall("POST", "/user/create", payload)

	hideLoading(bot, chatID)

	if err != nil {
		replyError(bot, chatID, "❌ Error API: "+err.Error())
		return
	}

	if res["success"] == true {
		data := res["data"].(map[string]interface{})
		if value, ok := data["username"]; !ok || value == nil || fmt.Sprintf("%v", value) == "" {
			data["username"] = username
		}
		sendAccountInfo(bot, chatID, data, config)
	} else {
		replyError(bot, chatID, fmt.Sprintf("❌ Gagal membuat akun: %s", res["message"]))
	}
}

func renewUser(bot *tgbotapi.BotAPI, chatID int64, username string, days int, protocols string, config *BotConfig) {
	showLoading(bot, chatID, "Memperpanjang akun...")
	payload := map[string]interface{}{
		"password": username,
		"days":     days,
	}
	if protocols != "" {
		payload["protocols"] = parseProtocolsCSV(protocols)
	}

	res, err := apiCall("POST", "/user/renew", payload)

	hideLoading(bot, chatID)

	if err != nil {
		replyError(bot, chatID, "❌ Error API: "+err.Error())
		return
	}

	if res["success"] == true {
		data := res["data"].(map[string]interface{})
		sendAccountInfo(bot, chatID, data, config)
	} else {
		replyError(bot, chatID, fmt.Sprintf("❌ Gagal: %s", res["message"]))
		showMainMenu(bot, chatID, config)
	}
}

func deleteUser(bot *tgbotapi.BotAPI, chatID int64, username string, config *BotConfig) {
	showLoading(bot, chatID, "Menghapus akun...")
	res, err := apiCall("POST", "/user/delete", map[string]interface{}{
		"password": username,
	})

	hideLoading(bot, chatID)

	if err != nil {
		replyError(bot, chatID, "❌ Error API: "+err.Error())
		return
	}

	if res["success"] == true {
		msg := tgbotapi.NewMessage(chatID, "✅ Akun berhasil dihapus.")
		deleteLastMessage(bot, chatID)
		bot.Send(msg)
		showMainMenu(bot, chatID, config)
	} else {
		replyError(bot, chatID, fmt.Sprintf("❌ Gagal: %s", res["message"]))
		showMainMenu(bot, chatID, config)
	}
}

func listUsers(bot *tgbotapi.BotAPI, chatID int64) {
	showLoading(bot, chatID, "Memuat daftar akun...")
	res, err := apiCall("GET", "/users", nil)

	hideLoading(bot, chatID)

	if err != nil {
		replyError(bot, chatID, "❌ Error API: "+err.Error())
		return
	}

	if res["success"] == true {
		users := res["data"].([]interface{})
		if len(users) == 0 {
			msg := tgbotapi.NewMessage(chatID, "📂 Tidak ada user terdaftar.")
			msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("⬅️ Kembali", "back_admin_panel"),
				),
			)
			sendAndTrack(bot, msg)
			return
		}

		msg := `╔════════════════════════╗
║             📂 DAFTAR AKUN VPN              ║
╚════════════════════════╝
`
		for i, u := range users {
			user := u.(map[string]interface{})
			statusEmoji := getStatusEmoji(fmt.Sprintf("%v", user["status"]))
			expiredDate := formatDate(fmt.Sprintf("%v", user["expired"]))

			msg += fmt.Sprintf("%d. %s `%s`\n   📅 %s\n\n",
				i+1,
				statusEmoji,
				user["password"],
				expiredDate,
			)
		}

		reply := tgbotapi.NewMessage(chatID, msg)
		reply.ParseMode = "Markdown"
		reply.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("⬅️ Kembali", "back_admin_panel"),
			),
		)
		sendAndTrack(bot, reply)
	} else {
		replyError(bot, chatID, "❌ Gagal mengambil data.")
	}
}

// ==========================================
// Pakasir API
// ==========================================

type PakasirPayment struct {
	PaymentNumber string `json:"payment_number"`
	ExpiredAt     string `json:"expired_at"`
}

func createPakasirTransaction(config *BotConfig, orderID string, amount int) (*PakasirPayment, error) {
	url := "https://app.pakasir.com/api/transactioncreate/qris"
	payload := map[string]interface{}{
		"project":  config.PakasirSlug,
		"order_id": orderID,
		"amount":   amount,
		"api_key":  config.PakasirApiKey,
	}
	jsonPayload, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", url, bytes.NewBuffer(jsonPayload))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	if paymentData, ok := result["payment"].(map[string]interface{}); ok {
		return &PakasirPayment{
			PaymentNumber: paymentData["payment_number"].(string),
			ExpiredAt:     paymentData["expired_at"].(string),
		}, nil
	}
	return nil, fmt.Errorf("invalid response from Pakasir")
}

func checkPakasirStatus(config *BotConfig, orderID string, amountStr string) (string, error) {
	url := fmt.Sprintf("https://app.pakasir.com/api/transactiondetail?project=%s&amount=%s&order_id=%s&api_key=%s",
		config.PakasirSlug, amountStr, orderID, config.PakasirApiKey)
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	if transaction, ok := result["transaction"].(map[string]interface{}); ok {
		return transaction["status"].(string), nil
	}
	return "", fmt.Errorf("transaction not found")
}

// ==========================================
// UI & Helpers
// ==========================================

func showMainMenu(bot *tgbotapi.BotAPI, chatID int64, config *BotConfig) {
	ipInfo, _ := getIpInfo()
	domain := config.Domain
	if domain == "" {
		domain = "Belum dikonfigurasi"
	}
	stockLine := "• Stok       : Memuat..."
	stockEmoji := "📦"
	if stockInfo, err := getStockInfo(); err == nil {
		percentage := float64(stockInfo.Used) / float64(stockInfo.Max) * 100
		if percentage >= 90 {
			stockEmoji = "🔴"
		} else if percentage >= 70 {
			stockEmoji = "🟡"
		} else {
			stockEmoji = "🟢"
		}

		progressBar := showProgressBar(int(percentage))
		stockLine = fmt.Sprintf("• Stok       : %s %d/%d\n  %s %.0f%%",
			stockEmoji, stockInfo.Used, stockInfo.Max, progressBar, percentage)
	}

	msgText := fmt.Sprintf(`╔════════════════════════╗
║             🚀 ZiVPN UDP STORE                ║
║                     Premium VPN                      ║
╚════════════════════════╝
📊 INFORMASI SERVER
━━━━━━━━━━━━━━━━━━━━━━━━
🌐 Domain     : %s
🏙️ Lokasi     : %s
📡 Provider   : %s
💰 Harga      : Rp %s/hari
%s
⚡ Status     : Online ✅
━━━━━━━━━━━━━━━━━━━━━━━━
🎯 Silakan pilih layanan:`,
		domain,
		ipInfo.City,
		ipInfo.Isp,
		formatRupiah(config.DailyPrice),
		stockLine,
	)
	msg := tgbotapi.NewMessage(chatID, msgText)

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🛒 Beli Akun Premium", "menu_create"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("💳 Cek Pembayaran", "menu_check_payment"),
			tgbotapi.NewInlineKeyboardButtonData("📞 Support", "menu_support"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📖 Panduan Setup", "menu_guide"),
		),
	)

	if isAdmin(config, chatID) {
		keyboard.InlineKeyboard = append(keyboard.InlineKeyboard,
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("📊 Dashboard", "menu_info"),
				tgbotapi.NewInlineKeyboardButtonData("🛠️ Admin Panel", "menu_admin"),
			),
		)
	}

	msg.ReplyMarkup = keyboard
	sendAndTrack(bot, msg)
}

func sendAccountInfo(bot *tgbotapi.BotAPI, chatID int64, data map[string]interface{}, config *BotConfig) {
	ipInfo, _ := getIpInfo()
	domain := config.Domain
	if domain == "" {
		domain = "Tidak tersedia"
	}
	protocolInfo := formatProtocols(data)
	username := extractUsername(data)
	ipLimit := extractValue(data, "ip_limit", "-")

	accountInfo := fmt.Sprintf("Username: %s\nPassword: %s\nExpired: %s\nIP Limit: %s\nProtocols: %s\nDomain: %s",
		username, data["password"], data["expired"], ipLimit, protocolInfo, domain,
	)

	mutex.Lock()
	lastAccountInfos[chatID] = accountInfo
	mutex.Unlock()

	expiredDate := formatDate(fmt.Sprintf("%v", data["expired"]))

	msg := fmt.Sprintf(`╔════════════════════════╗
║          ✅ AKUN PREMIUM AKTIF           ║
╚════════════════════════╝
🔐 DETAIL AKUN
━━━━━━━━━━━━━━━━━━━━━━━━
👤 Username  : %s
🔑 Password  : %s
⏰ Expired   : %s
🌐 IP Limit  : %s
🧩 PROTOKOL TERSEDIA
━━━━━━━━━━━━━━━━━━━━━━━━
%s
🌍 INFORMASI SERVER
━━━━━━━━━━━━━━━━━━━━━━━━
🔗 Domain    : %s
🏙️ Lokasi    : %s
📡 ISP       : %s
━━━━━━━━━━━━━━━━━━━━━━━━
💡 Gunakan detail di atas untuk
konfigurasi aplikasi VPN Anda
🙏 Terima kasih telah berlangganan!`,
		username,
		data["password"],
		expiredDate,
		ipLimit,
		formatProtocolsDetailed(data),
		domain,
		ipInfo.City,
		ipInfo.Isp,
	)
	reply := tgbotapi.NewMessage(chatID, msg)
	reply.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📋 Salin Detail", "copy_account"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📖 Panduan Setup", "menu_guide"),
			tgbotapi.NewInlineKeyboardButtonData("🏠 Menu Utama", "cancel"),
		),
	)
	deleteLastMessage(bot, chatID)
	bot.Send(reply)
}

func showUserSelection(bot *tgbotapi.BotAPI, chatID int64, page int, action string) {
	showLoading(bot, chatID, "Memuat daftar user...")
	users, err := getUsers()

	hideLoading(bot, chatID)

	if err != nil {
		replyError(bot, chatID, "❌ Gagal mengambil data user.")
		return
	}

	if len(users) == 0 {
		sendMessage(bot, chatID, "📂 Tidak ada user terdaftar.")
		return
	}

	perPage := 10
	totalPages := (len(users) + perPage - 1) / perPage

	if page < 1 {
		page = 1
	}
	if page > totalPages {
		page = totalPages
	}

	start := (page - 1) * perPage
	end := start + perPage
	if end > len(users) {
		end = len(users)
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	for _, u := range users[start:end] {
		statusEmoji := getStatusEmoji(u.Status)
		label := fmt.Sprintf("%s %s (%s)", statusEmoji, u.Password, u.Status)
		data := fmt.Sprintf("select_%s:%s", action, u.Password)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, data),
		))
	}

	var navRow []tgbotapi.InlineKeyboardButton
	if page > 1 {
		navRow = append(navRow, tgbotapi.NewInlineKeyboardButtonData("⬅️ Prev", fmt.Sprintf("page_%s:%d", action, page-1)))
	}
	if page < totalPages {
		navRow = append(navRow, tgbotapi.NewInlineKeyboardButtonData("Next ➡️", fmt.Sprintf("page_%s:%d", action, page+1)))
	}
	if len(navRow) > 0 {
		rows = append(rows, navRow)
	}

	rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("❌ Batal", "cancel")))

	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("📋 Pilih User untuk %s\n\nHalaman %d/%d", strings.Title(action), page, totalPages))
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	sendAndTrack(bot, msg)
}

func sendMessage(bot *tgbotapi.BotAPI, chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	if _, inState := userStates[chatID]; inState {
		cancelKb := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("❌ Batal", "cancel")),
		)
		msg.ReplyMarkup = cancelKb
	}
	sendAndTrack(bot, msg)
}

func replyError(bot *tgbotapi.BotAPI, chatID int64, text string) {
	sendMessage(bot, chatID, "❌ "+text)
}

func saveTorrentCustomRules(rules string) error {
	rulesText := strings.TrimSpace(rules)
	if rulesText == "" {
		return fmt.Errorf("aturan kosong")
	}
	if !strings.HasSuffix(rulesText, "\n") {
		rulesText += "\n"

	}
	return ioutil.WriteFile(TorrentRulesFile, []byte(rulesText), 0644)
}

func applyTorrentRules() error {
	if _, err := os.Stat(TorrentApplyScript); err != nil {
		return err
	}
	output, err := exec.Command(TorrentApplyScript).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func applyHighPerformance() (string, error) {
	sysctlConfig := strings.TrimSpace(`
net.core.default_qdisc=fq
net.ipv4.tcp_congestion_control=bbr
net.ipv4.ip_forward=1
net.netfilter.nf_conntrack_max=524288
net.netfilter.nf_conntrack_udp_timeout=120
net.netfilter.nf_conntrack_udp_timeout_stream=300
net.core.rmem_max=16777216
net.core.wmem_max=16777216
net.core.rmem_default=262144
net.core.wmem_default=262144
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
`)
	return applyPerformancePreset("High Performance", "/etc/sysctl.d/99-zivpn-high-performance.conf", sysctlConfig)
}

func applyConservativePerformance() (string, error) {
	sysctlConfig := strings.TrimSpace(`
net.core.default_qdisc=fq
net.ipv4.tcp_congestion_control=bbr
net.ipv4.ip_forward=1
net.netfilter.nf_conntrack_max=131072
net.netfilter.nf_conntrack_udp_timeout=120
net.netfilter.nf_conntrack_udp_timeout_stream=300
net.core.rmem_max=8388608
net.core.wmem_max=8388608
net.core.rmem_default=262144
net.core.wmem_default=262144
net.core.optmem_max=65536
net.core.somaxconn=32768
net.core.netdev_max_backlog=8192
net.core.netdev_budget=300
net.core.rps_sock_flow_entries=32768
net.ipv4.tcp_rmem=4096 87380 8388608
net.ipv4.tcp_wmem=4096 65536 8388608
net.ipv4.tcp_fastopen=3
fs.file-max=500000
net.ipv4.udp_mem=131072 262144 524288
net.ipv4.udp_rmem_min=8192
net.ipv4.udp_wmem_min=8192
`)
	return applyPerformancePreset("Conservative", "/etc/sysctl.d/99-zivpn-conservative.conf", sysctlConfig)
}

func applyPerformancePreset(label, sysctlConfigPath, sysctlConfig string) (string, error) {
	const sysctlBackupPath = "/etc/sysctl.d/99-zivpn-performance.backup"
	keys := []string{
		"net.core.default_qdisc",
		"net.ipv4.tcp_congestion_control",
		"net.ipv4.ip_forward",
		"net.netfilter.nf_conntrack_max",
		"net.netfilter.nf_conntrack_udp_timeout",
		"net.netfilter.nf_conntrack_udp_timeout_stream",
		"net.core.rmem_max",
		"net.core.wmem_max",
		"net.core.rmem_default",
		"net.core.wmem_default",
		"net.core.optmem_max",
		"net.core.somaxconn",
		"net.core.netdev_max_backlog",
		"net.core.netdev_budget",
		"net.core.rps_sock_flow_entries",
		"net.ipv4.tcp_rmem",
		"net.ipv4.tcp_wmem",
		"net.ipv4.tcp_fastopen",
		"fs.file-max",
		"net.ipv4.udp_mem",
		"net.ipv4.udp_rmem_min",
		"net.ipv4.udp_wmem_min",
	}
	if err := backupSysctlSettings(keys, sysctlBackupPath); err != nil {
		return "", fmt.Errorf("gagal backup sysctl: %w", err)
	}
	if err := ioutil.WriteFile(sysctlConfigPath, []byte(sysctlConfig+"\n"), 0644); err != nil {
		return "", err
	}
	if output, err := exec.Command("sysctl", "--system").CombinedOutput(); err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(string(output)))
	}
	statusBytes, err := ioutil.ReadFile("/proc/sys/net/ipv4/tcp_congestion_control")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s preset aktif (tcp_congestion_control=%s)", label, strings.TrimSpace(string(statusBytes))), nil
}

func backupSysctlSettings(keys []string, backupPath string) error {
	var builder strings.Builder
	builder.WriteString("# ZiVPN sysctl backup - ")
	builder.WriteString(time.Now().Format(time.RFC3339))
	builder.WriteString("\n")
	for _, key := range keys {
		output, err := exec.Command("sysctl", "-n", key).CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s: %v: %s", key, err, strings.TrimSpace(string(output)))
		}
		value := strings.TrimSpace(string(output))
		builder.WriteString(fmt.Sprintf("%s=%s\n", key, value))
	}
	return ioutil.WriteFile(backupPath, []byte(builder.String()), 0644)
}

func revertPerformanceSettings() (string, error) {
	const backupPath = "/etc/sysctl.d/99-zivpn-performance.backup"
	if _, err := os.Stat(backupPath); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("backup sysctl tidak ditemukan di %s", backupPath)
		}
		return "", err
	}
	if output, err := exec.Command("sysctl", "--system").CombinedOutput(); err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(string(output)))
	}
	return "Pengaturan sysctl dikembalikan dari backup.", nil
}

func cancelOperation(bot *tgbotapi.BotAPI, chatID int64, userID int64, config *BotConfig) {
	if state, ok := userStates[userID]; ok && state == "torrent_custom_rules" {
		delete(userStates, userID)
		delete(tempUserData, userID)
	} else {
		resetState(userID)
	}
	showMainMenu(bot, chatID, config)
}

func sendAndTrack(bot *tgbotapi.BotAPI, msg tgbotapi.MessageConfig) {
	deleteLastMessage(bot, msg.ChatID)
	sentMsg, err := bot.Send(msg)
	if err == nil {
		lastMessageIDs[msg.ChatID] = sentMsg.MessageID
	}
}

func deleteLastMessage(bot *tgbotapi.BotAPI, chatID int64) {
	if msgID, ok := lastMessageIDs[chatID]; ok {
		deleteMsg := tgbotapi.NewDeleteMessage(chatID, msgID)
		bot.Request(deleteMsg)
		delete(lastMessageIDs, chatID)
	}
}

func resetState(userID int64) {
	delete(userStates, userID)
	delete(tempUserData, userID)
}

func validatePassword(bot *tgbotapi.BotAPI, chatID int64, text string) bool {
	if len(text) < 3 || len(text) > 20 {
		sendMessage(bot, chatID, "❌ Password harus 3-20 karakter. Coba lagi:")
		return false
	}
	if !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(text) {
		sendMessage(bot, chatID, "❌ Password hanya boleh huruf, angka, - dan _. Coba lagi:")
		return false
	}
	return true
}

func validateUsername(bot *tgbotapi.BotAPI, chatID int64, text string) bool {
	if len(text) < 3 || len(text) > 20 {
		sendMessage(bot, chatID, "❌ Username harus 3-20 karakter. Coba lagi:")
		return false
	}
	if !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(text) {
		sendMessage(bot, chatID, "❌ Username hanya boleh huruf, angka, - dan _. Coba lagi:")
		return false
	}
	return true
}

func validateNumber(bot *tgbotapi.BotAPI, chatID int64, text string, min, max int, fieldName string) (int, bool) {
	val, err := strconv.Atoi(text)
	if err != nil || val < min || val > max {
		sendMessage(bot, chatID, fmt.Sprintf("❌ %s harus angka positif (%d-%d). Coba lagi:", fieldName, min, max))
		return 0, false
	}
	return val, true
}

func validateIpLimit(limit int) error {
	if limit < 1 || limit > 2 {
		return fmt.Errorf("IP limit harus antara 1-2")
	}
	return nil
}

func parseProtocolsCSV(protocols string) []string {
	parts := strings.Split(protocols, ",")
	results := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value != "" {
			results = append(results, value)
		}
	}
	return results
}

func parseProtocolsInput(bot *tgbotapi.BotAPI, chatID int64, text string) ([]string, bool) {
	supported := map[string]bool{
		"udp":      true,
		"ssh":      true,
		"dropbear": true,
		"ws":       true,
		"ssl":      true,
	}
	input := strings.TrimSpace(strings.ToLower(text))
	if input == "" {
		sendMessage(bot, chatID, "❌ Protokol wajib diisi. Contoh: udp, ssh, ws")
		return nil, false
	}
	if input == "all" {
		return []string{"udp", "ssh", "dropbear", "ws", "ssl"}, true
	}
	parts := strings.Split(input, ",")
	seen := map[string]bool{}
	var protocols []string
	for _, part := range parts {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		if !supported[p] {
			sendMessage(bot, chatID, fmt.Sprintf("❌ Protokol tidak dikenal: %s", p))
			return nil, false
		}
		if !seen[p] {
			seen[p] = true
			protocols = append(protocols, p)
		}
	}
	if len(protocols) == 0 {
		sendMessage(bot, chatID, "❌ Protokol wajib diisi. Contoh: udp, ssh, ws")
		return nil, false
	}
	return protocols, true
}

func formatProtocols(data map[string]interface{}) string {
	if value, ok := data["protocols"]; ok && value != nil {
		switch v := value.(type) {
		case string:
			if v != "" {
				return v
			}
		case []interface{}:
			parts := []string{}
			for _, item := range v {
				if s, ok := item.(string); ok {
					parts = append(parts, s)
				}
			}
			if len(parts) > 0 {
				return strings.Join(parts, ",")
			}
		}
	}
	return "udp"
}

func formatProtocolsDetailed(data map[string]interface{}) string {
	protocols := extractProtocols(data)
	if len(protocols) == 0 {
		return "✓ UDP (Default)"
	}
	var lines []string
	for _, proto := range protocols {
		emoji := getProtocolEmoji(proto)
		lines = append(lines, fmt.Sprintf("%s %s", emoji, strings.ToUpper(proto)))
	}

	return strings.Join(lines, "\n")
}

func extractUsername(data map[string]interface{}) string {
	if value, ok := data["username"]; ok && value != nil {
		if str := strings.TrimSpace(fmt.Sprintf("%v", value)); str != "" {
			return str
		}
	}
	if value, ok := data["password"]; ok && value != nil {
		return strings.TrimSpace(fmt.Sprintf("%v", value))
	}
	return "-"
}

func extractValue(data map[string]interface{}, key, defaultVal string) string {
	if value, ok := data[key]; ok && value != nil {
		return fmt.Sprintf("%v", value)
	}
	return defaultVal
}

func extractProtocols(data map[string]interface{}) []string {
	if value, ok := data["protocols"]; ok && value != nil {
		switch v := value.(type) {
		case string:
			if v != "" {
				return strings.Split(v, ",")
			}
		case []interface{}:
			var result []string
			for _, item := range v {
				if s, ok := item.(string); ok {
					result = append(result, s)
				}
			}
			return result
		}
	}
	return []string{"udp"}
}

func systemInfo(bot *tgbotapi.BotAPI, chatID int64, config *BotConfig) {
	showLoading(bot, chatID, "Mengambil informasi sistem...")
	res, err := apiCall("GET", "/info", nil)

	hideLoading(bot, chatID)

	if err != nil {
		replyError(bot, chatID, "❌ Error API: "+err.Error())
		return
	}

	if res["success"] == true {
		data := res["data"].(map[string]interface{})
		ipInfo, _ := getIpInfo()
		domain := getInfoValue(data, "domain", config.Domain)

		domainStatus := "❌ Domain mismatch"
		if getInfoBool(data, "domain_resolves") {
			domainStatus = "✅ Domain OK"
		}

		msg := tgbotapi.NewMessage(chatID, fmt.Sprintf(`╔════════════════════════╗
║            🖥️ INFO SISTEM ZiVPN              ║
╚════════════════════════╝
🌐 JARINGAN
━━━━━━━━━━━━━━━━━━━━━━━━

Domain        : %s
Status Domain : %s
IP Public     : %s
IP Private    : %s
Port          : %s

⚙️ SISTEM
━━━━━━━━━━━━━━━━━━━━━━━━

Service   : %s
CPU       : %s
RAM       : %s
Disk      : %s
Uptime    : %s
Load Avg  : %s
Kernel    : %s
Version   : %s

📍 LOKASI
━━━━━━━━━━━━━━━━━━━━━━━━

City      : %s
ISP       : %s

━━━━━━━━━━━━━━━━━━━━━━━━
🕐 Update: %s`,
			domain,
			domainStatus,
			getInfoValue(data, "public_ip", "-"),
			getInfoValue(data, "private_ip", "-"),
			getInfoValue(data, "port", "-"),
			getInfoValue(data, "service", "-"),
			getInfoValue(data, "cpu", "-"),
			getInfoValue(data, "ram", "-"),
			getInfoValue(data, "disk", "-"),
			getInfoValue(data, "uptime", "-"),
			getInfoValue(data, "load_avg", "-"),
			getInfoValue(data, "kernel", "-"),
			getInfoValue(data, "zivpn_version", "-"),
			ipInfo.City,
			ipInfo.Isp,
			time.Now().Format("15:04:05, 02 Jan 2006"),
		))
		msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔄 Refresh", "menu_info"),
				tgbotapi.NewInlineKeyboardButtonData("⬅️ Kembali", "cancel"),
			),
		)
		deleteLastMessage(bot, chatID)
		sendAndTrack(bot, msg)
	} else {
		replyError(bot, chatID, "❌ Gagal mengambil info.")
	}
}

func getInfoValue(data map[string]interface{}, key, fallback string) string {
	if value, ok := data[key]; ok && value != nil {
		if str, ok := value.(string); ok && str != "" {
			return str
		}
	}
	return fallback
}

func getInfoInt(data map[string]interface{}, key string, fallback int) int {
	if value, ok := data[key]; ok && value != nil {
		switch v := value.(type) {
		case float64:
			return int(v)
		case int:
			return v
		case int64:
			return int(v)
		case string:
			if parsed, err := strconv.Atoi(v); err == nil {
				return parsed
			}
		}
	}
	return fallback
}

func getInfoBool(data map[string]interface{}, key string) bool {
	if value, ok := data[key]; ok && value != nil {
		if boolean, ok := value.(bool); ok {
			return boolean
		}
	}
	return false
}

type StockInfo struct {
	Max       int
	Used      int
	Available int
}

func getStockInfo() (*StockInfo, error) {
	res, err := apiCall("GET", "/info", nil)
	if err != nil {
		return nil, err
	}
	if res["success"] != true {
		return nil, fmt.Errorf("Gagal mengambil info stok")
	}

	data, ok := res["data"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("Data stok tidak valid")
	}

	maxAccounts := getInfoInt(data, "max_accounts", MaxAccounts)
	usedAccounts := getInfoInt(data, "used_accounts", 0)
	availableAccounts := getInfoInt(data, "available_accounts", maxAccounts-usedAccounts)
	if availableAccounts < 0 {
		availableAccounts = 0
	}

	return &StockInfo{
		Max:       maxAccounts,
		Used:      usedAccounts,
		Available: availableAccounts,
	}, nil
}

func getUserCount() (int, error) {
	res, err := apiCall("GET", "/users", nil)
	if err != nil {
		return 0, err
	}
	if res["success"] != true {
		return 0, fmt.Errorf("Gagal mengambil data user")
	}

	switch data := res["data"].(type) {
	case []interface{}:
		return len(data), nil
	default:
		return 0, fmt.Errorf("Format data user tidak valid")
	}
}

func getUsers() ([]UserData, error) {
	res, err := apiCall("GET", "/users", nil)
	if err != nil {
		return nil, err
	}
	if res["success"] != true {
		return nil, fmt.Errorf("failed to get users")
	}

	var users []UserData
	dataBytes, _ := json.Marshal(res["data"])
	json.Unmarshal(dataBytes, &users)
	return users, nil
}

func listOnlineUsers(bot *tgbotapi.BotAPI, chatID int64) {
	showLoading(bot, chatID, "Memuat user online...")
	res, err := apiCall("GET", "/online", nil)

	hideLoading(bot, chatID)

	if err != nil {
		replyError(bot, chatID, "❌ Error API: "+err.Error())
		return
	}

	if res["success"] == true {
		var entries []OnlineAccount
		dataBytes, _ := json.Marshal(res["data"])
		json.Unmarshal(dataBytes, &entries)
		if len(entries) == 0 {
			msg := tgbotapi.NewMessage(chatID, "📡 Tidak ada akun online saat ini.")
			msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("⬅️ Kembali", "back_admin_panel"),
				),
			)
			sendAndTrack(bot, msg)
			return
		}

		msg := fmt.Sprintf(`╔════════════════════════╗
║              📡 AKUN ONLINE (%d)              ║
╚════════════════════════╝
`, len(entries))
		for i, entry := range entries {
			name := entry.Username
			if name == "" {
				name = entry.Password
			}
			lastSeen := formatDate(entry.LastSeen)
			msg += fmt.Sprintf("%d. 🟢 `%s`\n   📍 IP: %s\n   🕐 %s\n\n", i+1, name, entry.IP, lastSeen)
		}

		reply := tgbotapi.NewMessage(chatID, msg)
		reply.ParseMode = "Markdown"
		reply.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔄 Refresh", "menu_online"),
				tgbotapi.NewInlineKeyboardButtonData("⬅️ Kembali", "back_admin_panel"),
			),
		)
		sendAndTrack(bot, reply)
	} else {
		replyError(bot, chatID, "❌ Gagal mengambil data online.")
	}
}

func showBackupRestoreMenu(bot *tgbotapi.BotAPI, chatID int64, config *BotConfig, userID int64) {
	msg := tgbotapi.NewMessage(chatID, `╔════════════════════════╗
║                  🛠️ ADMIN PANEL                   ║
╚════════════════════════╝
Pilih menu administrasi:`)
	rows := [][]tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📡 User Online", "menu_online"),
			tgbotapi.NewInlineKeyboardButtonData("📋 List Akun", "menu_admin_list"),
		),
	}

	if isAdmin(config, userID) {
		rows = append(rows,
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("➕ Buat Akun", "menu_admin_create"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔄 Perpanjang", "menu_admin_renew"),
				tgbotapi.NewInlineKeyboardButtonData("🗑️ Hapus Akun", "menu_admin_delete"),
			),
		)
	}

	if isOwner(config, userID) {
		rows = append(rows,
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("👥 Kelola Admin", "menu_admins"),
			),
		)
	}

	rows = append(rows,
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🚀 High Performance", "menu_perf_high"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🐢 Mode Hemat", "menu_perf_conservative"),
			tgbotapi.NewInlineKeyboardButtonData("♻️ Reset Default", "menu_perf_revert"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("💾 Backup Data", "menu_backup_action"),
			tgbotapi.NewInlineKeyboardButtonData("📥 Restore Data", "menu_restore_action"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🏠 Menu Utama", "cancel"),
		),
	)

	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	sendAndTrack(bot, msg)
}

func showAdminMenu(bot *tgbotapi.BotAPI, chatID int64) {
	msg := tgbotapi.NewMessage(chatID, `╔════════════════════════╗
║                 👥 KELOLA ADMIN                  ║
╚════════════════════════╝
Pilih aksi:`)
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("➕ Tambah Admin", "menu_admin_add"),
			tgbotapi.NewInlineKeyboardButtonData("➖ Hapus Admin", "menu_admin_remove"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📋 Daftar Admin", "menu_admins_list"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬅️ Kembali", "back_admin_panel"),
		),
	)
	sendAndTrack(bot, msg)
}

func startAddAdmin(bot *tgbotapi.BotAPI, chatID int64, userID int64) {
	userStates[userID] = "admin_add"
	sendMessage(bot, chatID, "👤 Masukkan Admin ID Telegram yang ingin ditambahkan:")
}

func startRemoveAdmin(bot *tgbotapi.BotAPI, chatID int64, userID int64) {
	userStates[userID] = "admin_remove"
	sendMessage(bot, chatID, "🗑️ Masukkan Admin ID Telegram yang ingin dihapus:")
}

func listAdmins(bot *tgbotapi.BotAPI, chatID int64, config *BotConfig) {
	admins := adminIDSet(config)
	if len(admins) == 0 {
		msg := tgbotapi.NewMessage(chatID, "❌ Belum ada admin terdaftar.")
		msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("⬅️ Kembali", "back_admin_panel"),
			),
		)
		sendAndTrack(bot, msg)
		return
	}
	ids := make([]int64, 0, len(admins))
	for id := range admins {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	var builder strings.Builder
	builder.WriteString( `╔════════════════════════╗
║                 📋 DAFTAR ADMIN                 ║
╚════════════════════════╝
`)
	for i, id := range ids {
		role := resolveAdminRole(config, id)
		roleEmoji := "👤"
		if role == "owner" || role == "superadmin" {
			roleEmoji = "👑"
		}
		builder.WriteString(fmt.Sprintf("%d. %s `%d`\n   Role: %s\n\n", i+1, roleEmoji, id, strings.ToUpper(role)))
	}
	msg := tgbotapi.NewMessage(chatID, builder.String())
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬅️ Kembali", "back_admin_panel"),
		),
	)
	sendAndTrack(bot, msg)
}

func performBackup(bot *tgbotapi.BotAPI, chatID int64) {
	showLoading(bot, chatID, "Membuat backup data...")
	files := []string{
		"/etc/zivpn/config.json",
		"/etc/zivpn/users.json",
		"/etc/zivpn/domain",
		"/etc/zivpn/apikey",
		"/etc/zivpn/api_port",
		"/etc/zivpn/zivpn.crt",
		"/etc/zivpn/zivpn.key",
		"/etc/zivpn/bot-config.json",
	}

	buf := new(bytes.Buffer)
	zipWriter := zip.NewWriter(buf)

	for _, file := range files {
		if _, err := os.Stat(file); os.IsNotExist(err) {
			continue
		}

		f, err := os.Open(file)
		if err != nil {
			continue
		}
		defer f.Close()

		w, err := zipWriter.Create(filepath.Base(file))
		if err != nil {
			continue
		}

		if _, err := io.Copy(w, f); err != nil {
			continue
		}
	}

	zipWriter.Close()

	hideLoading(bot, chatID)

	fileName := fmt.Sprintf("zivpn-backup-%s.zip", time.Now().Format("20060102-150405"))

	tmpFile := "/tmp/" + fileName
	if err := ioutil.WriteFile(tmpFile, buf.Bytes(), 0644); err != nil {
		replyError(bot, chatID, "❌ Gagal membuat file backup.")
		return
	}
	defer os.Remove(tmpFile)

	doc := tgbotapi.NewDocument(chatID, tgbotapi.FilePath(tmpFile))
	doc.Caption = fmt.Sprintf(`✅ BACKUP BERHASIL!
📦 File: %s
📊 Size: %s
🕐 Time: %s
⚠️ Simpan file ini dengan aman!`,
		fileName,
		formatBytes(int64(buf.Len())),
		time.Now().Format("15:04:05, 02 Jan 2006"),
	)
	deleteLastMessage(bot, chatID)
	bot.Send(doc)
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func startRestore(bot *tgbotapi.BotAPI, chatID int64, userID int64) {
	userStates[userID] = "waiting_restore_file"
	msg := tgbotapi.NewMessage(chatID, `╔════════════════════════╗
║                 📥 RESTORE DATA                  ║
╚════════════════════════╝
⬆️ Silakan kirim file ZIP backup Anda sekarang.
⚠️ PERINGATAN:

Data saat ini akan ditimpa!
Pastikan file backup valid
Bot akan restart otomatis

💡 Tip: Backup data lama sebelum restore`)
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❌ Batal", "cancel"),
		),
	)
	sendAndTrack(bot, msg)
}

func processRestoreFile(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, config *BotConfig) {
	chatID := msg.Chat.ID
	userID := msg.From.ID
	resetState(userID)
	showLoading(bot, chatID, "Memproses file backup...")

	fileID := msg.Document.FileID
	file, err := bot.GetFile(tgbotapi.FileConfig{FileID: fileID})
	if err != nil {
		hideLoading(bot, chatID)
		replyError(bot, chatID, "❌ Gagal mengunduh file.")
		return
	}

	fileUrl := file.Link(config.BotToken)
	resp, err := http.Get(fileUrl)
	if err != nil {
		hideLoading(bot, chatID)
		replyError(bot, chatID, "❌ Gagal mengunduh file content.")
		return
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		hideLoading(bot, chatID)
		replyError(bot, chatID, "❌ Gagal membaca file.")
		return
	}

	zipReader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		hideLoading(bot, chatID)
		replyError(bot, chatID, "❌ File bukan format ZIP yang valid.")
		return
	}

	validFiles := map[string]bool{
		"config.json":     true,
		"users.json":      true,
		"bot-config.json": true,
		"domain":          true,
		"apikey":          true,
		"api_port":        true,
		"zivpn.crt":       true,
		"zivpn.key":       true,
	}

	restoredFiles := 0
	for _, f := range zipReader.File {
		if !validFiles[f.Name] {
			continue
		}

		rc, err := f.Open()
		if err != nil {
			continue
		}
		defer rc.Close()

		dstPath := filepath.Join("/etc/zivpn", f.Name)
		dst, err := os.Create(dstPath)
		if err != nil {
			continue
		}
		defer dst.Close()

		io.Copy(dst, rc)
		restoredFiles++
	}

	hideLoading(bot, chatID)

	exec.Command("systemctl", "restart", "zivpn").Run()
	exec.Command("systemctl", "restart", "zivpn-api").Run()

	msgSuccess := tgbotapi.NewMessage(chatID, fmt.Sprintf(`✅ RESTORE BERHASIL!
📊 Statistik:

File restored: %d
Service restarted: ZiVPN, API

🔄 Bot akan restart dalam 2 detik...
Terima kasih!`, restoredFiles))
	bot.Send(msgSuccess)
	go func() {
		time.Sleep(2 * time.Second)
		exec.Command("systemctl", "restart", "zivpn-bot").Run()
	}()
}

// ==========================================
// Admin & Notifications
// ==========================================

func isAdmin(config *BotConfig, userID int64) bool {
	if config == nil {
		return false
	}
	if userID == config.AdminID {
		return true
	}
	for _, adminID := range config.AdminIDs {
		if userID == adminID {
			return true
		}
	}
	return hasAdminRole(config, userID)
}

func isOwner(config *BotConfig, userID int64) bool {
	if config == nil {
		return false
	}
	if userID == config.AdminID {
		return true
	}
	if config.AdminRoles == nil {
		return false
	}
	role, ok := config.AdminRoles[strconv.FormatInt(userID, 10)]
	return ok && isOwnerRole(role)
}

func hasAdminRole(config *BotConfig, userID int64) bool {
	if config.AdminRoles == nil {
		return false
	}
	role, ok := config.AdminRoles[strconv.FormatInt(userID, 10)]
	return ok && isAdminRole(role)
}

func isAdminRole(role string) bool {
	role = strings.ToLower(strings.TrimSpace(role))
	switch role {
	case "", "admin", "owner", "superadmin":
		return true
	default:
		return false
	}
}

func isOwnerRole(role string) bool {
	role = strings.ToLower(strings.TrimSpace(role))
	switch role {
	case "owner", "superadmin":
		return true
	default:
		return false
	}
}

type BotNotification struct {
	Event   string   `json:"event"`
	Message string   `json:"message"`
	Users   []string `json:"users,omitempty"`
	Count   int      `json:"count,omitempty"`
	Date    string   `json:"date,omitempty"`
}

func startNotificationServer(bot *tgbotapi.BotAPI, config *BotConfig) {
	mux := http.NewServeMux()
	mux.HandleFunc("/notify", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("X-API-Key") != ApiKey {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		var payload BotNotification
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "Invalid payload", http.StatusBadRequest)
			return
		}

		message := payload.Message
		if message == "" {
			message = formatNotificationMessage(payload)
		}

		if message != "" {
			notifyAdmins(bot, config, message)
		}

		w.WriteHeader(http.StatusOK)
	})

	go func() {
		if err := http.ListenAndServe(BotNotifyAddr, mux); err != nil {
			log.Printf("Notification server stopped: %v", err)
		}
	}()
}

func formatNotificationMessage(payload BotNotification) string {
	switch payload.Event {
	case "expire":
		if payload.Count == 0 {
			return "⏰ Expire check selesai. Tidak ada akun yang direvoke."
		}
		if len(payload.Users) > 0 {
			return fmt.Sprintf("⏰ Expire check selesai. %d akun direvoke:\n- %s", payload.Count, strings.Join(payload.Users, "\n- "))
		}
		return fmt.Sprintf("⏰ Expire check selesai. %d akun direvoke.", payload.Count)
	case "cleanup":
		if payload.Count == 0 {
			return "🧹 Cleanup selesai. Tidak ada akun expired yang dihapus."
		}
		if len(payload.Users) > 0 {
			return fmt.Sprintf("🧹 Cleanup selesai. %d akun dihapus:\n- %s", payload.Count, strings.Join(payload.Users, "\n- "))
		}
		return fmt.Sprintf("🧹 Cleanup selesai. %d akun dihapus.", payload.Count)
	default:
		return payload.Message
	}
}

func notifyAdmins(bot *tgbotapi.BotAPI, config *BotConfig, message string) {
	for _, adminID := range adminRecipients(config) {
		msg := tgbotapi.NewMessage(adminID, message)
		bot.Send(msg)
	}
}

func adminRecipients(config *BotConfig) []int64 {
	return mapToSortedIDs(adminIDSet(config))
}

func adminIDSet(config *BotConfig) map[int64]struct{} {
	unique := make(map[int64]struct{})
	if config == nil {
		return unique
	}
	if config.AdminID != 0 {
		unique[config.AdminID] = struct{}{}
	}
	for _, adminID := range config.AdminIDs {
		if adminID != 0 {
			unique[adminID] = struct{}{}
		}
	}
	for idStr, role := range config.AdminRoles {
		if !isAdminRole(role) {
			continue
		}
		if id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64); err == nil && id != 0 {
			unique[id] = struct{}{}
		}
	}
	return unique
}

func mapToSortedIDs(unique map[int64]struct{}) []int64 {
	ids := make([]int64, 0, len(unique))
	for id := range unique {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func parseAdminIDInput(bot *tgbotapi.BotAPI, chatID int64, text string) (int64, bool) {
	adminID, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if err != nil || adminID <= 0 {
		sendMessage(bot, chatID, "❌ Admin ID tidak valid. Masukkan angka Telegram ID.")
		return 0, false
	}
	return adminID, true
}

func addAdmin(config *BotConfig, adminID int64) error {
	if config == nil {
		return fmt.Errorf("Konfigurasi tidak tersedia.")
	}
	if isAdmin(config, adminID) {
		return fmt.Errorf("Admin sudah terdaftar.")
	}
	config.AdminIDs = append(config.AdminIDs, adminID)
	if config.AdminRoles == nil {
		config.AdminRoles = make(map[string]string)
	}
	config.AdminRoles[strconv.FormatInt(adminID, 10)] = "admin"
	config.AdminIDs = mapToSortedIDs(adminIDSet(config))
	return nil
}

func removeAdmin(config *BotConfig, adminID int64) error {
	if config == nil {
		return fmt.Errorf("Konfigurasi tidak tersedia.")
	}
	if adminID == config.AdminID {
		return fmt.Errorf("Tidak bisa menghapus admin utama.")
	}
	if !isAdmin(config, adminID) {
		return fmt.Errorf("Admin tidak ditemukan.")
	}
	filtered := make([]int64, 0, len(config.AdminIDs))
	for _, id := range config.AdminIDs {
		if id != adminID {
			filtered = append(filtered, id)
		}
	}
	config.AdminIDs = filtered
	if config.AdminRoles != nil {
		delete(config.AdminRoles, strconv.FormatInt(adminID, 10))
	}
	config.AdminIDs = mapToSortedIDs(adminIDSet(config))
	return nil
}

func resolveAdminRole(config *BotConfig, adminID int64) string {
	if adminID == config.AdminID {
		return "owner"
	}
	if config.AdminRoles != nil {
		if role, ok := config.AdminRoles[strconv.FormatInt(adminID, 10)]; ok && role != "" {
			return role
		}
	}
	return "admin"
}

func saveConfig(config *BotConfig) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(BotConfigFile, data, 0644)
}

func loadConfig() (BotConfig, error) {
	var config BotConfig
	file, err := ioutil.ReadFile(BotConfigFile)
	if err != nil {
		return config, err
	}
	err = json.Unmarshal(file, &config)
	normalizeAdminConfig(&config)

	if config.Domain == "" {
		if domainBytes, err := ioutil.ReadFile(DomainFile); err == nil {
			config.Domain = strings.TrimSpace(string(domainBytes))
		}
	}

	return config, err
}

func normalizeAdminConfig(config *BotConfig) {
	if config == nil {
		return
	}
	adminSet := adminIDSet(config)
	if len(adminSet) == 0 && config.AdminID != 0 {
		adminSet[config.AdminID] = struct{}{}
	}
	config.AdminIDs = mapToSortedIDs(adminSet)
}

func apiCall(method, endpoint string, payload interface{}) (map[string]interface{}, error) {
	var reqBody []byte
	var err error
	if payload != nil {
		reqBody, err = json.Marshal(payload)
		if err != nil {
			return nil, err
		}
	}

	client := &http.Client{}
	req, err := http.NewRequest(method, ApiUrl+endpoint, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", ApiKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := ioutil.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(body, &result)

	return result, nil
}

func getIpInfo() (IpInfo, error) {
	resp, err := http.Get("http://ip-api.com/json/")
	if err != nil {
		return IpInfo{}, err
	}
	defer resp.Body.Close()
	var info IpInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return IpInfo{}, err
	}
	return info, nil
}
