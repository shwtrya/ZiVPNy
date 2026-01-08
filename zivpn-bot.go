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
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// ==========================================
// Constants & Configuration
// ==========================================

const (
	BotConfigFile = "/etc/zivpn/bot-config.json"
	ApiPortFile   = "/etc/zivpn/api_port"
	ApiKeyFile    = "/etc/zivpn/apikey"
	DomainFile    = "/etc/zivpn/domain"
	PortFile      = "/etc/zivpn/port"
	PackagesFile  = "/etc/zivpn/packages.json"
	BotNotifyAddr = "127.0.0.1:9871"
)

var ApiUrl = "http://127.0.0.1:" + PortFile + "/api"

var ApiKey = "AutoFtBot-agskjgdvsbdreiWG1234512SDKrqw"

type BotConfig struct {
	BotToken   string            `json:"bot_token"`
	AdminID    int64             `json:"admin_id"`
	AdminIDs   []int64           `json:"admin_ids,omitempty"`
	AdminRoles map[string]string `json:"admin_roles,omitempty"`
	Mode       string            `json:"mode"`   // "public" or "private"
	Domain     string            `json:"domain"` // Domain from setup
}

type IpInfo struct {
	City  string `json:"city"`
	Isp   string `json:"isp"`
	Query string `json:"query"`
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

// ==========================================
// Main Entry Point
// ==========================================

func main() {
	// Load API Key
	if keyBytes, err := ioutil.ReadFile(ApiKeyFile); err == nil {
		ApiKey = strings.TrimSpace(string(keyBytes))
	}

	// Load API Port
	if portBytes, err := ioutil.ReadFile(ApiPortFile); err == nil {
		port := strings.TrimSpace(string(portBytes))
		ApiUrl = fmt.Sprintf("http://127.0.0.1:%s/api", port)
	}

	// Load Config
	config, err := loadConfig()
	if err != nil {
		log.Fatal("Gagal memuat konfigurasi bot:", err)
	}

	// Initialize Bot
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

	// Main Loop
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
	// Access Control
	if !isAllowed(config, msg.From.ID) {
		replyError(bot, msg.Chat.ID, "⛔ Akses Ditolak. Bot ini Private.")
		return
	}

	// Handle Document Upload (Restore)
	if msg.Document != nil && isAdmin(config, msg.From.ID) {
		if state, exists := userStates[msg.From.ID]; exists && state == "waiting_restore_file" {
			processRestoreFile(bot, msg, config)
			return
		}
	}

	// Handle State (User Input)
	if state, exists := userStates[msg.From.ID]; exists {
		handleState(bot, msg, state, config)
		return
	}

	// Handle Commands
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
	// Access Control (Special case for toggle_mode)
	if !isAllowed(config, query.From.ID) {
		if query.Data != "toggle_mode" || !isAdmin(config, query.From.ID) {
			bot.Request(tgbotapi.NewCallback(query.ID, "Akses Ditolak"))
			return
		}
	}

	chatID := query.Message.Chat.ID
	userID := query.From.ID

	switch {
	// --- Menu Navigation ---
	case query.Data == "menu_create":
		startCreateUser(bot, chatID, userID)
	case query.Data == "menu_delete":
		showUserSelection(bot, chatID, 1, "delete")
	case query.Data == "menu_renew":
		showUserSelection(bot, chatID, 1, "renew")
	case query.Data == "menu_list":
		if isAdmin(config, userID) {
			listUsers(bot, chatID)
		}
	case query.Data == "menu_online":
		if isAdmin(config, userID) {
			listOnlineUsers(bot, chatID)
		}
	case query.Data == "menu_cleanup":
		if isAdmin(config, userID) {
			cleanupExpiredUsers(bot, chatID, config)
		}
	case query.Data == "menu_info":
		if isAdmin(config, userID) {
			systemInfo(bot, chatID, config)
		}
	case query.Data == "menu_backup_restore":
		if isAdmin(config, userID) {
			showBackupRestoreMenu(bot, chatID)
		}
	case query.Data == "menu_backup_action":
		if isAdmin(config, userID) {
			performBackup(bot, chatID)
		}
	case query.Data == "menu_restore_action":
		if isAdmin(config, userID) {
			startRestore(bot, chatID, userID)
		}
	case query.Data == "menu_admins":
		if isAdmin(config, userID) {
			showAdminMenu(bot, chatID)
		}
	case query.Data == "menu_admin_add":
		if isAdmin(config, userID) {
			startAddAdmin(bot, chatID, userID)
		}
	case query.Data == "menu_admin_remove":
		if isAdmin(config, userID) {
			startRemoveAdmin(bot, chatID, userID)
		}
	case query.Data == "menu_admin_list":
		if isAdmin(config, userID) {
			listAdmins(bot, chatID, config)
		}
	case query.Data == "cancel":
		cancelOperation(bot, chatID, userID, config)
	case strings.HasPrefix(query.Data, "section_"):
		// Section headers are non-actionable.

	// --- Pagination ---
	case strings.HasPrefix(query.Data, "page_"):
		handlePagination(bot, chatID, query.Data)

	// --- Action Selection ---
	case strings.HasPrefix(query.Data, "select_renew:"):
		startRenewUser(bot, chatID, userID, query.Data)
	case strings.HasPrefix(query.Data, "select_delete:"):
		confirmDeleteUser(bot, chatID, query.Data)
	case strings.HasPrefix(query.Data, "select_package:"):
		selectPackageForCreate(bot, chatID, userID, query.Data)

	// --- Action Confirmation ---
	case strings.HasPrefix(query.Data, "confirm_delete:"):
		username := strings.TrimPrefix(query.Data, "confirm_delete:")
		deleteUser(bot, chatID, username, config)

	// --- Admin Actions ---
	case query.Data == "toggle_mode":
		toggleMode(bot, chatID, userID, config)
	}

	bot.Request(tgbotapi.NewCallback(query.ID, ""))
}

func handleState(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, state string, config *BotConfig) {
	userID := msg.From.ID
	text := strings.TrimSpace(msg.Text)
	chatID := msg.Chat.ID

	switch state {
	case "create_package":
		sendMessage(bot, chatID, "📦 Silakan pilih paket dari daftar di atas.")
		return
	case "create_username":
		if !validateUsername(bot, chatID, text) {
			return
		}
		tempUserData[userID]["username"] = text
		createUser(bot, chatID, tempUserData[userID]["username"], tempUserData[userID]["package_id"], config)
		resetState(userID)

	case "renew_protocols":
		if text == "" || strings.EqualFold(text, "-") {
			userStates[userID] = "renew_days"
			sendMessage(bot, chatID, "⏳ Masukkan Tambahan Durasi (hari):")
			return
		}
		protocols, ok := parseProtocolsInput(bot, chatID, text, false)
		if !ok {
			return
		}
		tempUserData[userID]["protocols"] = strings.Join(protocols, ",")
		userStates[userID] = "renew_days"
		sendMessage(bot, chatID, "⏳ Masukkan Tambahan Durasi (hari):")

	case "renew_days":
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
	}
}

// ==========================================
// Feature Implementation
// ==========================================

func startCreateUser(bot *tgbotapi.BotAPI, chatID int64, userID int64) {
	tempUserData[userID] = make(map[string]string)
	userStates[userID] = "create_package"
	showPackageMenu(bot, chatID, userID)
}

func showPackageMenu(bot *tgbotapi.BotAPI, chatID int64, userID int64) {
	packages, err := loadPackages()
	if err != nil {
		replyError(bot, chatID, "Gagal membaca paket: "+err.Error())
		return
	}
	if len(packages) == 0 {
		replyError(bot, chatID, "Paket belum tersedia. Hubungi admin.")
		return
	}

	lines := []string{"📦 *PILIH PAKET*"}
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, pkg := range packages {
		protocols := strings.Join(pkg.Protocols, ", ")
		lines = append(lines, fmt.Sprintf("• *%s* (`%s`) — %d hari, IP %d, %s", pkg.Name, pkg.ID, pkg.Days, pkg.IpLimit, protocols))
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("%s (%d hari)", pkg.Name, pkg.Days), "select_package:"+pkg.ID),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("❌ Batal", "cancel"),
	))

	msg := tgbotapi.NewMessage(chatID, strings.Join(lines, "\n"))
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	sendAndTrack(bot, msg)
}

