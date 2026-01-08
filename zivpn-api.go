package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	ConfigFile  = "/etc/zivpn/config.json"
	UserDB      = "/etc/zivpn/users.json"
	DomainFile  = "/etc/zivpn/domain"
	ApiKeyFile  = "/etc/zivpn/apikey"
	Port        = "/etc/zivpn/api_port"
	ProtocolDir = "/etc/zivpn/protocols"
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
}

type UserStore struct {
	Username        string                       `json:"username"`
	Password        string                       `json:"password"`
	Expired         string                       `json:"expired"`
	Status          string                       `json:"status"`
	Protocols       []string                     `json:"protocols"`
	ProtocolOptions map[string]map[string]string `json:"protocol_options,omitempty"`
}

type Response struct {
	Success bool        `json:"success"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
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

	if password == "" || req.Days <= 0 {
		jsonResponse(w, http.StatusBadRequest, false, "Password dan days harus valid", nil)
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

	found := false
	newUsers := []UserStore{}
	var newExpDate string
	var updatedProtocols []string

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

			updatedProtocols = normalized.Protocols
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
		})
	}

	jsonResponse(w, http.StatusOK, true, "Daftar user", userList)
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

	info := map[string]string{
		"domain":     domain,
		"public_ip":  strings.TrimSpace(string(ipPub)),
		"private_ip": strings.Fields(string(ipPriv))[0],
		"port":       "5667",
		"service":    "zivpn",
	}

	jsonResponse(w, http.StatusOK, true, "System Info", info)
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

func restartService() error {
	cmd := exec.Command("systemctl", "restart", "zivpn.service")
	return cmd.Run()
}
