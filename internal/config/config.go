// Package config memuat dan memvalidasi konfigurasi dari environment / file .env.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // embed database zona waktu agar Asia/Jakarta tersedia di VPS minimal

	"github.com/joho/godotenv"
)

type Config struct {
	// Telegram aktif bila TELEGRAM_BOT_TOKEN diisi.
	TelegramToken     string
	TelegramAllowedID int64

	// WhatsApp aktif bila WA_ENABLED=true. Nomor berformat internasional tanpa "+", misal 6281234567890.
	WAEnabled       bool
	WAAllowedNumber string
	WABotNumber     string // opsional: login dengan kode tautan alih-alih scan QR
	WASessionDB     string

	RouterBaseURL string
	RouterAPIKey  string
	AIModel       string

	// Kredensial SIAPkerja; bila kosong, bot hanya menyusun draf tanpa mengirim ke Monev.
	SiapkerjaEmail    string
	SiapkerjaPassword string
	Headless          bool
	ChromePath        string // opsional; kosong = cari Chrome/Chromium otomatis
	BrowserProfileDir string

	// ReminderTimes berformat "HH:MM", dievaluasi di zona Location.
	ReminderTimes []string
	Location      *time.Location
}

// Load membaca .env (jika ada) lalu environment variables.
func Load() (*Config, error) {
	_ = godotenv.Load() // .env opsional; di VPS bisa pakai env systemd

	var errs []error
	required := func(key string) string {
		v := strings.TrimSpace(os.Getenv(key))
		if v == "" {
			errs = append(errs, fmt.Errorf("%s wajib diisi", key))
		}
		return v
	}

	cfg := &Config{
		TelegramToken:     strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN")),
		WAEnabled:         strings.EqualFold(getenv("WA_ENABLED", "false"), "true"),
		WASessionDB:       getenv("WA_SESSION_DB", "storage_state/whatsapp.db"),
		RouterBaseURL:     strings.TrimRight(getenv("ROUTER_BASE_URL", "http://localhost:20128/v1"), "/"),
		RouterAPIKey:      os.Getenv("ROUTER_API_KEY"),
		AIModel:           getenv("AI_MODEL_NAME", "gpt-4o-mini"),
		SiapkerjaEmail:    strings.TrimSpace(os.Getenv("SIAPKERJA_EMAIL")),
		SiapkerjaPassword: os.Getenv("SIAPKERJA_PASSWORD"),
		Headless:          !strings.EqualFold(getenv("HEADLESS", "true"), "false"),
		ChromePath:        strings.TrimSpace(os.Getenv("CHROME_PATH")),
		BrowserProfileDir: getenv("BROWSER_PROFILE_DIR", "storage_state/chrome"),
	}

	if cfg.TelegramToken != "" {
		if raw := required("TELEGRAM_ALLOWED_USER_ID"); raw != "" {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				errs = append(errs, fmt.Errorf("TELEGRAM_ALLOWED_USER_ID harus angka: %w", err))
			}
			cfg.TelegramAllowedID = id
		}
	}

	if cfg.WAEnabled {
		cfg.WAAllowedNumber = NormalizePhone(required("WA_ALLOWED_NUMBER"))
		cfg.WABotNumber = NormalizePhone(os.Getenv("WA_BOT_NUMBER"))
	}

	loc, err := time.LoadLocation(getenv("TZ_LOCATION", "Asia/Jakarta"))
	if err != nil {
		errs = append(errs, fmt.Errorf("TZ_LOCATION tidak valid: %w", err))
	}
	cfg.Location = loc

	for _, t := range strings.Split(getenv("REMINDER_TIMES", "17:00,21:00"), ",") {
		t = strings.TrimSpace(t)
		if _, err := time.Parse("15:04", t); err != nil {
			errs = append(errs, fmt.Errorf("REMINDER_TIMES %q bukan format HH:MM", t))
			continue
		}
		cfg.ReminderTimes = append(cfg.ReminderTimes, t)
	}

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return cfg, nil
}

// RequireChannel memastikan minimal satu channel chat aktif (tidak dibutuhkan untuk -cek-login).
func (c *Config) RequireChannel() error {
	if c.TelegramToken == "" && !c.WAEnabled {
		return errors.New("aktifkan minimal satu channel: isi TELEGRAM_BOT_TOKEN dan/atau WA_ENABLED=true")
	}
	return nil
}

// HasMonevCredentials melaporkan apakah pengiriman otomatis ke Monev bisa diaktifkan.
func (c *Config) HasMonevCredentials() bool {
	return c.SiapkerjaEmail != "" && c.SiapkerjaPassword != ""
}

// NormalizePhone membuang karakter non-digit dan mengubah awalan 0 menjadi 62 (Indonesia).
func NormalizePhone(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	digits := b.String()
	if strings.HasPrefix(digits, "0") {
		digits = "62" + digits[1:]
	}
	return digits
}

func getenv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