func selectPackageForCreate(bot *tgbotapi.BotAPI, chatID int64, userID int64, data string) {
	packageID := strings.TrimPrefix(data, "select_package:")
	pkg, err := findPackageByID(packageID)
	if err != nil {
		replyError(bot, chatID, err.Error())
		return
	}
	tempUserData[userID]["package_id"] = pkg.ID
	tempUserData[userID]["package_name"] = pkg.Name
	userStates[userID] = "create_username"
	sendMessage(bot, chatID, fmt.Sprintf("👤 Masukkan Password untuk paket %s:", pkg.Name))
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

func cancelOperation(bot *tgbotapi.BotAPI, chatID int64, userID int64, config *BotConfig) {
	resetState(userID)
	showMainMenu(bot, chatID, config)
}

func handlePagination(bot *tgbotapi.BotAPI, chatID int64, data string) {
	parts := strings.Split(data, ":")
	action := parts[0][5:] // remove "page_"
	page, _ := strconv.Atoi(parts[1])
	showUserSelection(bot, chatID, page, action)
}

func toggleMode(bot *tgbotapi.BotAPI, chatID int64, userID int64, config *BotConfig) {
	if !isAdmin(config, userID) {
		return
	}
	if config.Mode == "public" {
		config.Mode = "private"
	} else {
		config.Mode = "public"
	}
	saveConfig(config)
	showMainMenu(bot, chatID, config)
}

func createUser(bot *tgbotapi.BotAPI, chatID int64, username string, packageID string, config *BotConfig) {
	payload := map[string]interface{}{
		"password":   username,
		"package_id": packageID,
	}

	res, err := apiCall("POST", "/user/create", payload)

	if err != nil {
		replyError(bot, chatID, "Error API: "+err.Error())
		return
	}

	if res["success"] == true {
		data := res["data"].(map[string]interface{})
		sendAccountInfo(bot, chatID, data, config)
	} else {
		replyError(bot, chatID, fmt.Sprintf("Gagal: %s", res["message"]))
		showMainMenu(bot, chatID, config)
	}
}

func renewUser(bot *tgbotapi.BotAPI, chatID int64, username string, days int, protocols string, config *BotConfig) {
	payload := map[string]interface{}{
		"password": username,
		"days":     days,
	}
	if protocols != "" {
		payload["protocols"] = strings.Split(protocols, ",")
	}

	res, err := apiCall("POST", "/user/renew", payload)

	if err != nil {
		replyError(bot, chatID, "Error API: "+err.Error())
		return
	}

	if res["success"] == true {
		data := res["data"].(map[string]interface{})
		// For renew, we might not have the limit handy, so passing 0 or fetching it would be ideal.
		// But for now, let's just display what we have.
		sendAccountInfo(bot, chatID, data, config)
	} else {
		replyError(bot, chatID, fmt.Sprintf("Gagal: %s", res["message"]))
		showMainMenu(bot, chatID, config)
	}
}

func deleteUser(bot *tgbotapi.BotAPI, chatID int64, username string, config *BotConfig) {
	res, err := apiCall("POST", "/user/delete", map[string]interface{}{
		"password": username,
	})

	if err != nil {
		replyError(bot, chatID, "Error API: "+err.Error())
		return
	}

	if res["success"] == true {
		msg := tgbotapi.NewMessage(chatID, "✅ Password berhasil dihapus.")
		deleteLastMessage(bot, chatID)
		bot.Send(msg)
		showMainMenu(bot, chatID, config)
	} else {
		replyError(bot, chatID, fmt.Sprintf("Gagal: %s", res["message"]))
		showMainMenu(bot, chatID, config)
	}
}

func listUsers(bot *tgbotapi.BotAPI, chatID int64) {
	res, err := apiCall("GET", "/users", nil)
	if err != nil {
		replyError(bot, chatID, "Error API: "+err.Error())
		return
	}

	if res["success"] == true {
		users := res["data"].([]interface{})
		if len(users) == 0 {
			sendMessage(bot, chatID, "📂 Tidak ada user.")
			return
		}

		msg := "📂 *DAFTAR AKUN*\n"
		for _, u := range users {
			user := u.(map[string]interface{})
			status := "🟢"
			if user["status"] == "Expired" {
				status = "🔴"
			}
			msg += fmt.Sprintf("\n%s `%s` (%s)", status, user["password"], user["expired"])
		}

		reply := tgbotapi.NewMessage(chatID, msg)
		reply.ParseMode = "Markdown"
		sendAndTrack(bot, reply)
	} else {
		replyError(bot, chatID, "Gagal mengambil data.")
	}
}

func listOnlineUsers(bot *tgbotapi.BotAPI, chatID int64) {
	res, err := apiCall("GET", "/online", nil)
	if err != nil {
		replyError(bot, chatID, "Error API: "+err.Error())
		return
	}

	if res["success"] == true {
		var entries []OnlineAccount
		dataBytes, _ := json.Marshal(res["data"])
		json.Unmarshal(dataBytes, &entries)
		if len(entries) == 0 {
			sendMessage(bot, chatID, "📡 Tidak ada akun online.")
			return
		}

		msg := "📡 *AKUN ONLINE*\n"
		for _, entry := range entries {
			name := entry.Username
			if name == "" {
				name = entry.Password
			}
			lastSeen := entry.LastSeen
			if parsed, err := time.Parse(time.RFC3339, entry.LastSeen); err == nil {
				lastSeen = parsed.Format("02-01-2006 15:04:05")
			}
			msg += fmt.Sprintf("\n• `%s` (%s) - %s", name, entry.IP, lastSeen)
		}

		reply := tgbotapi.NewMessage(chatID, msg)
		reply.ParseMode = "Markdown"
		sendAndTrack(bot, reply)
	} else {
		replyError(bot, chatID, "Gagal mengambil data online.")
	}
}

func systemInfo(bot *tgbotapi.BotAPI, chatID int64, config *BotConfig) {
	res, err := apiCall("GET", "/info", nil)
	if err != nil {
		replyError(bot, chatID, "Error API: "+err.Error())
		return
	}

	if res["success"] == true {
		data := res["data"].(map[string]interface{})
		ipInfo, _ := getIpInfo()
		domain := getInfoValue(data, "domain", config.Domain)

		msg := fmt.Sprintf("```\n🖥️ INFO ZIVPN UDP\n━━━━━━━━━━━━━━━━━━━━━\n🌐 JARINGAN\n• Domain    : %s\n• IP Public : %s\n• IP Private: %s\n• Port      : %s\n⚙️ SISTEM\n• Service   : %s\n• CPU       : %s\n• RAM       : %s\n• Disk      : %s\n• Uptime    : %s\n• Load Avg  : %s\n• Kernel    : %s\n• Version   : %s\n📍 LOKASI\n• City      : %s\n• ISP       : %s\n━━━━━━━━━━━━━━━━━━━━━\n```",
			domain,
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
		)

		reply := tgbotapi.NewMessage(chatID, msg)
		reply.ParseMode = "Markdown"
		deleteLastMessage(bot, chatID)
		bot.Send(reply)
		showMainMenu(bot, chatID, config)
	} else {
		replyError(bot, chatID, "Gagal mengambil info.")
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

func cleanupExpiredUsers(bot *tgbotapi.BotAPI, chatID int64, config *BotConfig) {
	sendMessage(bot, chatID, "⏳ Sedang membersihkan akun expired...")

	res, err := apiCall("POST", "/cron/cleanup", nil)
	if err != nil {
		replyError(bot, chatID, "Error API: "+err.Error())
		showMainMenu(bot, chatID, config)
		return
	}

	if res["success"] == true {
		data := res["data"]
		if data != nil {
			dataMap := data.(map[string]interface{})
			count := int(dataMap["deleted_count"].(float64))
			if count > 0 {
				deletedUsers := dataMap["deleted_users"].([]interface{})
				userList := ""
				for _, u := range deletedUsers {
					userList += fmt.Sprintf("\n• `%s`", u.(string))
				}
				msg := fmt.Sprintf("✅ Berhasil menghapus %d akun expired:%s", count, userList)
				reply := tgbotapi.NewMessage(chatID, msg)
				reply.ParseMode = "Markdown"
				deleteLastMessage(bot, chatID)
				bot.Send(reply)
			} else {
				sendMessage(bot, chatID, "✅ Tidak ada akun expired yang perlu dihapus.")
			}
		} else {
			sendMessage(bot, chatID, "✅ "+res["message"].(string))
		}
	} else {
		replyError(bot, chatID, fmt.Sprintf("Gagal: %s", res["message"]))
	}
	showMainMenu(bot, chatID, config)
}

func showBackupRestoreMenu(bot *tgbotapi.BotAPI, chatID int64) {
	msg := tgbotapi.NewMessage(chatID, "💾 *Backup & Restore*\nSilakan pilih menu:")
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬇️ Backup Data", "menu_backup_action"),
			tgbotapi.NewInlineKeyboardButtonData("⬆️ Restore Data", "menu_restore_action"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❌ Kembali", "cancel"),
		),
	)
	sendAndTrack(bot, msg)
}

func showAdminMenu(bot *tgbotapi.BotAPI, chatID int64) {
	msg := tgbotapi.NewMessage(chatID, "👥 *Kelola Admin*\nPilih aksi:")
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("➕ Tambah Admin", "menu_admin_add"),
			tgbotapi.NewInlineKeyboardButtonData("➖ Hapus Admin", "menu_admin_remove"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📋 Daftar Admin", "menu_admin_list"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❌ Kembali", "cancel"),
		),
	)
	sendAndTrack(bot, msg)
}

