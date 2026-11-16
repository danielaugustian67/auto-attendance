package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"golang.org/x/sync/errgroup"

	"auto-attendance/internal/ai"
	"auto-attendance/internal/bot"
	"auto-attendance/internal/config"
	"auto-attendance/internal/monev"
	"auto-attendance/internal/scheduler"
)

func main() {
	checkLogin := flag.Bool("cek-login", false, "hanya uji login SIAPkerja/Monev lalu simpan screenshot, tanpa mengirim apa pun")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("konfigurasi tidak valid:\n%v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	browser := &monev.Browser{
		Email:      cfg.SiapkerjaEmail,
		Password:   cfg.SiapkerjaPassword,
		ProfileDir: cfg.BrowserProfileDir,
		ExecPath:   cfg.ChromePath,
		Headless:   cfg.Headless,
		Location:   cfg.Location,
	}

	if *checkLogin {
		runCheckLogin(ctx, browser, filepath.Join(filepath.Dir(cfg.BrowserProfileDir), "cek-login.png"))
		return
	}

	if err := cfg.RequireChannel(); err != nil {
		log.Fatalf("konfigurasi tidak valid:\n%v", err)
	}

	aiClient := ai.NewClient(cfg.RouterBaseURL, cfg.RouterAPIKey, cfg.AIModel)

	var sub monev.Submitter = monev.Disabled{}
	if cfg.HasMonevCredentials() {
		sub = browser
		log.Print("monev: pengiriman otomatis aktif (browser headless)")
	} else {
		log.Print("monev: SIAPKERJA_EMAIL/PASSWORD kosong, bot hanya menyusun draf")
	}

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

// runCheckLogin menguji kredensial tanpa mengirim kehadiran. Jangan jalankan saat bot aktif,
// karena keduanya memakai profil browser yang sama.
func runCheckLogin(ctx context.Context, b *monev.Browser, out string) {
	log.Print("cek-login: membuka Monev...")
	shot, err := b.CheckLogin(ctx)
	if err != nil {
		log.Fatalf("cek-login gagal: %v", err)
	}
	if err := os.WriteFile(out, shot, 0o600); err != nil {
		log.Fatalf("cek-login berhasil, tapi gagal menyimpan screenshot: %v", err)
	}
	log.Printf("cek-login berhasil; screenshot halaman riwayat disimpan di %s", out)
}
