# ZiVPN UDP Tunnel

**ZiVPN UDP Tunnel** adalah solusi tunneling UDP premium dengan manajemen yang mudah, aman, dan otomatis. Dilengkapi dengan **API Server** dan **Telegram Bot** untuk pengelolaan user tanpa ribet.

---

## 🌟 Fitur Utama

*   **Minimalist CLI**: Installer dengan tampilan modern, bersih, dan elegan.
*   **Headless Management**: Manajemen user sepenuhnya via API atau Bot.
*   **Telegram Bot Integration**:
    *   **Free Bot**: Manajemen user (Create, Renew, Delete) dengan fitur **Backup & Restore**.
    *   **Paid Bot**: Integrasi Pakasir (QRIS) dengan **Admin Panel** tersembunyi.
*   **Robust User Management**:
    *   **Auto-Revoke**: User expired otomatis disconnect setiap jam 00:00 WIB (via Cron).
    *   **Clean Deletion**: Hapus user bersih total dari config dan database.
    *   **Bot Notification**: Admin menerima notifikasi saat expire check/cleanup sukses.
*   **IP Limit & Monitoring Online**: Batasi koneksi per akun dan lihat akun aktif beserta IP/last_seen.
*   **Dynamic Security**: API Key dan sertifikat SSL digenerate otomatis.
*   **Fail2ban Protection**: Proteksi brute-force untuk SSH dan ZiVPN UDP.
*   **Multi-Admin Bot**: Dukungan beberapa admin dengan role (`owner`/`admin`/`superadmin`).
*   **Template Paket Akun**: Paket siap pakai dengan limit IP/protocol.
*   **High Performance**: Core UDP ZiVPN yang dioptimalkan.

---

## 🧭 Changelog/Update Plan (v1.1–v1.4)

*   **v1.1** — **IP Limit & Template Paket**: Dukungan limit IP per akun serta paket akun berbasis template (`packages.json`).
*   **v1.2** — **Fail2ban**: Proteksi otomatis untuk SSH dan ZiVPN UDP.
*   **v1.3** — **Notifikasi & Multi-Admin**: Notifikasi bot saat cron sukses dan dukungan multi-admin dengan role.
*   **v1.4** — **Monitoring Online**: Endpoint monitoring akun aktif (IP & last_seen).

---

## 💳 Persiapan Payment Gateway (Pakasir)
Jika Anda ingin menggunakan **Paid Bot**, Anda wajib memiliki akun Pakasir.

