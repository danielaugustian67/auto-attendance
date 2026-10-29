// Package bot menangani percakapan: tanya kegiatan -> draf AI -> konfirmasi -> kirim.
// Alurnya (Flow) tidak bergantung channel; Telegram dan WhatsApp hanya adapter Messenger.
package bot

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"auto-attendance/internal/ai"
	"auto-attendance/internal/monev"
)

// Messenger mengirim balasan ke pengguna di satu channel.
type Messenger interface {
	// Send mengirim teks; withActions menampilkan pilihan Kirim/Revisi/Batal.
	Send(ctx context.Context, text string, withActions bool)
	SendPhoto(ctx context.Context, png []byte, caption string) error
	Typing(ctx context.Context)
}

// Action adalah pilihan pengguna saat draf ditampilkan.
type Action string

const (
	ActionSubmit Action = "submit"
	ActionRevise Action = "revise"
	ActionCancel Action = "cancel"
)

type state int

const (
	stateIdle          state = iota
	stateAwaitingNotes       // sudah ditanya, menunggu catatan kegiatan
	stateGenerating          // AI sedang menyusun draf
	stateReviewing           // draf ditampilkan, menunggu konfirmasi atau instruksi revisi
	stateSubmitting          // sedang mengirim ke Monev
)

const helpText = "Halo! Aku bantu isi logbook Monev MagangHub.\n\n" +
	"/isi – mulai isi logbook hari ini\n/status – cek status hari ini\n/batal – batalkan draf\n\n" +
	"Atau langsung ketik kegiatanmu hari ini. Saat draf tampil, pilih Kirim/Revisi/Batal atau ketik instruksi revisi."

type Flow struct {
	ai  *ai.Client
	sub monev.Submitter
	loc *time.Location

	mu            sync.Mutex
	targets       []Messenger // tujuan pengingat
	state         state
	notes         string
	draft         ai.Logbook
	draftDate     time.Time
	lastSubmitted string // tanggal "2006-01-02" terakhir berhasil dikirim (in-memory)
}

func NewFlow(aiClient *ai.Client, sub monev.Submitter, loc *time.Location) *Flow {
	return &Flow{ai: aiClient, sub: sub, loc: loc}
}

// AddReminderTarget mendaftarkan channel yang ikut menerima pengingat terjadwal.
func (f *Flow) AddReminderTarget(m Messenger) {
	f.mu.Lock()
	f.targets = append(f.targets, m)
	f.mu.Unlock()
}

// Remind dipanggil scheduler; dilewati jika logbook hari ini sudah terkirim.
func (f *Flow) Remind(ctx context.Context) {
	f.mu.Lock()
	if f.lastSubmitted == f.today() {
		f.mu.Unlock()
		return
	}
	st := f.state
	if st == stateIdle {
		f.state = stateAwaitingNotes
	}
	targets := append([]Messenger(nil), f.targets...)
	f.mu.Unlock()

	for _, m := range targets {
		switch st {
		case stateReviewing:
			m.Send(ctx, "⏰ Draf logbook hari ini belum dikirim. Kirim sekarang sebelum 23.59 WIB?", true)
		case stateGenerating, stateSubmitting:
			// sedang diproses, tidak perlu mengganggu
		default:
			m.Send(ctx, "⏰ Logbook hari ini belum diisi. Apa saja kegiatanmu hari ini? Ketik poin-poin singkat saja.", false)
		}
	}
}

// HandleText memproses pesan teks dari pengguna yang sudah lolos filter channel.
func (f *Flow) HandleText(ctx context.Context, m Messenger, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	// Perintah boleh dengan atau tanpa "/" (di WhatsApp tidak ada menu perintah).
	if cmd, ok := command(text); ok {
		switch cmd {
		case "start", "help":
			m.Send(ctx, helpText, false)
		case "isi":
			f.mu.Lock()
			busy := f.state == stateGenerating || f.state == stateSubmitting
			if !busy {
				f.state = stateAwaitingNotes
			}
			f.mu.Unlock()
			if busy {
				m.Send(ctx, "⏳ Masih ada proses berjalan, tunggu sebentar.", false)
				return
			}
			m.Send(ctx, "Apa saja kegiatanmu hari ini? Ketik poin-poin singkat saja.", false)
		case "status":
			m.Send(ctx, f.statusText(), false)
		case "batal":
			f.HandleAction(ctx, m, ActionCancel)
		}
		return
	}

	f.mu.Lock()
	st, notes, prev := f.state, f.notes, f.draft
	if st == stateReviewing {
		if a, ok := quickAction(text); ok {
			f.mu.Unlock()
			f.HandleAction(ctx, m, a)
			return
		}
	}
	switch st {
	case stateGenerating, stateSubmitting:
		f.mu.Unlock()
		m.Send(ctx, "⏳ Masih diproses, tunggu sebentar.", false)
		return
	case stateReviewing:
		// teks saat review dianggap instruksi revisi
	default:
		notes = text
		f.notes = text
		f.draftDate = time.Now().In(f.loc)
	}
	f.state = stateGenerating
	f.mu.Unlock()

	m.Typing(ctx)
	var (
		lb  ai.Logbook
		err error
	)
	if st == stateReviewing {
		lb, err = f.ai.Revise(ctx, notes, prev, text)
	} else {
		lb, err = f.ai.Draft(ctx, notes)
	}

	f.mu.Lock()
	if err != nil {
		if st == stateReviewing {
			f.state = stateReviewing
		} else {
			f.state = stateAwaitingNotes
		}
		f.mu.Unlock()
		log.Printf("gagal menyusun draf: %v", err)
		m.Send(ctx, "❌ Gagal menyusun draf: "+err.Error()+"\n\nCoba kirim ulang pesanmu.", false)
		return
	}
	f.draft = lb
	f.state = stateReviewing
	date := f.draftDate
	f.mu.Unlock()

	m.Send(ctx, formatDraft(date, lb), true)
}

