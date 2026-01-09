//go:build api
// +build api

package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	ConfigFile   = "/etc/zivpn/config.json"
	UserDB       = "/etc/zivpn/users.json"
	DomainFile   = "/etc/zivpn/domain"
	ApiKeyFile   = "/etc/zivpn/apikey"
	Port         = "/etc/zivpn/api_port"
	PortFile     = "/etc/zivpn/port"
	ProtocolDir  = "/etc/zivpn/protocols"
	PackagesFile = "/etc/zivpn/packages.json"
	LogFile      = "/var/log/zivpn.log"
	BotNotifyURL = "http://127.0.0.1:9871/notify"
	MaxAccounts  = 20
)

var AuthToken = "AutoFtBot-agskjgdvsbdreiWG1234512SDKrqw"

var supportedProtocols = map[string]bool{
	"udp":      true,
	"ssh":      true,
	"dropbear": true,
	"ws":       true,
	"ssl":      true,
}

var defaultProtocols = []string{"udp"}

type Config struct {
	Listen string `json:"listen"`
	Cert   string `json:"cert"`
	Key    string `json:"key"`
	Obfs   string `json:"obfs"`
	Auth   struct {
		Mode   string   `json:"mode"`
		Config []string `json:"config"`
	} `json:"auth"`
}

type UserRequest struct {
	Username        string                       `json:"username"`
	Password        string                       `json:"password"`
	Days            int                          `json:"days"`
	Protocols       []string                     `json:"protocols"`
	ProtocolOptions map[string]map[string]string `json:"protocol_options"`
	IpLimit         int                          `json:"ip_limit"`
	PackageID       string                       `json:"package_id"`
}

type UserStore struct {
	Username        string                       `json:"username"`
	Password        string                       `json:"password"`
	Expired         string                       `json:"expired"`
	Status          string                       `json:"status"`
	Protocols       []string                     `json:"protocols"`
	ProtocolOptions map[string]map[string]string `json:"protocol_options,omitempty"`
	IpLimit         int                          `json:"ip_limit"`
}