func startAddAdmin(bot *tgbotapi.BotAPI, chatID int64, userID int64) {
	userStates[userID] = "admin_add"
	sendMessage(bot, chatID, "Masukkan Admin ID Telegram yang ingin ditambahkan:")
}

func startRemoveAdmin(bot *tgbotapi.BotAPI, chatID int64, userID int64) {
	userStates[userID] = "admin_remove"
	sendMessage(bot, chatID, "Masukkan Admin ID Telegram yang ingin dihapus:")
}

func listAdmins(bot *tgbotapi.BotAPI, chatID int64, config *BotConfig) {
	admins := adminIDSet(config)
	if len(admins) == 0 {
		sendMessage(bot, chatID, "Belum ada admin terdaftar.")
		return
	}

	ids := make([]int64, 0, len(admins))
	for id := range admins {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	var builder strings.Builder
	builder.WriteString("📋 *Daftar Admin*\n")
	for _, id := range ids {
		role := resolveAdminRole(config, id)
		builder.WriteString(fmt.Sprintf("• `%d` (%s)\n", id, role))
	}

	msg := tgbotapi.NewMessage(chatID, builder.String())
	msg.ParseMode = "Markdown"
	sendAndTrack(bot, msg)
}

func performBackup(bot *tgbotapi.BotAPI, chatID int64) {
	sendMessage(bot, chatID, "⏳ Sedang membuat backup...")

	// Files to backup
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

	fileName := fmt.Sprintf("zivpn-backup-%s.zip", time.Now().Format("20060102-150405"))

	// Create a temporary file for the upload
	tmpFile := "/tmp/" + fileName
	if err := ioutil.WriteFile(tmpFile, buf.Bytes(), 0644); err != nil {
		replyError(bot, chatID, "Gagal membuat file backup.")
		return
	}
	defer os.Remove(tmpFile)

	doc := tgbotapi.NewDocument(chatID, tgbotapi.FilePath(tmpFile))
	doc.Caption = "✅ Backup Full Data ZiVPN"

	deleteLastMessage(bot, chatID)
	bot.Send(doc)
}

func startRestore(bot *tgbotapi.BotAPI, chatID int64, userID int64) {
	userStates[userID] = "waiting_restore_file"
	sendMessage(bot, chatID, "⬆️ *Restore Data*\n\nSilakan kirim file ZIP backup Anda sekarang.\n\n⚠️ PERINGATAN: Data saat ini akan ditimpa!")
}

func processRestoreFile(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, config *BotConfig) {
	chatID := msg.Chat.ID
	userID := msg.From.ID

	resetState(userID)
	sendMessage(bot, chatID, "⏳ Sedang memproses file...")

	// Download file
	fileID := msg.Document.FileID
	file, err := bot.GetFile(tgbotapi.FileConfig{FileID: fileID})
	if err != nil {
		replyError(bot, chatID, "Gagal mengunduh file.")
		return
	}

	fileUrl := file.Link(config.BotToken)
	resp, err := http.Get(fileUrl)
	if err != nil {
		replyError(bot, chatID, "Gagal mengunduh file content.")
		return
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		replyError(bot, chatID, "Gagal membaca file.")
		return
	}

	// Unzip
	zipReader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		replyError(bot, chatID, "File bukan format ZIP yang valid.")
		return
	}

	for _, f := range zipReader.File {
		// Security check: only allow specific files
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
	}

	// Restart Services
	exec.Command("systemctl", "restart", "zivpn").Run()
	exec.Command("systemctl", "restart", "zivpn-api").Run()

	msgSuccess := tgbotapi.NewMessage(chatID, "✅ Restore Berhasil!\nService ZiVPN, API, dan Bot telah direstart.")
	bot.Send(msgSuccess)

	// Restart Bot with delay to allow message sending
	go func() {
		time.Sleep(2 * time.Second)
		exec.Command("systemctl", "restart", "zivpn-bot").Run()
	}()

	showMainMenu(bot, chatID, config)
}