// HandleAction memproses pilihan Kirim/Revisi/Batal.
func (f *Flow) HandleAction(ctx context.Context, m Messenger, a Action) {
	switch a {
	case ActionRevise:
		f.mu.Lock()
		ok := f.state == stateReviewing
		f.mu.Unlock()
		if ok {
			m.Send(ctx, "✏️ Ketik bagian yang ingin diubah, misal: \"tambahkan detail soal rapat dengan mentor\".", false)
		}
	case ActionCancel:
		f.mu.Lock()
		if f.state == stateGenerating || f.state == stateSubmitting {
			f.mu.Unlock()
			m.Send(ctx, "⏳ Proses sedang berjalan, tidak bisa dibatalkan sekarang.", false)
			return
		}
		f.state, f.notes, f.draft = stateIdle, "", ai.Logbook{}
		f.mu.Unlock()
		m.Send(ctx, "Draf dibatalkan. Ketik /isi untuk mulai lagi.", false)
	case ActionSubmit:
		f.submit(ctx, m)
	}
}

func (f *Flow) submit(ctx context.Context, m Messenger) {
	f.mu.Lock()
	if f.state != stateReviewing {
		f.mu.Unlock()
		return
	}
	f.state = stateSubmitting
	date, lb := f.draftDate, f.draft
	f.mu.Unlock()

	m.Send(ctx, "🚀 Mengirim logbook ke Monev...", false)
	res, err := f.sub.Submit(ctx, date, lb)

	f.mu.Lock()
	if err != nil {
		f.state = stateReviewing
		f.mu.Unlock()
		log.Printf("gagal submit: %v", err)
		m.Send(ctx, "❌ Gagal mengirim: "+err.Error(), true)
		return
	}
	f.state, f.notes, f.draft = stateIdle, "", ai.Logbook{}
	f.lastSubmitted = date.Format("2006-01-02")
	f.mu.Unlock()

	msg := res.Message
	if msg == "" {
		msg = "✅ Logbook berhasil dikirim."
	}
	if len(res.Screenshot) > 0 {
		err := m.SendPhoto(ctx, res.Screenshot, msg)
		if err == nil {
			return
		}
		log.Printf("gagal kirim screenshot: %v", err)
	}
	m.Send(ctx, msg, false)
}

func (f *Flow) statusText() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lastSubmitted == f.today() {
		return "✅ Logbook hari ini sudah terkirim."
	}
	switch f.state {
	case stateReviewing:
		return "📋 Draf sudah siap, menunggu konfirmasi kirim."
	case stateGenerating:
		return "⏳ Draf sedang disusun AI."
	case stateSubmitting:
		return "🚀 Logbook sedang dikirim ke Monev."
	default:
		return "⚠️ Logbook hari ini belum diisi. Ketik /isi untuk mulai."
	}
}

func (f *Flow) today() string {
	return time.Now().In(f.loc).Format("2006-01-02")
}

// command mengenali pesan satu kata seperti "/isi" atau "isi".
func command(text string) (string, bool) {
	if strings.ContainsAny(text, " \n\t") {
		return "", false
	}
	cmd := strings.ToLower(strings.TrimPrefix(text, "/"))
	if i := strings.IndexByte(cmd, '@'); i >= 0 { // "/isi@NamaBot" di Telegram
		cmd = cmd[:i]
	}
	switch cmd {
	case "start", "help", "isi", "status", "batal":
		return cmd, true
	}
	return "", false
}

// quickAction memetakan balasan singkat saat review ("1"/"kirim", dst) ke Action.
func quickAction(text string) (Action, bool) {
	switch strings.ToLower(text) {
	case "1", "kirim":
		return ActionSubmit, true
	case "2", "revisi":
		return ActionRevise, true
	case "3":
		return ActionCancel, true
	}
	return "", false
}

func formatDraft(date time.Time, lb ai.Logbook) string {
	return fmt.Sprintf("📋 Draf logbook %s\n\n📝 Uraian Aktivitas (%d karakter):\n%s\n\n💡 Pelajaran yang Diperoleh:\n%s\n\n⚠️ Kendala yang Dihadapi:\n%s",
		date.Format("02-01-2006"), utf8.RuneCountInString(lb.Aktivitas), lb.Aktivitas, lb.Pembelajaran, lb.Kendala)
}
