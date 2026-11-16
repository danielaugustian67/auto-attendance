package monev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/chromedp"

	"auto-attendance/internal/ai"
)

const (
	monevHost    = "monev.maganghub.kemnaker.go.id"
	accountHost  = "account.kemnaker.go.id"
	riwayatURL   = "https://" + monevHost + "/dashboard/riwayat"
	todayFormURL = riwayatURL + "?action=today" // membuka form kehadiran + laporan hari ini

	// Label form, diambil dari halaman login SIAPkerja dan bundle Monev.
	labelActivity = "Uraian aktivitas"
	labelLesson   = "Pembelajaran yang diperoleh"
	labelObstacle = "Kendala yang dialami"
	labelConfirm  = "Saya menyatakan telah meninjau dan memastikan isian laporan ini sudah benar"
	submitText    = "Simpan dan Kirim"
	successText   = "Kehadiran dan laporan harian berhasil dikirim."
	// Judul peringatan di halaman riwayat bila akun tidak boleh mengisi (misal status magang berakhir).
	disabledNotice = "Pengisian laporan dinonaktifkan"

	selLoginButton = `//button[@type="submit" and normalize-space(.)="Masuk"]`
	// Literal JS: kotak error komponen Login.vue SIAPkerja.
	loginErrorSelector = `"form .text-danger"`
)

// Browser mengirim logbook lewat Chromium headless (chromedp), hanya hidup selama submit.
//
// Izin lokasi selalu ditolak: bot tidak punya lokasi perangkat dan tidak boleh mengarang
// koordinat, jadi kehadiran terkirim tanpa koordinat.
type Browser struct {
	Email      string
	Password   string
	ProfileDir string // menyimpan cookie sesi SIAPkerja/Monev antar-run
	ExecPath   string // opsional; kosong = cari Chrome/Chromium otomatis
	Headless   bool
	Location   *time.Location
}

func (b *Browser) Submit(ctx context.Context, date time.Time, lb ai.Logbook) (Result, error) {
	today := time.Now().In(b.Location).Format("2006-01-02")
	if d := date.In(b.Location).Format("2006-01-02"); d != today {
		return Result{}, fmt.Errorf("draf ini untuk %s, sedangkan Monev hanya menerima kehadiran hari ini (%s); buat draf baru", d, today)
	}

	var res Result
	err := b.session(ctx, func(ctx context.Context) error {
		if err := b.openTodayForm(ctx); err != nil {
			return err
		}
		var err error
		res, err = fillAndSubmit(ctx, lb)
		return err
	})
	return res, err
}

// fillAndSubmit mengisi form yang sudah terbuka, mencentang pernyataan, lalu menekan Simpan dan Kirim.
// Pernyataan dicentang atas nama pengguna karena pengguna sudah meninjau dan menyetujui draf di chat.
func fillAndSubmit(ctx context.Context, lb ai.Logbook) (Result, error) {
	if err := chromedp.Do(ctx,
		setField(labelActivity, lb.Aktivitas),
		setField(labelLesson, lb.Pembelajaran),
		setField(labelObstacle, lb.Kendala),
		check(labelConfirm),
	); err != nil {
		return Result{}, fmt.Errorf("gagal mengisi form: %w", err)
	}

	if _, err := waitFor(ctx, jsFunc(`t => {
		const b = [...document.querySelectorAll("button")].find(b => b.textContent.trim() === t);
		return b && !b.disabled ? "ok" : "";
	}`, submitText), 15*time.Second); err != nil {
		return Result{}, fmt.Errorf("tombol %q tidak aktif; ada kolom yang belum valid: %w", submitText, err)
	}
	if err := chromedp.Do(ctx, chromedp.Click(fmt.Sprintf(`//button[normalize-space(.)=%q]`, submitText))); err != nil {
		return Result{}, fmt.Errorf("gagal menekan %q: %w", submitText, err)
	}

	outcome, err := waitFor(ctx, jsFunc(`ok => {
		if (document.body.innerText.includes(ok)) return "ok";
		const err = [...document.querySelectorAll(".v-snackbar__content, [role=alert]")].map(e => e.innerText.trim()).find(Boolean);
		return err ? "error:" + err : "";
	}`, successText), 45*time.Second)
	if err != nil {
		return Result{}, fmt.Errorf("tidak ada konfirmasi dari Monev setelah menekan %q: %w", submitText, err)
	}
	if msg, failed := strings.CutPrefix(outcome, "error:"); failed {
		return Result{}, fmt.Errorf("Monev menolak: %s", msg)
	}

	time.Sleep(2 * time.Second) // beri waktu kalender memuat status terbaru
	shot, err := chromedp.Run(ctx, chromedp.FullScreenshot(100))
	if err != nil {
		log.Printf("monev: gagal screenshot bukti: %v", err)
	}
	return Result{Message: "✅ " + successText, Screenshot: shot}, nil
}