type Response struct {
	Success bool        `json:"success"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

type BotNotification struct {
	Event   string   `json:"event"`
	Message string   `json:"message,omitempty"`
	Users   []string `json:"users,omitempty"`
	Count   int      `json:"count,omitempty"`
	Date    string   `json:"date,omitempty"`
}

type Package struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Days      int      `json:"days"`
	IpLimit   int      `json:"ip_limit"`
	Protocols []string `json:"protocols"`
}

var mutex = &sync.Mutex{}

func main() {
	port := flag.Int("port", 8080, "Port to run the API server on")
	flag.Parse()

	if keyBytes, err := ioutil.ReadFile(ApiKeyFile); err == nil {
		AuthToken = strings.TrimSpace(string(keyBytes))
	}

	http.HandleFunc("/api/user/create", authMiddleware(createUser))
	http.HandleFunc("/api/user/delete", authMiddleware(deleteUser))
	http.HandleFunc("/api/user/renew", authMiddleware(renewUser))
	http.HandleFunc("/api/users", authMiddleware(listUsers))
	http.HandleFunc("/api/online", authMiddleware(listOnlineUsers))
	http.HandleFunc("/api/info", authMiddleware(getSystemInfo))
	http.HandleFunc("/api/cron/expire", authMiddleware(checkExpiration))
	http.HandleFunc("/api/cron/cleanup", authMiddleware(cleanupExpired))

	log.Printf("Server started at :%d", *port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", *port), nil))
}

func authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-API-Key")
		if token != AuthToken {
			jsonResponse(w, http.StatusUnauthorized, false, "Unauthorized", nil)
			return
		}
		next(w, r)
	}
}

func jsonResponse(w http.ResponseWriter, status int, success bool, message string, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(Response{
		Success: success,
		Message: message,
		Data:    data,
	})
}

func notifyBot(event string, users []string, count int, message string) {
	payload := BotNotification{
		Event:   event,
		Message: message,
		Users:   users,
		Count:   count,
		Date:    time.Now().Format("2006-01-02"),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Failed to marshal bot notification: %v", err)
		return
	}

	req, err := http.NewRequest(http.MethodPost, BotNotifyURL, strings.NewReader(string(body)))
	if err != nil {
		log.Printf("Failed to create bot notification request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", AuthToken)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Failed to send bot notification: %v", err)
		return
	}
	defer resp.Body.Close()
}

func createUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, false, "Method not allowed", nil)
		return
	}

	var req UserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, false, "Invalid request body", nil)
		return
	}

	username := strings.TrimSpace(req.Username)
	password := strings.TrimSpace(req.Password)
	if username == "" && password != "" {
		username = password
	}
	if password == "" && username != "" {
		password = username
	}

	packageID := strings.TrimSpace(req.PackageID)
	if packageID != "" {
		pkg, err := findPackageByID(packageID)
		if err != nil {
			jsonResponse(w, http.StatusBadRequest, false, err.Error(), nil)
			return
		}
		if req.Days <= 0 {
			req.Days = pkg.Days
		}
		if len(req.Protocols) == 0 {
			req.Protocols = pkg.Protocols
		}
		if req.IpLimit <= 0 {
			req.IpLimit = pkg.IpLimit
		}
	}

	if password == "" || req.Days <= 0 {
		jsonResponse(w, http.StatusBadRequest, false, "Password dan days harus valid", nil)
		return
	}
	if req.IpLimit == 0 {
		req.IpLimit = 1
	}
	if err := validateIpLimit(req.IpLimit); err != nil {
		jsonResponse(w, http.StatusBadRequest, false, err.Error(), nil)
		return
	}

	mutex.Lock()
	defer mutex.Unlock()

	config, err := loadConfig()
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, false, "Gagal membaca config", nil)
		return
	}

	for _, p := range config.Auth.Config {
		if p == password {
			jsonResponse(w, http.StatusConflict, false, "User sudah ada", nil)
			return
		}
	}

	expDate := time.Now().Add(time.Duration(req.Days) * 24 * time.Hour).Format("2006-01-02")

	users, err := loadUsers()
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, false, "Gagal membaca database user", nil)
		return
	}
	if len(users) >= MaxAccounts {
		jsonResponse(w, http.StatusConflict, false, "Stok akun habis", nil)
		return
	}

	for _, u := range users {
		if normalizeStoredUser(u).Password == password {
			jsonResponse(w, http.StatusConflict, false, "User sudah ada", nil)
			return
		}
	}

	protocols := normalizeProtocols(req.Protocols)
	if usesProtocol(protocols, "udp") {
		config.Auth.Config = append(config.Auth.Config, password)
		if err := saveConfig(config); err != nil {
			jsonResponse(w, http.StatusInternalServerError, false, "Gagal menyimpan config", nil)
			return
		}
	}

	newUser := UserStore{
		Username:        username,
		Password:        password,
		Expired:         expDate,
		Status:          "active",
		Protocols:       protocols,
		ProtocolOptions: req.ProtocolOptions,
		IpLimit:         req.IpLimit,
	}
	users = append(users, newUser)

	if err := saveUsers(users); err != nil {
		jsonResponse(w, http.StatusInternalServerError, false, "Gagal menyimpan database user", nil)
		return
	}

	if err := syncProtocolConfigs(users); err != nil {
		jsonResponse(w, http.StatusInternalServerError, false, "Gagal menyimpan konfigurasi protokol", nil)
		return
	}

	if err := restartService(); err != nil {
		jsonResponse(w, http.StatusInternalServerError, false, "Gagal merestart service", nil)
		return
	}

	domain := "Tidak diatur"
	if domainBytes, err := ioutil.ReadFile(DomainFile); err == nil {
		domain = strings.TrimSpace(string(domainBytes))
	}

	jsonResponse(w, http.StatusOK, true, "User berhasil dibuat", map[string]interface{}{
		"username":  username,
		"password":  password,
		"expired":   expDate,
		"domain":    domain,
		"protocols": protocols,
		"ip_limit":  req.IpLimit,
	})
}

func deleteUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, false, "Method not allowed", nil)
		return
	}

	var req UserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, false, "Invalid request body", nil)
		return
	}

	password := strings.TrimSpace(req.Password)
	if password == "" {
		password = strings.TrimSpace(req.Username)
	}

	mutex.Lock()
	defer mutex.Unlock()

	config, err := loadConfig()
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, false, "Gagal membaca config", nil)
		return
	}

	foundInConfig := false
	newConfigAuth := []string{}
	for _, p := range config.Auth.Config {
		if p == password {
			foundInConfig = true
		} else {
			newConfigAuth = append(newConfigAuth, p)
		}
	}

	if foundInConfig {
		config.Auth.Config = newConfigAuth
		if err := saveConfig(config); err != nil {
			jsonResponse(w, http.StatusInternalServerError, false, "Gagal menyimpan config", nil)
			return
		}
	}

	users, err := loadUsers()
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, false, "Gagal membaca database user", nil)
		return
	}

	foundInDB := false
	newUsers := []UserStore{}
	for _, u := range users {
		normalized := normalizeStoredUser(u)
		if normalized.Password == password {
			foundInDB = true
			continue
		}
		newUsers = append(newUsers, normalized)
	}

	if !foundInConfig && !foundInDB {
		jsonResponse(w, http.StatusNotFound, false, "User tidak ditemukan", nil)
		return
	}

	if foundInDB {
		if err := saveUsers(newUsers); err != nil {
			jsonResponse(w, http.StatusInternalServerError, false, "Gagal menyimpan database user", nil)
			return
		}
	}

	if err := syncProtocolConfigs(newUsers); err != nil {
		jsonResponse(w, http.StatusInternalServerError, false, "Gagal menyimpan konfigurasi protokol", nil)
		return
	}

	if foundInConfig {
		if err := restartService(); err != nil {
			jsonResponse(w, http.StatusInternalServerError, false, "Gagal merestart service", nil)
			return
		}
	}

	jsonResponse(w, http.StatusOK, true, "User berhasil dihapus", nil)
}

func renewUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, false, "Method not allowed", nil)
		return
	}

	var req UserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, false, "Invalid request body", nil)
		return
	}

	username := strings.TrimSpace(req.Username)
	password := strings.TrimSpace(req.Password)
	if username == "" && password != "" {
		username = password
	}
	if password == "" && username != "" {
		password = username
	}

	mutex.Lock()
	defer mutex.Unlock()

	users, err := loadUsers()
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, false, "Gagal membaca database user", nil)
		return
	}

	if req.IpLimit != 0 {
		if err := validateIpLimit(req.IpLimit); err != nil {
			jsonResponse(w, http.StatusBadRequest, false, err.Error(), nil)
			return
		}
	}

	found := false
	newUsers := []UserStore{}
	var newExpDate string
	var updatedProtocols []string
	var updatedIpLimit int

	for _, u := range users {
		normalized := normalizeStoredUser(u)
		if normalized.Password == password {
			found = true
			currentExp, err := time.Parse("2006-01-02", normalized.Expired)
			if err != nil {
				currentExp = time.Now()
			}

			if currentExp.Before(time.Now()) {
				currentExp = time.Now()
			}

			newExp := currentExp.Add(time.Duration(req.Days) * 24 * time.Hour)
			newExpDate = newExp.Format("2006-01-02")

			normalized.Expired = newExpDate

			if normalized.Status == "locked" {
				normalized.Status = "active"
				if usesProtocol(normalized.Protocols, "udp") {
					go enableUser(password)
				}
			}

			if len(req.Protocols) > 0 {
				normalized.Protocols = normalizeProtocols(req.Protocols)
			}
			if req.ProtocolOptions != nil {
				normalized.ProtocolOptions = req.ProtocolOptions
			}
			if req.IpLimit != 0 {
				normalized.IpLimit = req.IpLimit
			}

			updatedProtocols = normalized.Protocols
			updatedIpLimit = normalized.IpLimit
			newUsers = append(newUsers, normalized)
		} else {
			newUsers = append(newUsers, normalizeStoredUser(u))
		}
	}

	if !found {
		jsonResponse(w, http.StatusNotFound, false, "User tidak ditemukan di database", nil)
		return
	}

	config, err := loadConfig()
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, false, "Gagal membaca config", nil)
		return
	}

	if usesProtocol(updatedProtocols, "udp") {
		exists := false
		for _, p := range config.Auth.Config {
			if p == password {
				exists = true
				break
			}
		}
		if !exists {
			config.Auth.Config = append(config.Auth.Config, password)
		}
	} else {
		config.Auth.Config = removeFromList(config.Auth.Config, password)
	}

	if err := saveConfig(config); err != nil {
		jsonResponse(w, http.StatusInternalServerError, false, "Gagal menyimpan config", nil)
		return
	}

	if err := saveUsers(newUsers); err != nil {
		jsonResponse(w, http.StatusInternalServerError, false, "Gagal menyimpan database user", nil)
		return
	}

	if err := syncProtocolConfigs(newUsers); err != nil {
		jsonResponse(w, http.StatusInternalServerError, false, "Gagal menyimpan konfigurasi protokol", nil)
		return
	}

	if err := restartService(); err != nil {
		jsonResponse(w, http.StatusInternalServerError, false, "Gagal merestart service", nil)
		return
	}

	jsonResponse(w, http.StatusOK, true, "User berhasil diperpanjang", map[string]interface{}{
		"username":  username,
		"password":  password,
		"expired":   newExpDate,
		"protocols": updatedProtocols,
		"ip_limit":  updatedIpLimit,
	})
}

func listUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, false, "Method not allowed", nil)
		return
	}

	users, err := loadUsers()
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, false, "Gagal membaca database user", nil)
		return
	}

	type UserInfo struct {
		Username  string   `json:"username"`
		Password  string   `json:"password"`
		Expired   string   `json:"expired"`
		Status    string   `json:"status"`
		Protocols []string `json:"protocols"`
		IpLimit   int      `json:"ip_limit"`
	}

	userList := []UserInfo{}
	today := time.Now().Format("2006-01-02")

	for _, u := range users {
		normalized := normalizeStoredUser(u)
		status := "Active"
		if normalized.Status == "locked" {
			status = "Locked"
		} else if normalized.Expired < today {
			status = "Expired"
		}

		userList = append(userList, UserInfo{
			Username:  normalized.Username,
			Password:  normalized.Password,
			Expired:   normalized.Expired,
			Status:    status,
			Protocols: normalized.Protocols,
			IpLimit:   normalized.IpLimit,
		})
	}

	jsonResponse(w, http.StatusOK, true, "Daftar user", userList)
}

func listOnlineUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, false, "Method not allowed", nil)
		return
	}

	onlineUsers, err := collectOnlineUsers()
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, false, "Gagal membaca data online", nil)
		return
	}

	jsonResponse(w, http.StatusOK, true, "Daftar akun online", onlineUsers)
}

func getSystemInfo(w http.ResponseWriter, r *http.Request) {
	cmd := exec.Command("curl", "-s", "ifconfig.me")
	ipPub, _ := cmd.Output()

	cmd = exec.Command("hostname", "-I")
	ipPriv, _ := cmd.Output()

	domain := "Tidak diatur"
	if domainBytes, err := ioutil.ReadFile(DomainFile); err == nil {
		domain = strings.TrimSpace(string(domainBytes))
	}

	privateIP := ""
	if fields := strings.Fields(string(ipPriv)); len(fields) > 0 {
		privateIP = fields[0]
	}

	publicIP := strings.TrimSpace(string(ipPub))
	domainResolves := false
	domainIPs := []string{}
	if domain != "" && domain != "Tidak diatur" {
		if ips, err := net.LookupIP(domain); err == nil {
			seen := map[string]bool{}
			for _, ip := range ips {
				ipStr := ip.String()
				if !seen[ipStr] {
					seen[ipStr] = true
					domainIPs = append(domainIPs, ipStr)
				}
				if ipStr == publicIP {
					domainResolves = true
				}
			}
		}
	}

	usedAccounts := 0
	if users, err := loadUsers(); err == nil {
		usedAccounts = len(users)
	}

	info := map[string]interface{}{
		"domain":          domain,
		"public_ip":       publicIP,
		"private_ip":      privateIP,
		"domain_resolves": domainResolves,
		"domain_ips":      domainIPs,
		"port":            "5667",
		"service":         "zivpn",
		"cpu":             getCPUInfo(),
		"ram":             getRAMInfo(),
		"disk":            getDiskUsage(),
		"uptime":          getUptimeInfo(),
		"load_avg":        getLoadAverage(),
		"kernel":          getKernelVersion(),
		"zivpn_version":   getZiVPNVersion(),
		"max_accounts":    MaxAccounts,
		"used_accounts":   usedAccounts,
		"available_accounts": func() int {
			available := MaxAccounts - usedAccounts
			if available < 0 {
				return 0
			}
			return available
		}(),
	}

	jsonResponse(w, http.StatusOK, true, "System Info", info)
}

func getCPUInfo() string {
	data, err := ioutil.ReadFile("/proc/cpuinfo")
	if err != nil {
		return "Unknown"
	}

	model := ""
	cores := 0
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "model name") && model == "" {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				model = strings.TrimSpace(parts[1])
			}
		}
		if strings.HasPrefix(line, "processor") {
			cores++
		}
	}

	if model == "" {
		model = "Unknown"
	}
	if cores == 0 {
		return model
	}
	coreLabel := "cores"
	if cores == 1 {
		coreLabel = "core"
	}
	return fmt.Sprintf("%s (%d %s)", model, cores, coreLabel)
}

func getRAMInfo() string {
	data, err := ioutil.ReadFile("/proc/meminfo")
	if err != nil {
		return "Unknown"
	}

	var totalKB, availableKB int64
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "MemTotal:") {
			fmt.Sscanf(line, "MemTotal: %d kB", &totalKB)
		}
		if strings.HasPrefix(line, "MemAvailable:") {
			fmt.Sscanf(line, "MemAvailable: %d kB", &availableKB)
		}
	}

	if totalKB == 0 {
		return "Unknown"
	}
	usedKB := totalKB - availableKB
	if usedKB < 0 {
		usedKB = 0
	}
	return fmt.Sprintf("%s / %s", formatKB(usedKB), formatKB(totalKB))
}

func getDiskUsage() string {
	cmd := exec.Command("df", "-h", "/")
	output, err := cmd.Output()
	if err != nil {
		return "Unknown"
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) < 2 {
		return "Unknown"
	}
	fields := strings.Fields(lines[1])
	if len(fields) < 5 {
		return "Unknown"
	}
	size := fields[1]
	used := fields[2]
	percent := fields[4]
	return fmt.Sprintf("%s/%s (%s)", used, size, percent)
}

func getUptimeInfo() string {
	data, err := ioutil.ReadFile("/proc/uptime")
	if err != nil {
		return "Unknown"
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return "Unknown"
	}
	seconds, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return "Unknown"
	}
	return formatUptime(seconds)
}

func getLoadAverage() string {
	data, err := ioutil.ReadFile("/proc/loadavg")
	if err != nil {
		return "Unknown"
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return "Unknown"
	}
	return strings.Join(fields[:3], " ")
}

func getKernelVersion() string {
	cmd := exec.Command("uname", "-r")
	output, err := cmd.Output()
	if err != nil {
		return "Unknown"
	}
	return strings.TrimSpace(string(output))
}

func getZiVPNVersion() string {
	paths := []string{"/etc/zivpn/version", "/etc/zivpn/VERSION"}
	for _, path := range paths {
		if data, err := ioutil.ReadFile(path); err == nil {
			version := strings.TrimSpace(string(data))
			if version != "" {
				return version
			}
		}
	}
	return "Unknown"
}

func formatKB(valueKB int64) string {
	if valueKB >= 1024*1024 {
		return fmt.Sprintf("%.2f GB", float64(valueKB)/(1024*1024))
	}
	return fmt.Sprintf("%.0f MB", float64(valueKB)/1024)
}

func formatUptime(seconds float64) string {
	duration := time.Duration(seconds) * time.Second
	days := int(duration.Hours()) / 24
	hours := int(duration.Hours()) % 24
	minutes := int(duration.Minutes()) % 60

	parts := []string{}
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 || days > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	parts = append(parts, fmt.Sprintf("%dm", minutes))
	return strings.Join(parts, " ")
}

func checkExpiration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, false, "Method not allowed", nil)
		return
	}

	users, err := loadUsers()
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, false, "Gagal membaca database user", nil)
		return
	}

	today := time.Now().Format("2006-01-02")
	config, err := loadConfig()
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, false, "Gagal membaca config", nil)
		return
	}

	revokedCount := 0
	revokedUsers := []string{}
	updatedUsers := make([]UserStore, 0, len(users))
	for _, u := range users {
		normalized := normalizeStoredUser(u)
		if normalized.Expired < today && normalized.Status == "active" {
			log.Printf("User %s expired (Exp: %s). Revoking access.\n", normalized.Password, normalized.Expired)
			if usesProtocol(normalized.Protocols, "udp") {
				config.Auth.Config = removeFromList(config.Auth.Config, normalized.Password)
			}
			normalized.Status = "expired"
			revokedCount++
			revokedUsers = append(revokedUsers, normalized.Password)
		}
		updatedUsers = append(updatedUsers, normalized)
	}

	if err := saveUsers(updatedUsers); err != nil {
		jsonResponse(w, http.StatusInternalServerError, false, "Gagal menyimpan database user", nil)
		return
	}

	if err := saveConfig(config); err != nil {
		jsonResponse(w, http.StatusInternalServerError, false, "Gagal menyimpan config", nil)
		return
	}

	if err := syncProtocolConfigs(updatedUsers); err != nil {
		jsonResponse(w, http.StatusInternalServerError, false, "Gagal menyimpan konfigurasi protokol", nil)
		return
	}

	if revokedCount > 0 {
		restartService()
		notifyBot("expire", revokedUsers, revokedCount, "")
	}

	jsonResponse(w, http.StatusOK, true, fmt.Sprintf("Expiration check complete. Revoked: %d", revokedCount), nil)
}

// cleanupExpired menghapus semua akun expired dari config.json DAN users.json
func cleanupExpired(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, false, "Method not allowed", nil)
		return
	}

	mutex.Lock()
	defer mutex.Unlock()

	users, err := loadUsers()
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, false, "Gagal membaca database user", nil)
		return
	}

	config, err := loadConfig()
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, false, "Gagal membaca config", nil)
		return
	}

	today := time.Now().Format("2006-01-02")

	// Collect expired passwords
	expiredPasswords := make(map[string]bool)
	for _, u := range users {
		normalized := normalizeStoredUser(u)
		if normalized.Expired < today {
			expiredPasswords[normalized.Password] = true
		}
	}

	if len(expiredPasswords) == 0 {
		jsonResponse(w, http.StatusOK, true, "Tidak ada akun expired", nil)
		return
	}

	// Remove from users.json
	activeUsers := []UserStore{}
	for _, u := range users {
		normalized := normalizeStoredUser(u)
		if !expiredPasswords[normalized.Password] {
			activeUsers = append(activeUsers, normalized)
		}
	}

	// Remove from config.json
	activeConfig := []string{}
	for _, p := range config.Auth.Config {
		if !expiredPasswords[p] {
			activeConfig = append(activeConfig, p)
		}
	}
	config.Auth.Config = activeConfig

	// Save both
	if err := saveUsers(activeUsers); err != nil {
		jsonResponse(w, http.StatusInternalServerError, false, "Gagal menyimpan users.json", nil)
		return
	}

	if err := saveConfig(config); err != nil {
		jsonResponse(w, http.StatusInternalServerError, false, "Gagal menyimpan config.json", nil)
		return
	}

	if err := syncProtocolConfigs(activeUsers); err != nil {
		jsonResponse(w, http.StatusInternalServerError, false, "Gagal menyimpan konfigurasi protokol", nil)
		return
	}

	// Restart service
	restartService()

	deletedCount := len(expiredPasswords)
	deletedList := []string{}
	for p := range expiredPasswords {
		deletedList = append(deletedList, p)
	}

	notifyBot("cleanup", deletedList, deletedCount, "")

	jsonResponse(w, http.StatusOK, true, fmt.Sprintf("Berhasil menghapus %d akun expired", deletedCount), map[string]interface{}{
		"deleted_count": deletedCount,
		"deleted_users": deletedList,
	})
}

func revokeAccess(password string) {
	mutex.Lock()
	defer mutex.Unlock()

	config, err := loadConfig()
	if err == nil {
		newConfigAuth := []string{}
		changed := false
		for _, p := range config.Auth.Config {
			if p == password {
				changed = true
			} else {
				newConfigAuth = append(newConfigAuth, p)
			}
		}
		if changed {
			config.Auth.Config = newConfigAuth
			saveConfig(config)
			restartService()
		}
	}
}

func enableUser(password string) {
	mutex.Lock()
	defer mutex.Unlock()

	config, err := loadConfig()
	if err != nil {
		return
	}

	exists := false
	for _, p := range config.Auth.Config {
		if p == password {
			exists = true
			break
		}
	}

	if !exists {
		config.Auth.Config = append(config.Auth.Config, password)
		saveConfig(config)
		restartService()
	}
}

func normalizeStoredUser(user UserStore) UserStore {
	if user.Username == "" {
		user.Username = user.Password
	}
	if user.Password == "" {
		user.Password = user.Username
	}
	if len(user.Protocols) == 0 {
		user.Protocols = append([]string{}, defaultProtocols...)
	} else {
		user.Protocols = normalizeProtocols(user.Protocols)
	}
	if user.IpLimit == 0 {
		user.IpLimit = 1
	}
	return user
}

func normalizeProtocols(protocols []string) []string {
	unique := map[string]bool{}
	normalized := []string{}
	for _, p := range protocols {
		trimmed := strings.ToLower(strings.TrimSpace(p))
		if trimmed == "" {
			continue
		}
		if !supportedProtocols[trimmed] {
			continue
		}
		if !unique[trimmed] {
			unique[trimmed] = true
			normalized = append(normalized, trimmed)
		}
	}
	if len(normalized) == 0 {
		return append([]string{}, defaultProtocols...)
	}
	return normalized
}

func usesProtocol(protocols []string, target string) bool {
	for _, p := range protocols {
		if p == target {
			return true
		}
	}
	return false
}

func removeFromList(items []string, target string) []string {
	filtered := make([]string, 0, len(items))
	for _, item := range items {
		if item != target {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func syncProtocolConfigs(users []UserStore) error {
	if err := os.MkdirAll(ProtocolDir, 0755); err != nil {
		return err
	}

	type ProtocolUser struct {
		Username string            `json:"username"`
		Password string            `json:"password"`
		Expired  string            `json:"expired"`
		Status   string            `json:"status"`
		IpLimit  int               `json:"ip_limit"`
		Options  map[string]string `json:"options,omitempty"`
	}

	type ProtocolConfig struct {
		Protocol  string         `json:"protocol"`
		UpdatedAt string         `json:"updated_at"`
		Users     []ProtocolUser `json:"users"`
	}

	usersByProtocol := map[string][]ProtocolUser{}
	today := time.Now().Format("2006-01-02")

	for _, u := range users {
		normalized := normalizeStoredUser(u)
		if normalized.Status == "locked" || normalized.Expired < today {
			continue
		}
		for _, protocol := range normalized.Protocols {
			options := map[string]string{}
			if normalized.ProtocolOptions != nil {
				if opt, ok := normalized.ProtocolOptions[protocol]; ok {
					options = opt
				}
			}
			usersByProtocol[protocol] = append(usersByProtocol[protocol], ProtocolUser{
				Username: normalized.Username,
				Password: normalized.Password,
				Expired:  normalized.Expired,
				Status:   normalized.Status,
				IpLimit:  normalized.IpLimit,
				Options:  options,
			})
		}
	}

	for protocol := range supportedProtocols {
		config := ProtocolConfig{
			Protocol:  protocol,
			UpdatedAt: time.Now().Format(time.RFC3339),
			Users:     usersByProtocol[protocol],
		}
		data, err := json.MarshalIndent(config, "", "  ")
		if err != nil {
			return err
		}
		if err := ioutil.WriteFile(fmt.Sprintf("%s/%s.json", ProtocolDir, protocol), data, 0644); err != nil {
			return err
		}
	}
	return nil
}

func loadConfig() (Config, error) {
	var config Config
	file, err := ioutil.ReadFile(ConfigFile)
	if err != nil {
		return config, err
	}
	err = json.Unmarshal(file, &config)
	return config, err
}

func saveConfig(config Config) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(ConfigFile, data, 0644)
}

func loadUsers() ([]UserStore, error) {
	var users []UserStore
	file, err := ioutil.ReadFile(UserDB)
	if err != nil {
		if os.IsNotExist(err) {
			return users, nil
		}
		return nil, err
	}
	err = json.Unmarshal(file, &users)
	return users, err
}

func saveUsers(users []UserStore) error {
	data, err := json.MarshalIndent(users, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(UserDB, data, 0644)
}

func loadPackages() ([]Package, error) {
	data, err := ioutil.ReadFile(PackagesFile)
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
			if pkg.Days <= 0 {
				return Package{}, fmt.Errorf("Durasi paket tidak valid")
			}
			if err := validateIpLimit(pkg.IpLimit); err != nil {
				return Package{}, err
			}
			if len(pkg.Protocols) == 0 {
				return Package{}, fmt.Errorf("Protokol paket tidak valid")
			}
			return pkg, nil
		}
	}
	return Package{}, fmt.Errorf("Paket tidak ditemukan")
}

func restartService() error {
	cmd := exec.Command("systemctl", "restart", "zivpn.service")
	return cmd.Run()
}

func validateIpLimit(limit int) error {
	if limit < 1 || limit > 2 {
		return fmt.Errorf("IP limit harus antara 1-2")
	}
	return nil
}

type onlineLogEntry struct {
	User     string
	IP       string
	LastSeen time.Time
}

type OnlineAccount struct {
	Username string `json:"username"`
	Password string `json:"password"`
	IP       string `json:"ip"`
	LastSeen string `json:"last_seen"`
}

// Expected log format tokens include user/account and ip fields like "user=", "account=", "ip=".
var logUserRegexes = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\buser(?:name)?\s*[:=]\s*"?([a-zA-Z0-9._-]+)"?`),
	regexp.MustCompile(`(?i)\baccount\s*[:=]\s*"?([a-zA-Z0-9._-]+)"?`),
	regexp.MustCompile(`(?i)\bpass(?:word)?\s*[:=]\s*"?([a-zA-Z0-9._-]+)"?`),
	regexp.MustCompile(`(?i)\busr\s*[:=]\s*"?([a-zA-Z0-9._-]+)"?`),
}

