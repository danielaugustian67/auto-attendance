package ai

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// MinAktivitasChars adalah batas minimal karakter Uraian Aktivitas di Monev.
const MinAktivitasChars = 100

// Logbook adalah tiga kolom laporan harian Monev MagangHub.
type Logbook struct {
	Aktivitas    string `json:"aktivitas"`
	Pembelajaran string `json:"pembelajaran"`
	Kendala      string `json:"kendala"`
}

func (l Logbook) Validate() error {
	var missing []string
	if strings.TrimSpace(l.Aktivitas) == "" {
		missing = append(missing, "aktivitas")
	}
	if strings.TrimSpace(l.Pembelajaran) == "" {
		missing = append(missing, "pembelajaran")
	}
	if strings.TrimSpace(l.Kendala) == "" {
		missing = append(missing, "kendala")
	}
	if len(missing) > 0 {
		return fmt.Errorf("kolom kosong: %s", strings.Join(missing, ", "))
	}
	if n := utf8.RuneCountInString(l.Aktivitas); n < MinAktivitasChars {
		return fmt.Errorf("aktivitas hanya %d karakter, minimal %d", n, MinAktivitasChars)
	}
	return nil
}

const systemPrompt = `Kamu adalah asisten profesional yang bertugas menyusun laporan harian (logbook) peserta Program Pemagangan Nasional Kemnaker RI.

Diberikan catatan singkat kegiatan pengguna, ubah menjadi JSON dengan tepat tiga kunci:
{
  "aktivitas": "Penjelasan detail kegiatan kerja harian. Bahasa baku, profesional, dan minimal 100 karakter.",
  "pembelajaran": "Keterampilan teknis, wawasan bisnis, atau soft skill baru yang dipelajari hari ini.",
  "kendala": "Hambatan atau tantangan yang dihadapi serta solusi penyelesaiannya (jika tidak ada kendala, jelaskan bahwa alur kerja berjalan lancar)."
}

Aturan:
- Gunakan Bahasa Indonesia baku dan formal.
- Jangan mengarang kegiatan yang tidak disebut pengguna; cukup perjelas dan rapikan.
- Balas HANYA dengan objek JSON, tanpa teks lain dan tanpa blok kode markdown.`

func draftUserPrompt(notes string) string {
	return "Catatan kegiatan hari ini:\n" + notes
}

func revisionUserPrompt(notes string, prev Logbook, instruction string) string {
	return fmt.Sprintf(`Catatan kegiatan hari ini:
%s

Draf sebelumnya:
- aktivitas: %s
- pembelajaran: %s
- kendala: %s

Revisi draf tersebut sesuai permintaan berikut: %s`, notes, prev.Aktivitas, prev.Pembelajaran, prev.Kendala, instruction)
}