// CheckLogin hanya login dan membuka halaman riwayat, tanpa mengirim apa pun. Untuk uji akun.
func (b *Browser) CheckLogin(ctx context.Context) ([]byte, error) {
	var shot []byte
	err := b.session(ctx, func(ctx context.Context) error {
		ready := `/^\/dashboard/.test(location.pathname) && document.querySelectorAll("h1, h2, label").length > 0`
		if err := b.open(ctx, riwayatURL, ready, "halaman riwayat Monev tidak termuat"); err != nil {
			return err
		}
		time.Sleep(2 * time.Second)
		var err error
		shot, err = chromedp.Run(ctx, chromedp.FullScreenshot(100))
		return err
	})
	return shot, err
}

// session menyalakan Chromium, menjalankan fn, lalu mematikannya. Saat gagal, screenshot
// halaman terakhir disimpan di ProfileDir/../monev-error.png untuk diagnosis.
func (b *Browser) session(parent context.Context, fn func(context.Context) error) error {
	if b.Email == "" || b.Password == "" {
		return errors.New("SIAPKERJA_EMAIL dan SIAPKERJA_PASSWORD belum diisi")
	}
	if err := os.MkdirAll(b.ProfileDir, 0o700); err != nil {
		return err
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.UserDataDir(b.ProfileDir),
		chromedp.WindowSize(1280, 900),
		// Kalender Monev memakai tanggal lokal browser; samakan dengan zona pengguna, bukan zona VPS.
		chromedp.Env("TZ="+b.Location.String()),
	)
	if b.ExecPath != "" {
		opts = append(opts, chromedp.ExecPath(b.ExecPath))
	}
	if !b.Headless {
		opts = append(opts, chromedp.Flag("headless", false))
	}
	if os.Geteuid() == 0 {
		opts = append(opts, chromedp.Flag("no-sandbox", true)) // Chrome menolak jalan sebagai root tanpa ini
	}

	ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
	defer cancel()
	actx, acancel := chromedp.NewExecAllocator(ctx, opts...)
	defer acancel()
	bctx, bcancel := chromedp.NewContext(actx)
	defer bcancel()

	if err := chromedp.Do(bctx); err != nil {
		return fmt.Errorf("gagal menyalakan Chromium (isi CHROME_PATH bila belum terpasang di PATH): %w", err)
	}
	if _, err := chromedp.CallBrowser(bctx, browser.SetPermission, browser.SetPermissionParams{
		Permission: &browser.PermissionDescriptor{Name: "geolocation"},
		Setting:    browser.PermissionSettingDenied,
	}); err != nil {
		return fmt.Errorf("gagal menolak izin lokasi: %w", err)
	}

	err := fn(bctx)
	if err != nil {
		b.saveErrorScreenshot(bctx)
	}
	return err
}

// openTodayForm membuka form kehadiran + laporan hari ini, login lebih dulu bila sesi habis.
func (b *Browser) openTodayForm(ctx context.Context) error {
	ready := jsFunc(`(l, d) => [...document.querySelectorAll("label")].some(e => e.textContent.trim() === l) || document.body.innerText.includes(d)`,
		labelActivity, disabledNotice)
	if err := b.open(ctx, todayFormURL, ready,
		"form kehadiran hari ini tidak terbuka; mungkin sudah terisi, hari libur, atau status magang tidak aktif"); err != nil {
		return err
	}
	return reportingDisabled(ctx)
}

// reportingDisabled mengembalikan error berisi pemberitahuan Monev bila pengisian laporan dinonaktifkan.
func reportingDisabled(ctx context.Context) error {
	notice, err := chromedp.Run(ctx, chromedp.Evaluate[string](jsFunc(`d => {
		const h = [...document.querySelectorAll("body *")].find(e => e.children.length === 0 && e.textContent.trim() === d);
		return h ? (h.parentElement.innerText || d).replace(/\s+/g, " ").trim() : "";
	}`, disabledNotice)))
	if err != nil {
		return fmt.Errorf("gagal membaca halaman Monev: %w", err)
	}
	if notice != "" {
		return fmt.Errorf("Monev: %s", notice)
	}
	return nil
}

// open membuka target sampai ekspresi ready bernilai true. Bila diarahkan ke form login
// SIAPkerja, kredensial diisi lalu target dibuka sekali lagi.
func (b *Browser) open(ctx context.Context, target, ready, notReadyMsg string) error {
	state := `(() => {
		if (location.host === "` + accountHost + `" && document.getElementById("username")) return "login";
		if (location.host === "` + monevHost + `" && (` + ready + `)) return "ready";
		return "";
	})()`

	for attempt := 0; ; attempt++ {
		if err := chromedp.Do(ctx, chromedp.Navigate(target)); err != nil {
			return fmt.Errorf("gagal membuka Monev: %w", err)
		}
		got, err := waitFor(ctx, state, 45*time.Second)
		if err != nil {
			return fmt.Errorf("%s: %w", notReadyMsg, err)
		}
		if got == "ready" {
			return nil
		}
		if attempt > 0 {
			return errors.New("masih diarahkan ke halaman login SIAPkerja setelah login")
		}
		if err := b.login(ctx); err != nil {
			return err
		}
	}
}

