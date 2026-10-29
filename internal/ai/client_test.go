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

func TestValidateMinChars(t *testing.T) {
	lb := Logbook{Aktivitas: "pendek", Pembelajaran: "x", Kendala: "y"}
	if err := lb.Validate(); err == nil {
		t.Fatal("aktivitas < 100 karakter seharusnya ditolak")
	}
	lb.Aktivitas = strings.Repeat("a", MinAktivitasChars)
	if err := lb.Validate(); err != nil {
		t.Fatal(err)
	}
}

// Jawaban pertama terlalu pendek, client harus meminta perbaikan sekali lalu menerima jawaban kedua.
func TestDraftRetriesOnInvalidAnswer(t *testing.T) {
	answers := []Logbook{
		{Aktivitas: "pendek", Pembelajaran: "x", Kendala: "y"},
		{Aktivitas: strings.Repeat("a", MinAktivitasChars), Pembelajaran: "x", Kendala: "y"},
	}
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
	if calls != 2 || len(lb.Aktivitas) != MinAktivitasChars {
		t.Fatalf("calls=%d aktivitas=%d", calls, len(lb.Aktivitas))
	}
}
