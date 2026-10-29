package bot

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/mdp/qrterminal/v3"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite" // driver SQLite pure Go (tanpa cgo) untuk sesi WhatsApp
)

const waActionsHint = "\n\nBalas:\n1 = ✅ Kirim sekarang\n2 = ✏️ Revisi\n3 = ❌ Batal\natau langsung ketik instruksi revisi."

// WhatsApp adalah adapter Messenger lewat whatsmeow (WhatsApp Web multi-device, tidak resmi).
// Nomor bot login via QR / kode tautan; hanya pesan dari nomor `to` yang dilayani.
type WhatsApp struct {
	cli        *whatsmeow.Client
	flow       *Flow
	to         types.JID // nomor pengguna yang dilayani
	pairNumber string    // opsional: login pakai kode tautan, bukan QR
	ctx        context.Context
}

// NewWhatsApp membuka (atau membuat) file sesi di dbPath. Nomor dalam format internasional tanpa "+", misal 6281234567890.
func NewWhatsApp(ctx context.Context, dbPath, allowedNumber, pairNumber string, flow *Flow) (*WhatsApp, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return nil, err
	}
	container, err := sqlstore.New(ctx, "sqlite",
		"file:"+dbPath+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)",
		waLog.Stdout("WA-DB", "WARN", true))
	if err != nil {
		return nil, fmt.Errorf("gagal membuka sesi WhatsApp: %w", err)
	}
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		return nil, fmt.Errorf("gagal membaca sesi WhatsApp: %w", err)
	}
	return &WhatsApp{
		cli:        whatsmeow.NewClient(device, waLog.Stdout("WA", "WARN", true)),
		flow:       flow,
		to:         types.NewJID(allowedNumber, types.DefaultUserServer),
		pairNumber: pairNumber,
	}, nil
}

// Run login (bila belum) lalu menerima pesan sampai ctx dibatalkan.
func (w *WhatsApp) Run(ctx context.Context) error {
	w.ctx = ctx
	w.cli.AddEventHandler(w.handleEvent)

	if w.cli.Store.ID == nil {
		if err := w.login(ctx); err != nil {
			if ctx.Err() != nil {
				return nil // dihentikan saat menunggu scan, bukan error
			}
			return err
		}
	} else if err := w.cli.Connect(); err != nil {
		return fmt.Errorf("gagal terhubung ke WhatsApp: %w", err)
	}
	defer w.cli.Disconnect()

	if w.cli.Store.ID.User == w.to.User {
		return errors.New("nomor bot WhatsApp sama dengan WA_ALLOWED_NUMBER; bot butuh nomor sendiri karena pesan dari diri sendiri diabaikan")
	}
	log.Printf("whatsapp: terhubung sebagai %s, melayani %s", w.cli.Store.ID.User, w.to.User)
	<-ctx.Done()
	return nil
}

func (w *WhatsApp) login(ctx context.Context) error {
	qrChan, err := w.cli.GetQRChannel(ctx)
	if err != nil {
		return err
	}
	if err := w.cli.Connect(); err != nil {
		return fmt.Errorf("gagal terhubung ke WhatsApp: %w", err)
	}

	pairRequested := false
	for item := range qrChan {
		switch item.Event {
		case whatsmeow.QRChannelEventCode:
			if w.pairNumber == "" {
				fmt.Println("\nScan QR ini dari WhatsApp nomor bot: Setelan > Perangkat tertaut > Tautkan perangkat")
				qrterminal.GenerateHalfBlock(item.Code, qrterminal.L, os.Stdout)
				continue
			}
			if !pairRequested {
				pairRequested = true
				code, err := w.cli.PairPhone(ctx, w.pairNumber, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
				if err != nil {
					w.cli.Disconnect()
					return fmt.Errorf("gagal meminta kode tautan WhatsApp: %w", err)
				}
				log.Printf("whatsapp: di HP nomor bot buka Perangkat tertaut > Tautkan perangkat > Tautkan dengan nomor telepon, lalu masukkan kode %s", code)
			}
		case whatsmeow.QRChannelSuccess.Event:
			log.Print("whatsapp: login berhasil, sesi disimpan")
		default:
			w.cli.Disconnect()
			if item.Error != nil {
				return fmt.Errorf("login WhatsApp gagal: %w", item.Error)
			}
			return fmt.Errorf("login WhatsApp gagal (%s); jalankan ulang untuk mencoba lagi", item.Event)
		}
	}
	if w.cli.Store.ID == nil {
		w.cli.Disconnect()
		return errors.New("login WhatsApp tidak selesai")
	}
	return nil
}

func (w *WhatsApp) handleEvent(evt any) {
	switch v := evt.(type) {
	case *events.Message:
		info := v.Info
		if info.IsFromMe || info.IsGroup || info.Chat.Server == types.BroadcastServer {
			return
		}
		text := v.Message.GetConversation()
		if text == "" {
			text = v.Message.GetExtendedTextMessage().GetText()
		}
		if text == "" {
			return
		}
		// whatsmeow memanggil handler berurutan; AI/submit dijalankan terpisah agar tidak menahan event lain.
		go func() {
			if !w.isAllowed(w.ctx, info) {
				log.Printf("whatsapp: pesan dari %s diabaikan", info.Sender)
				return
			}
			w.flow.HandleText(w.ctx, w, text)
		}()
	case *events.LoggedOut:
		log.Print("whatsapp: sesi keluar dari HP; hapus file sesi lalu jalankan ulang untuk login lagi")
	}
}

// isAllowed mencocokkan pengirim dengan nomor yang diizinkan, termasuk bila WhatsApp memakai alamat LID.
func (w *WhatsApp) isAllowed(ctx context.Context, info types.MessageInfo) bool {
	for _, j := range []types.JID{info.Sender, info.SenderAlt} {
		switch j.Server {
		case types.DefaultUserServer:
			if j.User == w.to.User {
				return true
			}
		case types.HiddenUserServer:
			pn, err := w.cli.Store.LIDs.GetPNForLID(ctx, j.ToNonAD())
			if err == nil && pn.User == w.to.User {
				return true
			}
		}
	}
	return false
}

func (w *WhatsApp) Send(ctx context.Context, text string, withActions bool) {
	if withActions {
		text += waActionsHint
	}
	if _, err := w.cli.SendMessage(ctx, w.to, &waE2E.Message{Conversation: proto.String(text)}); err != nil {
		log.Printf("whatsapp: gagal kirim pesan: %v", err)
	}
}

func (w *WhatsApp) SendPhoto(ctx context.Context, png []byte, caption string) error {
	up, err := w.cli.Upload(ctx, png, whatsmeow.MediaImage)
	if err != nil {
		return err
	}
	_, err = w.cli.SendMessage(ctx, w.to, &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
		Caption:       proto.String(caption),
		Mimetype:      proto.String("image/png"),
		URL:           proto.String(up.URL),
		DirectPath:    proto.String(up.DirectPath),
		MediaKey:      up.MediaKey,
		FileEncSHA256: up.FileEncSHA256,
		FileSHA256:    up.FileSHA256,
		FileLength:    proto.Uint64(up.FileLength),
	}})
	return err
}

func (w *WhatsApp) Typing(ctx context.Context) {
	_ = w.cli.SendChatPresence(ctx, w.to, types.ChatPresenceComposing, types.ChatPresenceMediaText)
}
