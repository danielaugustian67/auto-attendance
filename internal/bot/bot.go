// Package bot menangani percakapan Telegram: tanya kegiatan -> draf AI -> konfirmasi -> kirim.
package bot

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	tg "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"auto-attendance/internal/ai"
	"auto-attendance/internal/monev"
)

type state int

const (
	stateIdle          state = iota
	stateAwaitingNotes       // sudah ditanya, menunggu catatan kegiatan
	stateGenerating          // AI sedang menyusun draf
	stateReviewing           // draf ditampilkan, menunggu konfirmasi atau instruksi revisi
	stateSubmitting          // sedang mengirim ke Monev
)

const (
	cbSubmit = "submit"
	cbRevise = "revise"
	cbCancel = "cancel"
)

type Bot struct {
	tg     *tg.Bot
	ai     *ai.Client
	sub    monev.Submitter
	userID int64
	loc    *time.Location

	mu            sync.Mutex
	state         state
	notes         string
	draft         ai.Logbook
	draftDate     time.Time
	lastSubmitted string // tanggal "2006-01-02" terakhir berhasil dikirim (in-memory)
}

func New(token string, userID int64, loc *time.Location, aiClient *ai.Client, sub monev.Submitter) (*Bot, error) {
	b := &Bot{ai: aiClient, sub: sub, userID: userID, loc: loc}
	t, err := tg.New(token, tg.WithDefaultHandler(b.handle))
	if err != nil {
		return nil, err
	}
	b.tg = t
	return b, nil
}

// Run memulai long polling sampai ctx dibatalkan.
func (b *Bot) Run(ctx context.Context) {
	b.tg.Start(ctx)
}

// Remind dipanggil scheduler; dilewati jika logbook hari ini sudah terkirim.
func (b *Bot) Remind(ctx context.Context) {
	b.mu.Lock()
	if b.lastSubmitted == b.today() {
		b.mu.Unlock()
		return
	}
	st := b.state
	if st == stateIdle {
		b.state = stateAwaitingNotes
	}
	b.mu.Unlock()

	switch st {
	case stateReviewing:
		b.send(ctx, "⏰ Draf logbook hari ini belum dikirim. Kirim sekarang sebelum 23.59 WIB?", confirmKeyboard())
	case stateGenerating, stateSubmitting:
		// sedang diproses, tidak perlu mengganggu
	default:
		b.send(ctx, "⏰ Logbook hari ini belum diisi. Apa saja kegiatanmu hari ini? Ketik poin-poin singkat saja.", nil)
	}
}

func (b *Bot) handle(ctx context.Context, _ *tg.Bot, u *models.Update) {
	switch {
	case u.CallbackQuery != nil:
		if u.CallbackQuery.From.ID != b.userID {
			return
		}
		_, _ = b.tg.AnswerCallbackQuery(ctx, &tg.AnswerCallbackQueryParams{CallbackQueryID: u.CallbackQuery.ID})
		b.handleCallback(ctx, u.CallbackQuery.Data)
	case u.Message != nil && u.Message.From != nil:
		if u.Message.From.ID != b.userID {
			log.Printf("pesan dari user tidak dikenal %d diabaikan", u.Message.From.ID)
			return
		}
		b.handleText(ctx, strings.TrimSpace(u.Message.Text))
	}
}

func (b *Bot) handleText(ctx context.Context, text string) {
	if text == "" {
		return
	}
	switch strings.ToLower(strings.Fields(text)[0]) {
	case "/start", "/help":
		b.send(ctx, "Halo! Aku bantu isi logbook Monev MagangHub.\n\n/isi – mulai isi logbook hari ini\n/status – cek status hari ini\n/batal – batalkan draf\n\nAtau langsung ketik kegiatanmu hari ini.", nil)
		return
	case "/isi":
		b.mu.Lock()
		busy := b.state == stateGenerating || b.state == stateSubmitting
		if !busy {
			b.state = stateAwaitingNotes
		}
		b.mu.Unlock()
		if busy {
			b.send(ctx, "⏳ Masih ada proses berjalan, tunggu sebentar.", nil)
			return
		}
		b.send(ctx, "Apa saja kegiatanmu hari ini? Ketik poin-poin singkat saja.", nil)
		return
	case "/status":
		b.send(ctx, b.statusText(), nil)
		return
	case "/batal":
		b.handleCallback(ctx, cbCancel)
		return
	}

	b.mu.Lock()
	st, notes, prev := b.state, b.notes, b.draft
	switch st {
	case stateGenerating, stateSubmitting:
		b.mu.Unlock()
		b.send(ctx, "⏳ Masih diproses, tunggu sebentar.", nil)
		return
	case stateReviewing:
		// teks saat review dianggap instruksi revisi
	default:
		notes = text
		b.notes = text
		b.draftDate = time.Now().In(b.loc)
	}
	b.state = stateGenerating
	b.mu.Unlock()

	b.typing(ctx)
	var (
		lb  ai.Logbook
		err error
	)
	if st == stateReviewing {
		lb, err = b.ai.Revise(ctx, notes, prev, text)
	} else {
		lb, err = b.ai.Draft(ctx, notes)
	}

	b.mu.Lock()
	if err != nil {
		if st == stateReviewing {
			b.state = stateReviewing
		} else {
			b.state = stateAwaitingNotes
		}
		b.mu.Unlock()
		log.Printf("gagal menyusun draf: %v", err)
		b.send(ctx, "❌ Gagal menyusun draf: "+err.Error()+"\n\nCoba kirim ulang pesanmu.", nil)
		return
	}
	b.draft = lb
	b.state = stateReviewing
	date := b.draftDate
	b.mu.Unlock()

	b.send(ctx, formatDraft(date, lb), confirmKeyboard())
}

