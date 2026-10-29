package bot

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"auto-attendance/internal/ai"
	"auto-attendance/internal/monev"
)

type sent struct {
	text        string
	withActions bool
}

type fakeMessenger struct {
	mu   sync.Mutex
	msgs []sent
}

func (m *fakeMessenger) Send(_ context.Context, text string, withActions bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.msgs = append(m.msgs, sent{text, withActions})
}
func (m *fakeMessenger) SendPhoto(context.Context, []byte, string) error {
	return errors.New("no photo")
}
func (m *fakeMessenger) Typing(context.Context) {}

func (m *fakeMessenger) last() sent {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.msgs) == 0 {
		return sent{}
	}
	return m.msgs[len(m.msgs)-1]
}

func (m *fakeMessenger) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.msgs)
}

type fakeSubmitter struct {
	calls int
	err   error
}

func (s *fakeSubmitter) Submit(context.Context, time.Time, ai.Logbook) (monev.Result, error) {
	s.calls++
	return monev.Result{}, s.err
}

func newTestFlow(t *testing.T, sub monev.Submitter) *Flow {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		content, _ := json.Marshal(ai.Logbook{Aktivitas: strings.Repeat("a", ai.MinAktivitasChars), Pembelajaran: "p", Kendala: "k"})
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": string(content)}}},
		})
	}))
	t.Cleanup(srv.Close)
	return NewFlow(ai.NewClient(srv.URL, "", "m"), sub, time.UTC)
}

func TestFlowDraftThenQuickSubmit(t *testing.T) {
	ctx := context.Background()
	sub := &fakeSubmitter{}
	f := newTestFlow(t, sub)
	m := &fakeMessenger{}
	f.AddReminderTarget(m)

	f.HandleText(ctx, m, "ngoding fitur login")
	if got := m.last(); !got.withActions || !strings.Contains(got.text, "Draf logbook") {
		t.Fatalf("draf tidak tampil dengan pilihan aksi: %+v", got)
	}

	f.HandleText(ctx, m, "1") // balasan cepat WhatsApp
	if sub.calls != 1 {
		t.Fatalf("submitter dipanggil %d kali, harusnya 1", sub.calls)
	}
	if got := m.last().text; !strings.Contains(got, "berhasil") {
		t.Fatalf("pesan sukses tidak dikirim: %q", got)
	}

	before := m.count()
	f.Remind(ctx)
	if m.count() != before {
		t.Fatal("pengingat seharusnya dilewati setelah logbook terkirim")
	}
}

func TestFlowSubmitFailureKeepsDraft(t *testing.T) {
	ctx := context.Background()
	sub := &fakeSubmitter{err: errors.New("monev down")}
	f := newTestFlow(t, sub)
	m := &fakeMessenger{}

	f.HandleText(ctx, m, "rapat dengan mentor")
	f.HandleAction(ctx, m, ActionSubmit)
	if got := m.last(); !got.withActions || !strings.Contains(got.text, "monev down") {
		t.Fatalf("gagal submit harus menampilkan error dan pilihan aksi lagi: %+v", got)
	}

	f.HandleText(ctx, m, "kirim")
	if sub.calls != 2 {
		t.Fatalf("draf seharusnya masih bisa dikirim ulang, calls=%d", sub.calls)
	}
}

func TestCommandParsing(t *testing.T) {
	cases := map[string]string{"/isi": "isi", "isi": "isi", "/status@MyBot": "status", "BATAL": "batal"}
	for in, want := range cases {
		if got, ok := command(in); !ok || got != want {
			t.Errorf("command(%q) = %q,%v; want %q", in, got, ok, want)
		}
	}
	if _, ok := command("isi laporan hari ini"); ok {
		t.Error("kalimat biasa tidak boleh dianggap perintah")
	}
}
