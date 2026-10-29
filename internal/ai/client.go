// Package ai berisi client OpenAI-compatible ke 9Router untuk menyusun logbook.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
}

func NewClient(baseURL, apiKey, model string) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		http:    &http.Client{Timeout: 90 * time.Second},
	}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

// Draft menyusun logbook dari catatan singkat pengguna.
func (c *Client) Draft(ctx context.Context, notes string) (Logbook, error) {
	return c.generate(ctx, draftUserPrompt(notes))
}

// Revise menyusun ulang draf sebelumnya sesuai instruksi pengguna.
func (c *Client) Revise(ctx context.Context, notes string, prev Logbook, instruction string) (Logbook, error) {
	return c.generate(ctx, revisionUserPrompt(notes, prev, instruction))
}

// generate memanggil model dan, bila hasilnya tidak valid, meminta perbaikan satu kali.
func (c *Client) generate(ctx context.Context, userPrompt string) (Logbook, error) {
	msgs := []chatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		raw, err := c.complete(ctx, msgs)
		if err != nil {
			return Logbook{}, err
		}
		lb, err := parseLogbook(raw)
		if err == nil {
			err = lb.Validate()
		}
		if err == nil {
			return lb, nil
		}
		lastErr = err
		msgs = append(msgs,
			chatMessage{Role: "assistant", Content: raw},
			chatMessage{Role: "user", Content: "Jawaban belum memenuhi syarat (" + err.Error() + "). Perbaiki dan balas hanya dengan JSON."},
		)
	}
	return Logbook{}, fmt.Errorf("AI gagal menghasilkan logbook valid: %w", lastErr)
}

func (c *Client) complete(ctx context.Context, msgs []chatMessage) (string, error) {
	body, err := json.Marshal(chatRequest{Model: c.model, Messages: msgs, Temperature: 0.4})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("gagal menghubungi 9Router: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("9Router membalas %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}

	var cr chatResponse
	if err := json.Unmarshal(data, &cr); err != nil {
		return "", fmt.Errorf("respons 9Router tidak bisa dibaca: %w", err)
	}
	if len(cr.Choices) == 0 {
		return "", fmt.Errorf("respons 9Router tidak berisi pilihan jawaban")
	}
	return cr.Choices[0].Message.Content, nil
}

// parseLogbook mengambil objek JSON dari jawaban model, toleran terhadap blok ```json.
func parseLogbook(raw string) (Logbook, error) {
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return Logbook{}, fmt.Errorf("jawaban tidak berisi JSON")
	}
	var lb Logbook
	if err := json.Unmarshal([]byte(raw[start:end+1]), &lb); err != nil {
		return Logbook{}, fmt.Errorf("JSON tidak valid: %w", err)
	}
	lb.Aktivitas = strings.TrimSpace(lb.Aktivitas)
	lb.Pembelajaran = strings.TrimSpace(lb.Pembelajaran)
	lb.Kendala = strings.TrimSpace(lb.Kendala)
	return lb, nil
}