1.  **Registrasi**: Daftar akun di [https://pakasir.com](https://pakasir.com).
2.  **Buat Proyek**: Buat proyek baru di dashboard Pakasir.
3.  **Ambil Kredensial**:
    *   **Project Slug**: ID unik proyek Anda.
    *   **API Key**: Kunci rahasia untuk akses API.
4.  **Saldo**: Pastikan akun Pakasir Anda aktif.

---

## 📥 Instalasi

Jalankan perintah berikut di terminal VPS Anda (sebagai root):

```bash
wget -q https://raw.githubusercontent.com/shwtrya/ZiVPNy/main/install.sh && chmod +x install.sh && ./install.sh
```

### Konfigurasi Saat Instalasi
Saat script berjalan, Anda akan diminta memasukkan:
1.  **Domain**: Wajib diisi untuk generate sertifikat SSL (contoh: `vpn.domain.com`).
2.  **API Key**: Tekan **Enter** untuk auto-generate.
3.  **Telegram Bot** (Opsional):
    *   **Bot Token**: Token dari @BotFather.
    *   **Admin ID**: ID Telegram Anda (cek di @userinfobot).
    *   **Bot Type**: Free atau Paid.
4.  **Torrent Blocker** (Opsional):
    *   Aktifkan untuk memblokir trafik torrent pada port umum dan string BitTorrent (iptables/ufw).
    *   Rule dapat diubah di `/etc/zivpn/torrent-block.rules`.

### Konfigurasi Tambahan Setelah Instalasi
*   **Torrent Blocker**: Enable/disable sesuai kebutuhan (lihat bagian di bawah) dan sesuaikan rule di `/etc/zivpn/torrent-block.rules`.
*   **Backup Full**: Jalankan menu **Backup** dari bot (Free/Paid) untuk mendapatkan ZIP backup penuh. Simpan file ZIP sebagai cadangan restore.
*   **Cron Cleanup**: Pastikan cron berjalan (jadwal default di bawah) dan cek log di `/var/log/zivpn-cron.log`. Untuk uji manual, gunakan endpoint `/api/cron/cleanup`.

### Enable/Disable Torrent Blocker
*   **Enable**: Jalankan `/etc/zivpn/torrent-block-apply.sh`
*   **Disable**: Jalankan `/etc/zivpn/torrent-block-remove.sh`
*   **Custom rule**: Edit `/etc/zivpn/torrent-block.rules`, lalu jalankan ulang apply script.

### Fail2ban (SSH + ZiVPN UDP)
Installer akan memasang **fail2ban** dan membuat jail default untuk:
*   **SSH (sshd)** dengan backend systemd.
*   **ZiVPN UDP** (port `5667/udp`) memakai filter custom `zivpn` yang membaca `/var/log/zivpn.log` dan journal `zivpn.service`.

**File konfigurasi:**
*   Jail: `/etc/fail2ban/jail.d/zivpn.local`
*   Filter: `/etc/fail2ban/filter.d/zivpn.conf`

**Perintah umum:**
*   **Enable jail ZiVPN**: `fail2ban-client start zivpn-udp`
*   **Disable jail ZiVPN**: `fail2ban-client stop zivpn-udp`
*   **Cek status**: `fail2ban-client status zivpn-udp`

Jika format log ZiVPN berbeda, edit `failregex` di filter `zivpn.conf` lalu restart fail2ban:
`systemctl restart fail2ban`.

---

## 🤖 Telegram Bot Usage

### Free Bot
*   **Public User**: Hanya bisa akses menu **Create**, **Renew**, **Delete**.
*   **Admin**: Akses penuh termasuk **List Users**, **System Info**, dan **Backup & Restore**.

### Paid Bot (Pakasir)
*   **Public User**: Hanya bisa membeli akun (Create) dan Cek Info.
*   **Admin**: Memiliki menu rahasia **🛠️ Admin Panel** yang berisi fitur manajemen dan **Backup & Restore**.

### Format bot-config.json (Multi Admin)
Bot kini mendukung multi-admin melalui **admin_ids** dan/atau **admin_roles**.

```json
{
  "bot_token": "TOKEN_BOT",
  "admin_id": 123456789,
  "admin_ids": [123456789, 987654321],
  "admin_roles": {
    "123456789": "owner",
    "987654321": "admin"
  },
  "mode": "public",
  "domain": "vpn.domain.com"
}
```

Catatan:
* `admin_id` tetap didukung untuk kompatibilitas lama.
* `admin_ids` berisi daftar ID admin tambahan.
* `admin_roles` adalah map ID → role (`owner`/`admin`/`superadmin`). Role lain dianggap non-admin.

### Fitur Backup & Restore
*   **Backup**: Bot mengirim file ZIP berisi semua data server (backup full).
    *   [ ] `/etc/zivpn/config.json`
    *   [ ] `/etc/zivpn/users.json`
    *   [ ] `/etc/zivpn/domain`
    *   [ ] `/etc/zivpn/apikey`
    *   [ ] `/etc/zivpn/api_port`
    *   [ ] `/etc/zivpn/zivpn.crt`
    *   [ ] `/etc/zivpn/zivpn.key`
    *   [ ] `/etc/zivpn/bot-config.json`
*   **Restore**: Kirim file ZIP backup ke bot untuk restore data dan restart server otomatis.

---

## 📦 Paket Akun (Template)

Bot dan API dapat menggunakan template paket dari `/etc/zivpn/packages.json`. Contoh format:

```json
[
  {
    "id": "basic",
    "name": "Basic 7 Hari",
    "days": 7,
    "ip_limit": 1,
    "protocols": ["udp"]
  },
  {
    "id": "pro",
    "name": "Pro 30 Hari",
    "days": 30,
    "ip_limit": 2,
    "protocols": ["udp", "ws", "ssl"]
  }
]
```

---

## ⏱️ Jadwal Cron

Cron job otomatis dijalankan di server untuk maintenance akun:

*   **Expire Check**: Setiap hari **00:00 WIB** → `/api/cron/expire`
*   **Cleanup Expired**: Setiap hari **00:10 WIB** → `/api/cron/cleanup`

Log cron tersimpan di `/var/log/zivpn-cron.log`.

---

## 📱 ZiVPN Manager App

Kelola server dan user Anda dengan mudah menggunakan aplikasi Android resmi **ZiVPN Manager**.

[**Download ZiVPN Manager (APK)**](https://github.com/shwtrya/ZiVPNy/raw/main/App/app-release.apk)

### Screenshots
<p float="left">
  <img src="https://github.com/shwtrya/ZiVPNy/raw/main/App/photo_2025-12-18_20-25-53.jpg" width="200" />
  <img src="https://github.com/shwtrya/ZiVPNy/raw/main/App/photo_2025-12-18_20-26-05.jpg" width="200" />
  <img src="https://github.com/shwtrya/ZiVPNy/raw/main/App/photo_2025-12-18_20-26-11.jpg" width="200" />
  <img src="https://github.com/shwtrya/ZiVPNy/raw/main/App/photo_2025-12-18_20-26-15.jpg" width="200" />
  <img src="https://github.com/shwtrya/ZiVPNy/raw/main/App/photo_2025-12-18_20-26-21.jpg" width="200" />
</p>

---

## 🔌 API Documentation

API berjalan di port `8080`. Gunakan **API Key** pada header `X-API-Key`.

**Base URL**: `http://<IP-VPS>:8080`
**Header**: `X-API-Key: <YOUR-API-KEY>`

### 1. Create User
*   **Endpoint**: `/api/user/create`
*   **Method**: `POST`
*   **Body**:
    *   Manual: `{ "password": "user1", "days": 30, "ip_limit": 1, "protocols": ["udp"] }`
    *   Paket: `{ "password": "user1", "package_id": "basic" }`

### 2. Delete User
*   **Endpoint**: `/api/user/delete`
*   **Method**: `POST`
*   **Body**: `{ "password": "user1" }`

### 3. Renew User
*   **Endpoint**: `/api/user/renew`
*   **Method**: `POST`
*   **Body**: `{ "password": "user1", "days": 30 }`

### 4. List Users
*   **Endpoint**: `/api/users`
*   **Method**: `GET`

### 5. System Info
*   **Endpoint**: `/api/info`
*   **Method**: `GET`

### 6. Online Accounts (Monitoring)
*   **Endpoint**: `/api/online`
*   **Method**: `GET`
*   **Desc**: Menampilkan akun aktif beserta IP dan `last_seen` (berdasarkan log server ZiVPN dan/atau conntrack).

### 7. Cron Trigger (Expire Check)
*   **Endpoint**: `/api/cron/expire`
*   **Method**: `POST`
*   **Desc**: Trigger manual pengecekan expired (biasanya jalan otomatis jam 00:00 WIB).

### 8. Cron Trigger (Cleanup Expired)
*   **Endpoint**: `/api/cron/cleanup`
*   **Method**: `POST`
*   **Desc**: Hapus akun expired dari config dan database (biasanya jalan otomatis jam 00:10 WIB).

---

## 🚀 Postman Collection
Anda dapat mengimpor koleksi API lengkap ke Postman menggunakan file JSON berikut:
[Download zivpn_postman_collection.json](zivpn_postman_collection.json)

---

## �🛠️ Pemecahan Masalah (Troubleshooting)

### 1. Log "TCP error" di Jurnal
Jika Anda melihat log seperti:
`ERROR TCP error {"addr": "140.213.xx.xx:..."}`

*   **Penyebab**: Koneksi client tidak stabil (sering terjadi pada jaringan seluler/Indosat) atau masalah MTU.
*   **Solusi**:
    *   Ini biasanya **bukan error server**. Jika user masih bisa connect, abaikan saja.
    *   Jika user sering disconnect, sarankan user menurunkan **MTU** di aplikasi client mereka (coba `1100` atau `1200`).

### 2. Bot Telegram Tidak Merespon
*   Pastikan service berjalan: `systemctl status zivpn-bot`
*   Cek log error: `journalctl -u zivpn-bot -f`
*   Pastikan **Bot Token** dan **Admin ID** benar di `/etc/zivpn/bot-config.json`.
*   Restart bot: `systemctl restart zivpn-bot`

### 3. API Error "Unauthorized"
*   Pastikan Anda menggunakan **API Key** yang benar di header `X-API-Key`.
*   Cek key yang aktif di server: `cat /etc/zivpn/apikey`

### 4. Service Gagal Start
*   Cek status: `systemctl status zivpn`
*   Pastikan port `5667` (UDP) dan `8080` (TCP) tidak terpakai aplikasi lain.
*   Cek config: `cat /etc/zivpn/config.json`

---

## 🗑️ Uninstall

Untuk menghapus ZiVPN, API, Bot, dan semua konfigurasi:

```bash
wget -q https://raw.githubusercontent.com/shwtrya/ZiVPNy/main/uninstall.sh && chmod +x uninstall.sh && ./uninstall.sh
```