// Expected log format tokens include IP fields like "ip=", "src=", "remote=", or "raddr=".
var logIPRegexes = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:ip|addr|remote|src|raddr|peer)\s*[:=]\s*(\d+\.\d+\.\d+\.\d+)`),
	regexp.MustCompile(`\bfrom\s+(\d+\.\d+\.\d+\.\d+)`),
	regexp.MustCompile(`\b(\d+\.\d+\.\d+\.\d+):\d+\b`),
}

var logTimeRegex = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}`)

func collectOnlineUsers() ([]OnlineAccount, error) {
	users, err := loadUsers()
	if err != nil {
		return nil, err
	}

	userByPassword := make(map[string]UserStore)
	userByUsername := make(map[string]UserStore)
	today := time.Now().Format("2006-01-02")

	for _, u := range users {
		normalized := normalizeStoredUser(u)
		if normalized.Status == "locked" || normalized.Expired < today {
			continue
		}
		userByPassword[normalized.Password] = normalized
		userByUsername[normalized.Username] = normalized
	}

	logEntries, err := readOnlineLogEntries()
	if err != nil {
		return nil, err
	}
	if len(logEntries) == 0 {
		return []OnlineAccount{}, nil
	}

	port := readServerPort()
	conntrackIPs, conntrackErr := loadConntrackIPs(port)
	conntrackAvailable := conntrackErr == nil
	cutoff := time.Now().Add(-10 * time.Minute)

	selected := make(map[string]onlineLogEntry)
	for _, entry := range logEntries {
		// If conntrack is available but empty, fall back to log-derived entries instead of filtering them out.
		if len(conntrackIPs) > 0 {
			if !conntrackIPs[entry.IP] {
				continue
			}
		} else if !conntrackAvailable && entry.LastSeen.Before(cutoff) {
			continue
		}

		key := entry.User + "|" + entry.IP
		if existing, ok := selected[key]; ok {
			if entry.LastSeen.After(existing.LastSeen) {
				selected[key] = entry
			}
		} else {
			selected[key] = entry
		}
	}

	online := make([]OnlineAccount, 0, len(selected))
	for _, entry := range selected {
		username := entry.User
		password := entry.User
		if user, ok := userByPassword[entry.User]; ok {
			username = user.Username
			password = user.Password
		} else if user, ok := userByUsername[entry.User]; ok {
			username = user.Username
			password = user.Password
		}
		online = append(online, OnlineAccount{
			Username: username,
			Password: password,
			IP:       entry.IP,
			LastSeen: entry.LastSeen.Format(time.RFC3339),
		})
	}

	sort.Slice(online, func(i, j int) bool {
		if online[i].Username == online[j].Username {
			return online[i].IP < online[j].IP
		}
		return online[i].Username < online[j].Username
	})

	return online, nil
}

