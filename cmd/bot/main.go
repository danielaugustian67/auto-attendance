package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"auto-attendance/internal/ai"
	"auto-attendance/internal/bot"
	"auto-attendance/internal/config"
	"auto-attendance/internal/monev"
	"auto-attendance/internal/scheduler"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("konfigurasi tidak valid:\n%v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	aiClient := ai.NewClient(cfg.RouterBaseURL, cfg.RouterAPIKey, cfg.AIModel)

	// TODO: ganti dengan implementasi API/browser setelah alur Monev dipetakan.
	var sub monev.Submitter = monev.Unimplemented{}

	b, err := bot.New(cfg.TelegramToken, cfg.TelegramAllowedID, cfg.Location, aiClient, sub)
	if err != nil {
		log.Fatalf("gagal membuat bot Telegram: %v", err)
	}

	if err := scheduler.Start(ctx, cfg.Location, cfg.ReminderTimes, b.Remind); err != nil {
		log.Fatalf("gagal memasang jadwal pengingat: %v", err)
	}

	log.Printf("bot berjalan; pengingat %v (%s)", cfg.ReminderTimes, cfg.Location)
	b.Run(ctx)
	log.Print("bot berhenti")
}
