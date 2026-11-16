package ai

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// MinFieldChars adalah batas minimal karakter setiap kolom logbook di form Monev.
const MinFieldChars = 100

// Logbook adalah tiga kolom laporan harian Monev MagangHub.
type Logbook struct {
	Aktivitas    string `json:"aktivitas"`
	Pembelajaran string `json:"pembelajaran"`
	Kendala      string `json:"kendala"`
}

func (l Logbook) Validate() error {
	var problems []string
	for _, f := range []struct{ name, value string }{
		{"aktivitas", l.Aktivitas},
		{"pembelajaran", l.Pembelajaran},
		{"kendala", l.Kendala},
	} {
		if n := utf8.RuneCountInString(strings.TrimSpace(f.value)); n < MinFieldChars {
			problems = append(problems, fmt.Sprintf("%s %d karakter", f.name, n))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("setiap kolom minimal %d karakter (%s)", MinFieldChars, strings.Join(problems, ", "))
	}
	return nil
}

const systemPrompt = `Kamu adalah asisten profesional yang bertugas menyusun laporan harian (logbook) peserta Program Pemagangan Nasional Kemnaker RI.

Diberikan catatan singkat kegiatan pengguna, ubah menjadi JSON dengan tepat tiga kunci. SETIAP kolom wajib minimal 100 karakter:
{
  "aktivitas": "Penjelasan detail kegiatan kerja harian. Bahasa baku dan profesional.",
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
