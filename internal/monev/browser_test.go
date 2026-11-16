package monev

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"auto-attendance/internal/ai"
)

// formPage meniru form Monev: label for=id, nilai dibaca dari event input (seperti v-model),
// tombol nonaktif sampai tiap kolom >= 100 karakter dan pernyataan dicentang.
const formPage = `<!doctype html><html><body>
<label for="a">Uraian aktivitas</label><textarea id="a">draf lama</textarea>
<label for="l">Pembelajaran yang diperoleh</label><textarea id="l"></textarea>
<label for="k">Kendala yang dialami</label><textarea id="k"></textarea>
<input type="checkbox" id="c"><label for="c">Saya menyatakan telah meninjau dan memastikan isian laporan ini sudah benar</label>
<button id="s" disabled>Simpan dan Kirim</button>
<div id="out"></div>
<script>
const model = {a: "", l: "", k: "", c: false};
for (const id of ["a", "l", "k"]) document.getElementById(id).addEventListener("input", e => { model[id] = e.target.value; refresh(); });
document.getElementById("c").addEventListener("change", e => { model.c = e.target.checked; refresh(); });
function refresh() { document.getElementById("s").disabled = !(model.a.length >= 100 && model.l.length >= 100 && model.k.length >= 100 && model.c); }
document.getElementById("s").addEventListener("click", () => {
	window.sent = {...model};
	setTimeout(() => document.getElementById("out").textContent = "Kehadiran dan laporan harian berhasil dikirim.", 300);
});
</script></body></html>`

func newChrome(t *testing.T) context.Context {
	t.Helper()
	if _, err := exec.LookPath("google-chrome"); err != nil {
		if _, err := exec.LookPath("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"); err != nil {
			t.Skip("Chrome tidak terpasang")
		}
	}
	actx, acancel := chromedp.NewExecAllocator(context.Background(), chromedp.DefaultExecAllocatorOptions[:]...)
	ctx, cancel := chromedp.NewContext(actx)
	t.Cleanup(func() { cancel(); acancel() })
	if err := chromedp.Do(ctx); err != nil {
		t.Skipf("Chrome tidak bisa dijalankan: %v", err)
	}
	return ctx
}

func TestFillAndSubmitAgainstFakeForm(t *testing.T) {
	ctx := newChrome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(formPage))
	}))
	defer srv.Close()

	if err := chromedp.Do(ctx, chromedp.Navigate(srv.URL)); err != nil {
		t.Fatal(err)
	}
	lb := ai.Logbook{
		Aktivitas:    strings.Repeat("a", 120) + "\nbaris \"kedua\"",
		Pembelajaran: strings.Repeat("p", 100),
		Kendala:      strings.Repeat("k", 100),
	}
	res, err := fillAndSubmit(ctx, lb)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Screenshot) == 0 || !strings.Contains(res.Message, "berhasil") {
		t.Fatalf("hasil tidak lengkap: %q, screenshot %d byte", res.Message, len(res.Screenshot))
	}

	sent, err := chromedp.Run(ctx, chromedp.Evaluate[map[string]any](`window.sent`))
	if err != nil {
		t.Fatal(err)
	}
	if sent["a"] != lb.Aktivitas || sent["l"] != lb.Pembelajaran || sent["k"] != lb.Kendala || sent["c"] != true {
		t.Fatalf("model form tidak sesuai draf (draf lama harus tertimpa): %v", sent)
	}
}

// waitFor harus tetap jalan walau halaman berpindah (chromedp.Poll gagal di kasus ini).
func TestWaitForSurvivesNavigation(t *testing.T) {
	ctx := newChrome(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/start", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<script>setTimeout(() => location.href = "/done", 700)</script>`))
	})
	mux.HandleFunc("/done", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<p id="done">ok</p>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if err := chromedp.Do(ctx, chromedp.Navigate(srv.URL+"/start")); err != nil {
		t.Fatal(err)
	}
	got, err := waitFor(ctx, `document.getElementById("done") ? "done" : ""`, 10*time.Second)
	if err != nil || got != "done" {
		t.Fatalf("got %q, err %v", got, err)
	}
}

func TestReportingDisabledShowsMonevNotice(t *testing.T) {
	ctx := newChrome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<div role="alert"><b>Pengisian laporan dinonaktifkan</b>
			<p>Status magang berakhir karena pengunduran diri.</p></div>`))
	}))
	defer srv.Close()

	if err := chromedp.Do(ctx, chromedp.Navigate(srv.URL)); err != nil {
		t.Fatal(err)
	}
	err := reportingDisabled(ctx)
	if err == nil || !strings.Contains(err.Error(), "Status magang berakhir") {
		t.Fatalf("alasan dari Monev harus diteruskan: %v", err)
	}

	if err := chromedp.Do(ctx, chromedp.Navigate(srv.URL+"/x"), chromedp.Evaluate[chromedp.Void](`document.body.innerHTML = "<label>Uraian aktivitas</label>"`)); err != nil {
		t.Fatal(err)
	}
	if err := reportingDisabled(ctx); err != nil {
		t.Fatalf("form normal tidak boleh dianggap nonaktif: %v", err)
	}
}

func TestSubmitRejectsPastDraft(t *testing.T) {
	b := &Browser{Email: "x", Password: "y", Location: time.UTC}
	_, err := b.Submit(context.Background(), time.Now().Add(-48*time.Hour), ai.Logbook{})
	if err == nil || !strings.Contains(err.Error(), "hanya menerima kehadiran hari ini") {
		t.Fatalf("draf kemarin seharusnya ditolak sebelum membuka browser: %v", err)
	}
}
