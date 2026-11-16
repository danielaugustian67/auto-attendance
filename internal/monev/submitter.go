// Package monev berisi pengiriman logbook ke portal Monev MagangHub.
package monev

import (
	"context"
	"errors"
	"time"

	"auto-attendance/internal/ai"
)

// Result adalah bukti hasil pengiriman yang diteruskan ke chat pengguna.
type Result struct {
	Message    string
	Screenshot []byte // PNG, opsional
}

// Submitter mengirim logbook untuk tanggal tertentu ke Monev.
// Implementasinya bisa lewat API langsung (ringan) atau browser headless.
type Submitter interface {
	Submit(ctx context.Context, date time.Time, lb ai.Logbook) (Result, error)
}

var ErrNoCredentials = errors.New("pengiriman ke Monev belum aktif: isi SIAPKERJA_EMAIL dan SIAPKERJA_PASSWORD di .env lalu restart bot")

// Disabled dipakai bila kredensial SIAPkerja belum diisi; draf tetap bisa disusun.
type Disabled struct{}

func (Disabled) Submit(context.Context, time.Time, ai.Logbook) (Result, error) {
	return Result{}, ErrNoCredentials
}
