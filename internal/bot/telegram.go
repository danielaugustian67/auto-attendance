package bot

import (
	"bytes"
	"context"
	"log"

	tg "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// Telegram adalah adapter Messenger untuk satu pengguna Telegram.
type Telegram struct {
	tg     *tg.Bot
	flow   *Flow
	userID int64
}

func NewTelegram(token string, userID int64, flow *Flow) (*Telegram, error) {
	t := &Telegram{flow: flow, userID: userID}
	b, err := tg.New(token, tg.WithDefaultHandler(t.handle))
	if err != nil {
		return nil, err
	}
	t.tg = b
	return t, nil
}

// Run memulai long polling sampai ctx dibatalkan.
func (t *Telegram) Run(ctx context.Context) {
	t.tg.Start(ctx)
}

func (t *Telegram) handle(ctx context.Context, _ *tg.Bot, u *models.Update) {
	// go-telegram/bot sudah menjalankan tiap handler di goroutine sendiri.
	switch {
	case u.CallbackQuery != nil:
		if u.CallbackQuery.From.ID != t.userID {
			return
		}
		_, _ = t.tg.AnswerCallbackQuery(ctx, &tg.AnswerCallbackQueryParams{CallbackQueryID: u.CallbackQuery.ID})
		t.flow.HandleAction(ctx, t, Action(u.CallbackQuery.Data))
	case u.Message != nil && u.Message.From != nil:
		if u.Message.From.ID != t.userID {
			log.Printf("telegram: pesan dari user tidak dikenal %d diabaikan", u.Message.From.ID)
			return
		}
		t.flow.HandleText(ctx, t, u.Message.Text)
	}
}

func (t *Telegram) Send(ctx context.Context, text string, withActions bool) {
	params := &tg.SendMessageParams{ChatID: t.userID, Text: text}
	if withActions {
		params.Text += "\n\nPilih tombol di bawah, atau ketik instruksi revisi."
		params.ReplyMarkup = &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
			{{Text: "✅ Kirim Sekarang", CallbackData: string(ActionSubmit)}},
			{{Text: "✏️ Revisi", CallbackData: string(ActionRevise)}, {Text: "❌ Batal", CallbackData: string(ActionCancel)}},
		}}
	}
	if _, err := t.tg.SendMessage(ctx, params); err != nil {
		log.Printf("telegram: gagal kirim pesan: %v", err)
	}
}

func (t *Telegram) SendPhoto(ctx context.Context, png []byte, caption string) error {
	_, err := t.tg.SendPhoto(ctx, &tg.SendPhotoParams{
		ChatID:  t.userID,
		Photo:   &models.InputFileUpload{Filename: "bukti.png", Data: bytes.NewReader(png)},
		Caption: caption,
	})
	return err
}

func (t *Telegram) Typing(ctx context.Context) {
	_, _ = t.tg.SendChatAction(ctx, &tg.SendChatActionParams{ChatID: t.userID, Action: models.ChatActionTyping})
}
