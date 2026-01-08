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
	BotConfigFile = "/etc/zivpn/bot-config.json"
	ApiPortFile   = "/etc/zivpn/api_port"
	ApiKeyFile    = "/etc/zivpn/apikey"
	DomainFile    = "/etc/zivpn/domain"
	PortFile      = "/etc/zivpn/port"
)

var ApiUrl = "http://127.0.0.1:" + PortFile + "/api"

var ApiKey = "AutoFtBot-agskjgdvsbdreiWG1234512SDKrqw"

type BotConfig struct {
	BotToken      string `json:"bot_token"`
	AdminID       int64  `json:"admin_id"`
	Mode          string `json:"mode"`
	Domain        string `json:"domain"`
	PakasirSlug   string `json:"pakasir_slug"`
	PakasirApiKey string `json:"pakasir_api_key"`
	DailyPrice    int    `json:"daily_price"`
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
var mutex = &sync.Mutex{}

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

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
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
	// In Paid Bot, everyone can access, but actions are restricted/paid
	// Admin still has full control

	if state, exists := userStates[msg.From.ID]; exists {
		handleState(bot, msg, state, config)
		return
	}

	// Handle Document Upload (Restore) - Admin Only
	if msg.Document != nil && msg.From.ID == config.AdminID {
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

	switch {
	case query.Data == "menu_create":
		startCreateUser(bot, chatID, userID)
	case query.Data == "menu_info":
		systemInfo(bot, chatID, config)
	case query.Data == "cancel":
		cancelOperation(bot, chatID, userID, config)
	case strings.HasPrefix(query.Data, "section_"):
		// Section headers are non-actionable.

	case query.Data == "menu_admin":
		if userID == config.AdminID {
			showBackupRestoreMenu(bot, chatID)
		}
	case query.Data == "menu_online":
		if userID == config.AdminID {
			listOnlineUsers(bot, chatID)
		}
	case query.Data == "menu_backup_action":
		if userID == config.AdminID {
			performBackup(bot, chatID)
		}
	case query.Data == "menu_restore_action":
		if userID == config.AdminID {
			startRestore(bot, chatID, userID)
		}
	}

	bot.Request(tgbotapi.NewCallback(query.ID, ""))
}

func handleState(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, state string, config *BotConfig) {
	userID := msg.From.ID
	text := strings.TrimSpace(msg.Text)
	chatID := msg.Chat.ID

	switch state {
	case "create_password":
		if !validatePassword(bot, chatID, text) {
			return
		}
		mutex.Lock()
		tempUserData[userID]["password"] = text
		mutex.Unlock()
		userStates[userID] = "create_protocols"
		sendMessage(bot, chatID, "🧩 Masukkan protokol (pisahkan koma): udp, ssh, dropbear, ws, ssl")

	case "create_protocols":
		protocols, ok := parseProtocolsInput(bot, chatID, text)
		if !ok {
			return
		}
		mutex.Lock()
		tempUserData[userID]["protocols"] = strings.Join(protocols, ",")
		mutex.Unlock()
		userStates[userID] = "create_ip_limit"
		sendMessage(bot, chatID, "📌 Masukkan Limit IP (1-2):")

	case "create_ip_limit":
		_, ok := validateNumber(bot, chatID, text, 1, 2, "Limit IP")
		if !ok {
			return
		}
		mutex.Lock()
		tempUserData[userID]["ip_limit"] = text
		mutex.Unlock()
		userStates[userID] = "create_days"
		sendMessage(bot, chatID, fmt.Sprintf("⏳ Masukkan Durasi (hari)\nHarga: Rp %d / hari:", config.DailyPrice))

	case "create_days":
		days, ok := validateNumber(bot, chatID, text, 1, 365, "Durasi")
		if !ok {
			return
		}
		mutex.Lock()
		tempUserData[userID]["days"] = text
		mutex.Unlock()

		// Process Payment
		processPayment(bot, chatID, userID, days, config)
	}
}

// ==========================================
// Feature Implementation
// ==========================================

func startCreateUser(bot *tgbotapi.BotAPI, chatID int64, userID int64) {
	userStates[userID] = "create_password"
	mutex.Lock()
	tempUserData[userID] = make(map[string]string)
	tempUserData[userID]["chat_id"] = strconv.FormatInt(chatID, 10)
	mutex.Unlock()
	sendMessage(bot, chatID, "👤 Masukkan Password Baru:")
}

func processPayment(bot *tgbotapi.BotAPI, chatID int64, userID int64, days int, config *BotConfig) {
	price := days * config.DailyPrice
	if price < 500 {
		sendMessage(bot, chatID, fmt.Sprintf("❌ Total harga Rp %d. Minimal transaksi adalah Rp 500.\nSilakan tambah durasi.", price))
		return
	}
	orderID := fmt.Sprintf("ZIVPN-%d-%d", userID, time.Now().Unix())

	// Call Pakasir API
	payment, err := createPakasirTransaction(config, orderID, price)
	if err != nil {
		replyError(bot, chatID, "Gagal membuat pembayaran: "+err.Error())
		resetState(userID)
		return
	}

	// Store Order ID for verification
	mutex.Lock()
	tempUserData[userID]["order_id"] = orderID
	tempUserData[userID]["price"] = strconv.Itoa(price)
	mutex.Unlock()

	// Generate QR Image URL
	qrUrl := fmt.Sprintf("https://api.qrserver.com/v1/create-qr-code/?size=300x300&data=%s", payment.PaymentNumber)

	msgText := fmt.Sprintf("💳 **TAGIHAN PEMBAYARAN**\n\n🧾 **Ringkasan**\n• Password: `%s`\n• Durasi  : %d Hari\n• Limit IP: %s\n• Total   : Rp %d\n\n⏱️ **Batas Waktu**\n• Expired : %s\n\n📌 Silakan scan QRIS di atas untuk membayar.\n🔄 Sistem akan mengecek pembayaran otomatis setiap menit.",
		tempUserData[userID]["password"], days, tempUserData[userID]["ip_limit"], price, payment.ExpiredAt)

	photo := tgbotapi.NewPhoto(chatID, tgbotapi.FileURL(qrUrl))
	photo.Caption = msgText
	photo.ParseMode = "Markdown"

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❌ Batal", "cancel"),
		),
	)
	photo.ReplyMarkup = keyboard

	deleteLastMessage(bot, chatID)
	sentMsg, err := bot.Send(photo)
	if err == nil {
		lastMessageIDs[chatID] = sentMsg.MessageID
	}

	// Clear state but keep tempUserData for verification
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
					// Payment Success
					password := data["password"]
					days, _ := strconv.Atoi(data["days"])

					ipLimit, _ := strconv.Atoi(data["ip_limit"])
					createUser(bot, chatID, password, days, data["protocols"], ipLimit, config)
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

