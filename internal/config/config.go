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
	TelegramToken     string
	TelegramAllowedID int64

	RouterBaseURL string
	RouterAPIKey  string
	AIModel       string

	SiapkerjaEmail    string
	SiapkerjaPassword string

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
		TelegramToken:     required("TELEGRAM_BOT_TOKEN"),
		RouterBaseURL:     strings.TrimRight(getenv("ROUTER_BASE_URL", "http://localhost:20128/v1"), "/"),
		RouterAPIKey:      os.Getenv("ROUTER_API_KEY"),
		AIModel:           getenv("AI_MODEL_NAME", "gpt-4o-mini"),
		SiapkerjaEmail:    os.Getenv("SIAPKERJA_EMAIL"),
		SiapkerjaPassword: os.Getenv("SIAPKERJA_PASSWORD"),
	}

	if raw := required("TELEGRAM_ALLOWED_USER_ID"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			errs = append(errs, fmt.Errorf("TELEGRAM_ALLOWED_USER_ID harus angka: %w", err))
		}
		cfg.TelegramAllowedID = id
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

func getenv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
