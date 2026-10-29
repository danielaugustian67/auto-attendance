package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sync/errgroup"

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

	flow := bot.NewFlow(aiClient, sub, cfg.Location)
	g, ctx := errgroup.WithContext(ctx)

	if cfg.TelegramToken != "" {
		t, err := bot.NewTelegram(cfg.TelegramToken, cfg.TelegramAllowedID, flow)
		if err != nil {
			log.Fatalf("gagal membuat bot Telegram: %v", err)
		}
		flow.AddReminderTarget(t)
		g.Go(func() error {
			t.Run(ctx)
			return nil
		})
		log.Print("telegram: aktif")
	}

	if cfg.WAEnabled {
		w, err := bot.NewWhatsApp(ctx, cfg.WASessionDB, cfg.WAAllowedNumber, cfg.WABotNumber, flow)
		if err != nil {
			log.Fatalf("gagal menyiapkan WhatsApp: %v", err)
		}
		flow.AddReminderTarget(w)
		g.Go(func() error { return w.Run(ctx) })
	}

	if err := scheduler.Start(ctx, cfg.Location, cfg.ReminderTimes, flow.Remind); err != nil {
		log.Fatalf("gagal memasang jadwal pengingat: %v", err)
	}
	log.Printf("bot berjalan; pengingat %v (%s)", cfg.ReminderTimes, cfg.Location)

	if err := g.Wait(); err != nil {
		log.Fatalf("bot berhenti karena error: %v", err)
	}
	log.Print("bot berhenti")
}