func createUser(bot *tgbotapi.BotAPI, chatID int64, password string, days int, protocols string, ipLimit int, config *BotConfig) {
	payload := map[string]interface{}{
		"password": password,
		"days":     days,
		"ip_limit": ipLimit,
	}
	if protocols != "" {
		payload["protocols"] = strings.Split(protocols, ",")
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
		replyError(bot, chatID, fmt.Sprintf("Gagal membuat akun: %s", res["message"]))
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
	url := fmt.Sprintf("https://app.pakasir.com/api/transactioncreate/qris")
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
// UI & Helpers (Simplified for Paid Bot)
// ==========================================

func showMainMenu(bot *tgbotapi.BotAPI, chatID int64, config *BotConfig) {
	ipInfo, _ := getIpInfo()
	domain := config.Domain
	if domain == "" {
		domain = "(Not Configured)"
	}

	msgText := fmt.Sprintf("```\n✨ ZiVPN UDP Store v1.0\n━━━━━━━━━━━━━━━━━━━━━\n📊 STATUS RINGKAS\n• Status : ✅ Aktif\n• Domain : %s\n• City   : %s\n• ISP    : %s\n• Harga  : Rp %d / Hari\n━━━━━━━━━━━━━━━━━━━━━\n```\n👇 Silakan pilih menu di bawah ini:", domain, ipInfo.City, ipInfo.Isp, config.DailyPrice)

	msg := tgbotapi.NewMessage(chatID, msgText)
	msg.ParseMode = "Markdown"

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📂 AKUN", "section_akun"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🛒 Beli Akun Premium", "menu_create"),
		),
	)

	// Add Admin Panel for Admin
	if chatID == config.AdminID {
		keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🖥️ SISTEM", "section_sistem"),
		))
		keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📊 System Info", "menu_info"),
		))
		keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🛠️ ADMIN", "section_admin"),
		))
		keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🛠️ Admin Panel", "menu_admin"),
		))
	}

	msg.ReplyMarkup = keyboard
	sendAndTrack(bot, msg)
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
	msg := fmt.Sprintf("```\n✅ PREMIUM ACCOUNT\n━━━━━━━━━━━━━━━━━━━━━\n🔐 AKUN\n• Password : %s\n• Expired  : %s\n• IP Limit : %s\n🧩 PROTOKOL\n• Protocols: %s\n🌐 SERVER\n• Domain   : %s\n• City     : %s\n• ISP      : %s\n━━━━━━━━━━━━━━━━━━━━━\n```\nTerima kasih telah berlangganan!",
		data["password"], data["expired"], ipLimit, protocolInfo, domain, ipInfo.City, ipInfo.Isp,
	)

	reply := tgbotapi.NewMessage(chatID, msg)
	reply.ParseMode = "Markdown"
	deleteLastMessage(bot, chatID)
	bot.Send(reply)
	showMainMenu(bot, chatID, config)
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

func cancelOperation(bot *tgbotapi.BotAPI, chatID int64, userID int64, config *BotConfig) {
	resetState(userID)
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
	// Don't delete tempUserData immediately if pending payment, but here we do for cancel
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

func validateNumber(bot *tgbotapi.BotAPI, chatID int64, text string, min, max int, fieldName string) (int, bool) {
	val, err := strconv.Atoi(text)
	if err != nil || val < min || val > max {
		sendMessage(bot, chatID, fmt.Sprintf("❌ %s harus angka positif (%d-%d). Coba lagi:", fieldName, min, max))
		return 0, false
	}
	return val, true
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

func showBackupRestoreMenu(bot *tgbotapi.BotAPI, chatID int64) {
	msg := tgbotapi.NewMessage(chatID, "🛠️ *Admin Panel*\nSilakan pilih menu:")
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📡 Akun Online", "menu_online"),
		),
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

func performBackup(bot *tgbotapi.BotAPI, chatID int64) {
	sendMessage(bot, chatID, "⏳ Sedang membuat backup...")

	// Files to backup
	files := []string{
		"/etc/zivpn/config.json",
		"/etc/zivpn/users.json",
		"/etc/zivpn/domain",
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
	doc.Caption = "✅ Backup Data ZiVPN"

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

func loadConfig() (BotConfig, error) {
	var config BotConfig
	file, err := ioutil.ReadFile(BotConfigFile)
	if err != nil {
		return config, err
	}
	err = json.Unmarshal(file, &config)

	if config.Domain == "" {
		if domainBytes, err := ioutil.ReadFile(DomainFile); err == nil {
			config.Domain = strings.TrimSpace(string(domainBytes))
		}
	}

	return config, err
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