// ==========================================
// UI & Helpers
// ==========================================

func showMainMenu(bot *tgbotapi.BotAPI, chatID int64, config *BotConfig) {
	ipInfo, _ := getIpInfo()
	domain := config.Domain
	if domain == "" {
		domain = "(Not Configured)"
	}

	modeLabel := "Private"
	if config.Mode == "public" {
		modeLabel = "Public"
	}

	msgText := fmt.Sprintf("```\n✨ ZiVPN UDP Bot v1.0\n━━━━━━━━━━━━━━━━━━━━━\n📊 STATUS RINGKAS\n• Status : ✅ Aktif\n• Mode   : %s\n• Domain : %s\n• City   : %s\n• ISP    : %s\n━━━━━━━━━━━━━━━━━━━━━\n```\n👇 Silakan pilih menu di bawah ini:", modeLabel, domain, ipInfo.City, ipInfo.Isp)

	msg := tgbotapi.NewMessage(chatID, msgText)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = getMainMenuKeyboard(config, chatID)
	sendAndTrack(bot, msg)
}

func getMainMenuKeyboard(config *BotConfig, userID int64) tgbotapi.InlineKeyboardMarkup {
	// Public Menu (Everyone)
	rows := [][]tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📂 AKUN", "section_akun"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("👤 Create Password", "menu_create"),
			tgbotapi.NewInlineKeyboardButtonData("🗑️ Delete Password", "menu_delete"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 Renew Password", "menu_renew"),
		),
	}

	// Admin Menu (Admin Only)
	if isAdmin(config, userID) {
		modeLabel := "🔐 Mode: Private"
		if config.Mode == "public" {
			modeLabel = "🌍 Mode: Public"
		}

		rows[1] = append(rows[1], tgbotapi.NewInlineKeyboardButtonData("📋 List Passwords", "menu_list"))

		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🖥️ SISTEM", "section_sistem"),
		))
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📡 Akun Online", "menu_online"),
			tgbotapi.NewInlineKeyboardButtonData("📊 System Info", "menu_info"),
		))
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🛠️ ADMIN", "section_admin"),
		))
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("💾 Backup & Restore", "menu_backup_restore"),
			tgbotapi.NewInlineKeyboardButtonData("🧹 Cleanup Expired", "menu_cleanup"),
			tgbotapi.NewInlineKeyboardButtonData(modeLabel, "toggle_mode"),
		))
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("👥 Kelola Admin", "menu_admins"),
		))
	}

	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func sendAccountInfo(bot *tgbotapi.BotAPI, chatID int64, data map[string]interface{}, config *BotConfig) {
	ipInfo, _ := getIpInfo()
	domain := config.Domain
	if domain == "" {
		domain = "(Not Configured)"
	}

	protocolInfo := formatProtocols(data)
	ipLimit := "-"
	if value, ok := data["ip_limit"]; ok && value != nil {
		ipLimit = fmt.Sprintf("%v", value)
	}
	msg := fmt.Sprintf("```\n✅ AKUN ZIVPN UDP\n━━━━━━━━━━━━━━━━━━━━━\n🔐 AKUN\n• Password : %s\n• Expired  : %s\n• IP Limit : %s\n🧩 PROTOKOL\n• Protocols: %s\n🌐 SERVER\n• Domain   : %s\n• City     : %s\n• ISP      : %s\n• IP ISP   : %s\n━━━━━━━━━━━━━━━━━━━━━\n```",
		data["password"],
		data["expired"],
		ipLimit,
		protocolInfo,
		domain,
		ipInfo.City,
		ipInfo.Isp,
		ipInfo.Query,
	)

	reply := tgbotapi.NewMessage(chatID, msg)
	reply.ParseMode = "Markdown"
	deleteLastMessage(bot, chatID)
	bot.Send(reply)
	showMainMenu(bot, chatID, config)
}

