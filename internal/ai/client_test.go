package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseLogbookStripsCodeFence(t *testing.T) {
	raw := "```json\n{\"aktivitas\":\" a \",\"pembelajaran\":\"b\",\"kendala\":\"c\"}\n```"
	lb, err := parseLogbook(raw)
	if err != nil {
		t.Fatal(err)
	}
	if lb.Aktivitas != "a" || lb.Pembelajaran != "b" || lb.Kendala != "c" {
		t.Fatalf("hasil parse salah: %+v", lb)
	}
}

func validLogbook() Logbook {
	return Logbook{
		Aktivitas:    strings.Repeat("a", MinFieldChars),
		Pembelajaran: strings.Repeat("p", MinFieldChars),
		Kendala:      strings.Repeat("k", MinFieldChars),
	}
}

// Monev mewajibkan minimal 100 karakter di setiap kolom, bukan hanya aktivitas.
func TestValidateMinCharsEveryField(t *testing.T) {
	if err := validLogbook().Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Logbook){
		func(l *Logbook) { l.Aktivitas = "pendek" },
		func(l *Logbook) { l.Pembelajaran = "pendek" },
		func(l *Logbook) { l.Kendala = "pendek" },
	} {
		lb := validLogbook()
		mutate(&lb)
		if err := lb.Validate(); err == nil {
			t.Errorf("kolom < %d karakter seharusnya ditolak: %+v", MinFieldChars, lb)
		}
	}
}

// Jawaban pertama terlalu pendek, client harus meminta perbaikan sekali lalu menerima jawaban kedua.
func TestDraftRetriesOnInvalidAnswer(t *testing.T) {
	short := validLogbook()
	short.Kendala = "tidak ada"
	answers := []Logbook{short, validLogbook()}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("request tak terduga: %s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		content, _ := json.Marshal(answers[calls])
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": string(content)}}},
		})
	}))
	defer srv.Close()

	lb, err := NewClient(srv.URL+"/v1", "k", "m").Draft(context.Background(), "ngoding")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || lb != validLogbook() {
		t.Fatalf("calls=%d hasil=%+v", calls, lb)
	}
}