// login mengisi form SIAPkerja yang sedang terbuka lalu menunggu diarahkan kembali ke Monev.
func (b *Browser) login(ctx context.Context) error {
	log.Print("monev: sesi habis, login ke SIAPkerja")
	if err := chromedp.Do(ctx,
		setByID("username", b.Email),
		setByID("password", b.Password),
		chromedp.Click(selLoginButton),
	); err != nil {
		return fmt.Errorf("gagal mengisi form login SIAPkerja: %w", err)
	}

	state, err := waitFor(ctx, `(() => {
		if (location.host === "`+monevHost+`" && !/^\/(login|sso|authenticate)/.test(location.pathname)) return "monev";
		const err = [...document.querySelectorAll(`+loginErrorSelector+`)].map(e => e.innerText.trim()).find(Boolean);
		return err ? "error:" + err : "";
	})()`, 60*time.Second)
	if err != nil {
		return fmt.Errorf("login SIAPkerja tidak selesai (cek kredensial atau verifikasi tambahan): %w", err)
	}
	if msg, failed := strings.CutPrefix(state, "error:"); failed {
		return fmt.Errorf("login SIAPkerja ditolak: %s", msg)
	}
	log.Print("monev: login berhasil")
	return nil
}

func (b *Browser) saveErrorScreenshot(ctx context.Context) {
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	shot, err := chromedp.Run(sctx, chromedp.FullScreenshot(100))
	if err != nil {
		return
	}
	path := filepath.Join(filepath.Dir(b.ProfileDir), "monev-error.png")
	if err := os.WriteFile(path, shot, 0o600); err == nil {
		log.Printf("monev: screenshot error disimpan di %s", path)
	}
}

// fillJS mengisi elemen dan memicu event input/change agar v-model Vue ikut berubah
// (menimpa isian lama, termasuk autofill atau draf yang sudah tersimpan).
const fillJS = `(el, value) => {
	el.focus();
	el.value = value;
	el.dispatchEvent(new Event("input", { bubbles: true }));
	el.dispatchEvent(new Event("change", { bubbles: true }));
	el.blur();
}`

// setField mengisi kolom berdasarkan teks label-nya (setara get_by_label).
func setField(label, value string) chromedp.Action[chromedp.Void] {
	return chromedp.Evaluate[chromedp.Void](jsFunc(`(label, value) => {
		const l = [...document.querySelectorAll("label")].find(e => e.textContent.trim() === label);
		const el = l && document.getElementById(l.htmlFor);
		if (!el) throw new Error("kolom tidak ditemukan: " + label);
		(`+fillJS+`)(el, value);
	}`, label, value))
}

// setByID mengisi kolom berdasarkan id-nya (#username, #password di SIAPkerja).
func setByID(id, value string) chromedp.Action[chromedp.Void] {
	return chromedp.Evaluate[chromedp.Void](jsFunc(`(id, value) => {
		const el = document.getElementById(id);
		if (!el) throw new Error("kolom tidak ditemukan: #" + id);
		(`+fillJS+`)(el, value);
	}`, id, value))
}

// check mencentang checkbox berdasarkan label-nya bila belum tercentang.
func check(label string) chromedp.Action[chromedp.Void] {
	return chromedp.Evaluate[chromedp.Void](jsFunc(`label => {
		const l = [...document.querySelectorAll("label")].find(e => e.textContent.trim() === label);
		const el = l && document.getElementById(l.htmlFor);
		if (!el) throw new Error("checkbox tidak ditemukan: " + label);
		if (!el.checked) el.click();
	}`, label))
}

// waitFor menilai ekspresi JS berulang sampai hasilnya string tidak kosong. Berbeda dengan
// chromedp.Poll, loop ini tahan navigasi, jadi aman dipakai selama redirect SSO lintas domain.
func waitFor(ctx context.Context, expr string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		ectx, cancel := context.WithTimeout(ctx, 5*time.Second)
		v, err := chromedp.Run(ectx, chromedp.Evaluate[string](expr))
		cancel()
		if err == nil && v != "" {
			return v, nil
		}
		if err != nil {
			lastErr = err // halaman sedang berpindah; coba lagi
		}
		if time.Now().After(deadline) {
			if lastErr != nil {
				return "", fmt.Errorf("waktu tunggu %s habis: %w", timeout, lastErr)
			}
			return "", fmt.Errorf("waktu tunggu %s habis", timeout)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// jsFunc membuat ekspresi yang memanggil fungsi JS dengan argumen Go yang di-encode sebagai JSON.
func jsFunc(fn string, args ...any) string {
	encoded := make([]string, len(args))
	for i, a := range args {
		b, _ := json.Marshal(a)
		encoded[i] = string(b)
	}
	return "(" + fn + ")(" + strings.Join(encoded, ", ") + ")"
}