func (b *Bot) handleCallback(ctx context.Context, data string) {
	switch data {
	case cbRevise:
		b.mu.Lock()
		ok := b.state == stateReviewing
		b.mu.Unlock()
		if ok {
			b.send(ctx, "✏️ Ketik bagian yang ingin diubah, misal: \"tambahkan detail soal rapat dengan mentor\".", nil)
		}
	case cbCancel:
		b.mu.Lock()
		if b.state == stateGenerating || b.state == stateSubmitting {
			b.mu.Unlock()
			b.send(ctx, "⏳ Proses sedang berjalan, tidak bisa dibatalkan sekarang.", nil)
			return
		}
		b.state, b.notes, b.draft = stateIdle, "", ai.Logbook{}
		b.mu.Unlock()
		b.send(ctx, "Draf dibatalkan. Ketik /isi untuk mulai lagi.", nil)
	case cbSubmit:
		b.submit(ctx)
	}
}

func (b *Bot) submit(ctx context.Context) {
	b.mu.Lock()
	if b.state != stateReviewing {
		b.mu.Unlock()
		return
	}
	b.state = stateSubmitting
	date, lb := b.draftDate, b.draft
	b.mu.Unlock()

	b.send(ctx, "🚀 Mengirim logbook ke Monev...", nil)
	res, err := b.sub.Submit(ctx, date, lb)

	b.mu.Lock()
	if err != nil {
		b.state = stateReviewing
		b.mu.Unlock()
		log.Printf("gagal submit: %v", err)
		b.send(ctx, "❌ Gagal mengirim: "+err.Error(), confirmKeyboard())
		return
	}
	b.state, b.notes, b.draft = stateIdle, "", ai.Logbook{}
	b.lastSubmitted = date.Format("2006-01-02")
	b.mu.Unlock()

	msg := res.Message
	if msg == "" {
		msg = "✅ Logbook berhasil dikirim."
	}
	if len(res.Screenshot) > 0 {
		_, err := b.tg.SendPhoto(ctx, &tg.SendPhotoParams{
			ChatID:  b.userID,
			Photo:   &models.InputFileUpload{Filename: "bukti.png", Data: bytes.NewReader(res.Screenshot)},
			Caption: msg,
		})
		if err == nil {
			return
		}
		log.Printf("gagal kirim screenshot: %v", err)
	}
	b.send(ctx, msg, nil)
}

func (b *Bot) statusText() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.lastSubmitted == b.today() {
		return "✅ Logbook hari ini sudah terkirim."
	}
	switch b.state {
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

func (b *Bot) today() string {
	return time.Now().In(b.loc).Format("2006-01-02")
}

func (b *Bot) send(ctx context.Context, text string, markup models.ReplyMarkup) {
	params := &tg.SendMessageParams{ChatID: b.userID, Text: text}
	if markup != nil {
		params.ReplyMarkup = markup
	}
	if _, err := b.tg.SendMessage(ctx, params); err != nil {
		log.Printf("gagal kirim pesan: %v", err)
	}
}

func (b *Bot) typing(ctx context.Context) {
	_, _ = b.tg.SendChatAction(ctx, &tg.SendChatActionParams{ChatID: b.userID, Action: models.ChatActionTyping})
}

func confirmKeyboard() *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{{Text: "✅ Kirim Sekarang", CallbackData: cbSubmit}},
		{{Text: "✏️ Revisi", CallbackData: cbRevise}, {Text: "❌ Batal", CallbackData: cbCancel}},
	}}
}

func formatDraft(date time.Time, lb ai.Logbook) string {
	return fmt.Sprintf("📋 Draf logbook %s\n\n📝 Uraian Aktivitas (%d karakter):\n%s\n\n💡 Pelajaran yang Diperoleh:\n%s\n\n⚠️ Kendala yang Dihadapi:\n%s\n\nTekan Kirim, atau ketik instruksi revisi.",
		date.Format("02-01-2006"), utf8.RuneCountInString(lb.Aktivitas), lb.Aktivitas, lb.Pembelajaran, lb.Kendala)
}