func readOnlineLogEntries() ([]onlineLogEntry, error) {
	lines := []string{}
	logFiles := []string{LogFile, LogFile + ".1"}
	for _, path := range logFiles {
		fileLines, err := readLogFileLines(path)
		if err == nil && len(fileLines) > 0 {
			lines = append(lines, fileLines...)
		}
	}

	if len(lines) == 0 {
		output, err := exec.Command("journalctl", "-u", "zivpn", "-n", "2000", "--output=short-iso").Output()
		if err == nil {
			lines = strings.Split(string(output), "\n")
		}
	}

	if len(lines) == 0 {
		return []onlineLogEntry{}, nil
	}

	entries := make(map[string]onlineLogEntry)
	for _, line := range lines {
		user := extractLogValue(line, logUserRegexes)
		ip := extractLogValue(line, logIPRegexes)
		if user == "" || ip == "" {
			continue
		}
		lastSeen := parseLogTimestamp(line)
		if lastSeen.IsZero() {
			lastSeen = time.Now()
		}
		key := user + "|" + ip
		if existing, ok := entries[key]; ok {
			if lastSeen.After(existing.LastSeen) {
				entries[key] = onlineLogEntry{User: user, IP: ip, LastSeen: lastSeen}
			}
		} else {
			entries[key] = onlineLogEntry{User: user, IP: ip, LastSeen: lastSeen}
		}
	}

	collection := make([]onlineLogEntry, 0, len(entries))
	for _, entry := range entries {
		collection = append(collection, entry)
	}
	return collection, nil
}