func showUserSelection(bot *tgbotapi.BotAPI, chatID int64, page int, action string) {
	users, err := getUsers()
	if err != nil {
		replyError(bot, chatID, "Gagal mengambil data user.")
		return
	}

	if len(users) == 0 {
		sendMessage(bot, chatID, "📂 Tidak ada user.")
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
		label := fmt.Sprintf("%s (%s)", u.Password, u.Status)
		if u.Status == "Expired" {
			label = fmt.Sprintf("🔴 %s", label)
		} else {
			label = fmt.Sprintf("🟢 %s", label)
		}
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

	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("📋 Pilih User untuk %s (Halaman %d/%d):", strings.Title(action), page, totalPages))
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

func loadPackages() ([]Package, error) {
	data, err := os.ReadFile(PackagesFile)
	if err != nil {
		return nil, err
	}

	var wrapped struct {
		Packages []Package `json:"packages"`
	}
	if err := json.Unmarshal(data, &wrapped); err == nil && len(wrapped.Packages) > 0 {
		return wrapped.Packages, nil
	}

	var packages []Package
	if err := json.Unmarshal(data, &packages); err != nil {
		return nil, err
	}
	return packages, nil
}

func findPackageByID(packageID string) (Package, error) {
	packages, err := loadPackages()
	if err != nil {
		return Package{}, fmt.Errorf("Gagal membaca paket: %v", err)
	}
	for _, pkg := range packages {
		if strings.EqualFold(pkg.ID, packageID) {
			return pkg, nil
		}
	}
	return Package{}, fmt.Errorf("Paket tidak ditemukan")
}

// ==========================================
// Validation Helpers
// ==========================================

func validateUsername(bot *tgbotapi.BotAPI, chatID int64, text string) bool {
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

func validateNumber(bot *tgbotapi.BotAPI, chatID int64, text string, min, max int, fieldName string) (int, bool) {
	val, err := strconv.Atoi(text)
	if err != nil || val < min || val > max {
		sendMessage(bot, chatID, fmt.Sprintf("❌ %s harus angka positif (%d-%d). Coba lagi:", fieldName, min, max))
		return 0, false
	}
	return val, true
}

func parseProtocolsInput(bot *tgbotapi.BotAPI, chatID int64, text string, require bool) ([]string, bool) {
	supported := map[string]bool{
		"udp":      true,
		"ssh":      true,
		"dropbear": true,
		"ws":       true,
		"ssl":      true,
	}
	input := strings.TrimSpace(strings.ToLower(text))
	if input == "" {
		if require {
			sendMessage(bot, chatID, "❌ Protokol wajib diisi. Contoh: udp, ssh, ws")
			return nil, false
		}
		return nil, true
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
	if require && len(protocols) == 0 {
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

// ==========================================
// Configuration & Utils
// ==========================================

func isAllowed(config *BotConfig, userID int64) bool {
	return config.Mode == "public" || isAdmin(config, userID)
}

func isAdmin(config *BotConfig, userID int64) bool {
	if config == nil {
		return false
	}
	if hasAdminRole(config, userID) {
		return true
	}
	for _, adminID := range config.AdminIDs {
		if userID == adminID {
			return true
		}
	}
	return userID == config.AdminID
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

	// Jika domain kosong di config, coba baca dari file domain
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

// ==========================================
// Notification Server
// ==========================================

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

// ==========================================
// API Client
// ==========================================

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