func readLogFileLines(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	lines := []string{}
	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}

func extractLogValue(line string, patterns []*regexp.Regexp) string {
	for _, pattern := range patterns {
		if match := pattern.FindStringSubmatch(line); len(match) > 1 {
			return strings.TrimSpace(match[1])
		}
	}
	return ""
}

func parseLogTimestamp(line string) time.Time {
	match := logTimeRegex.FindString(line)
	if match == "" {
		return time.Time{}
	}
	parsed, err := time.Parse("2006-01-02 15:04:05", match)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func readServerPort() string {
	if portBytes, err := ioutil.ReadFile(PortFile); err == nil {
		port := strings.TrimSpace(string(portBytes))
		if port != "" {
			return port
		}
	}
	return "5667"
}

func loadConntrackIPs(port string) (map[string]bool, error) {
	ips := make(map[string]bool)
	if port == "" {
		port = "5667"
	}

	output, err := exec.Command("conntrack", "-L", "-p", "udp", "--dport", port).Output()
	if err != nil {
		return ips, err
	}

	re := regexp.MustCompile(`\bsrc=(\d+\.\d+\.\d+\.\d+)`)
	for _, match := range re.FindAllStringSubmatch(string(output), -1) {
		if len(match) > 1 {
			ips[match[1]] = true
		}
	}
	return ips, nil
}
