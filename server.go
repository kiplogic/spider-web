package main

// Spider Web — server HTTP.
// - Menyajikan UI web (embed di binary, 1 file).
// - POST   /api/jobs                 -> mulai crawl baru, balikan {jobId}
// - GET    /api/jobs/{id}/events     -> stream progress via Server-Sent Events
// - GET    /api/jobs/{id}            -> status job
// - GET    /api/jobs/{id}/results    -> hasil JSON (articles + errors)
// - POST   /api/jobs/{id}/stop       -> hentikan crawl
// - GET    /api/jobs/{id}/export     -> unduh .xlsx (1 website) atau .zip (multi website)
// - DELETE /api/jobs/{id}            -> hapus job dari memori

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

//go:embed ui
var uiAssets embed.FS

// ---------- Job ----------

type Job struct {
	id       string
	created  time.Time
	opts     CrawlOptions
	mainURLs []string

	cancel context.CancelFunc

	mu       sync.Mutex
	running  bool
	done     bool
	stopped  bool
	articles []ArticleResult
	errs     []ErrorResult
	last     Progress
	subs     map[chan Progress]struct{}
}

func newJob(opts CrawlOptions, mainURLs []string) *Job {
	return &Job{
		id:       newID(),
		created:  time.Now(),
		opts:     opts,
		mainURLs: mainURLs,
		articles: []ArticleResult{},
		errs:     []ErrorResult{},
		subs:     map[chan Progress]struct{}{},
	}
}

func newID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// publish dipanggil dari goroutine crawler (bisa konkuren) — non-blocking.
func (j *Job) publish(p Progress) {
	j.mu.Lock()
	j.last = p
	for ch := range j.subs {
		select {
		case ch <- p:
		default:
		}
	}
	j.mu.Unlock()
}

func (j *Job) subscribe() chan Progress {
	ch := make(chan Progress, 256)
	j.mu.Lock()
	j.subs[ch] = struct{}{}
	last := j.last
	finished := j.done
	j.mu.Unlock()
	// Kirim status terakhir agar tab SSE yang baru dibuka langsung sinkron.
	if last.Phase != "" {
		select {
		case ch <- last:
		default:
		}
	}
	if finished {
		select {
		case ch <- Progress{Phase: "finished"}:
		default:
		}
	}
	return ch
}

func (j *Job) unsubscribe(ch chan Progress) {
	j.mu.Lock()
	delete(j.subs, ch)
	j.mu.Unlock()
	close(ch)
}

func (j *Job) setResult(out CrawlOutput) {
	j.mu.Lock()
	j.articles = out.Articles
	j.errs = out.Errors
	j.stopped = out.Stopped
	j.running = false
	j.done = true
	j.mu.Unlock()
	// Sentinel penutup stream SSE (dikirim SETELAH hasil tersimpan).
	j.publish(Progress{Phase: "finished"})
}

func (j *Job) snapshot() (articles []ArticleResult, errs []ErrorResult, stopped, done, running bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.articles, j.errs, j.stopped, j.done, j.running
}

// ---------- Manager ----------

type Manager struct {
	mu   sync.Mutex
	jobs map[string]*Job
}

func newManager() *Manager {
	m := &Manager{jobs: map[string]*Job{}}
	go m.sweep()
	return m
}

func (m *Manager) add(j *Job) {
	m.mu.Lock()
	m.jobs[j.id] = j
	m.mu.Unlock()
}

func (m *Manager) get(id string) *Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.jobs[id]
}

func (m *Manager) remove(id string) {
	m.mu.Lock()
	j := m.jobs[id]
	delete(m.jobs, id)
	m.mu.Unlock()
	if j != nil && j.cancel != nil {
		j.cancel()
	}
}

// sweep: hapus job yang sudah selesai > 6 jam agar memori tidak menumpuk.
func (m *Manager) sweep() {
	for range time.Tick(30 * time.Minute) {
		now := time.Now()
		m.mu.Lock()
		for id, j := range m.jobs {
			j.mu.Lock()
			done := j.done
			created := j.created
			j.mu.Unlock()
			if done && now.Sub(created) > 6*time.Hour {
				delete(m.jobs, id)
			}
		}
		m.mu.Unlock()
	}
}

// ---------- HTTP handlers ----------

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (m *Manager) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MB
	var opts CrawlOptions
	if err := json.NewDecoder(r.Body).Decode(&opts); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "body JSON tidak valid"})
		return
	}
	mainURLs := normalizeURLList(opts.MainURLs)
	if len(mainURLs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "masukkan minimal satu URL website"})
		return
	}

	job := newJob(opts, mainURLs)
	ctx, cancel := context.WithCancel(context.Background())
	job.cancel = cancel
	job.running = true
	m.add(job)

	go func() {
		defer cancel()
		log.Printf("job %s mulai: %d URL", job.id, len(mainURLs))
		c := newCrawler(opts, job.publish)
		out := c.run(ctx, mainURLs)
		job.setResult(out)
		log.Printf("job %s selesai: %d artikel, %d error (stopped=%v)", job.id, len(out.Articles), len(out.Errors), out.Stopped)
	}()

	writeJSON(w, http.StatusCreated, map[string]string{"jobId": job.id})
}

func (m *Manager) handleEvents(w http.ResponseWriter, r *http.Request) {
	job := m.get(r.PathValue("id"))
	if job == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "job tidak ditemukan"})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming tidak didukung"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := job.subscribe()
	defer job.unsubscribe(ch)

	send := func(event string, v interface{}) error {
		data, err := json.Marshal(v)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		if err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case p, ok := <-ch:
			if !ok {
				return
			}
			if p.Phase == "finished" {
				_, _, stopped, _, _ := job.snapshot()
				_ = send("done", map[string]interface{}{"stopped": stopped})
				return
			}
			if err := send("progress", p); err != nil {
				return
			}
		}
	}
}

func (m *Manager) handleStatus(w http.ResponseWriter, r *http.Request) {
	job := m.get(r.PathValue("id"))
	if job == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "job tidak ditemukan"})
		return
	}
	articles, errs, stopped, done, running := job.snapshot()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"jobId":    job.id,
		"running":  running,
		"done":     done,
		"stopped":  stopped,
		"articles": len(articles),
		"errors":   len(errs),
	})
}

func (m *Manager) handleResults(w http.ResponseWriter, r *http.Request) {
	job := m.get(r.PathValue("id"))
	if job == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "job tidak ditemukan"})
		return
	}
	articles, errs, stopped, done, _ := job.snapshot()
	if articles == nil {
		articles = []ArticleResult{}
	}
	if errs == nil {
		errs = []ErrorResult{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"articles": articles,
		"errors":   errs,
		"stopped":  stopped,
		"done":     done,
	})
}

func (m *Manager) handleStop(w http.ResponseWriter, r *http.Request) {
	job := m.get(r.PathValue("id"))
	if job == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "job tidak ditemukan"})
		return
	}
	if job.cancel != nil {
		job.cancel()
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (m *Manager) handleDelete(w http.ResponseWriter, r *http.Request) {
	m.remove(r.PathValue("id"))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleExport: 1 website -> 1 file .xlsx langsung;
// multi website -> 1 file .zip berisi satu .xlsx per website.
func (m *Manager) handleExport(w http.ResponseWriter, r *http.Request) {
	job := m.get(r.PathValue("id"))
	if job == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "job tidak ditemukan"})
		return
	}
	articles, errs, _, done, _ := job.snapshot()
	if !done {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "crawl belum selesai"})
		return
	}

	groupArt := map[string][]ArticleResult{}
	groupErr := map[string][]ErrorResult{}
	for _, a := range articles {
		groupArt[a.MainURL] = append(groupArt[a.MainURL], a)
	}
	for _, e := range errs {
		groupErr[e.MainURL] = append(groupErr[e.MainURL], e)
	}
	var mains []string
	seen := map[string]bool{}
	for _, u := range job.mainURLs {
		if len(groupArt[u]) > 0 || len(groupErr[u]) > 0 {
			mains = append(mains, u)
			seen[u] = true
		}
	}
	for u := range groupArt {
		if !seen[u] {
			mains = append(mains, u)
			seen[u] = true
		}
	}
	if len(mains) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tidak ada hasil untuk disimpan"})
		return
	}

	ts := job.created.Format("20060102-150405")
	fileName := func(mainURL string) string {
		name := hostOf(mainURL)
		if name == "" {
			name = "crawl"
		}
		return name + "_" + ts + ".xlsx"
	}

	if len(mains) == 1 {
		name := fileName(mains[0])
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
		if err := writeExcel(w, groupArt[mains[0]], groupErr[mains[0]]); err != nil {
			log.Printf("export gagal: %v", err)
		}
		return
	}

	zipName := "spider_" + ts + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, zipName))
	zw := zip.NewWriter(w)
	for _, mainURL := range mains {
		fw, err := zw.Create(fileName(mainURL))
		if err != nil {
			log.Printf("zip entry gagal: %v", err)
			continue
		}
		if err := writeExcel(fw, groupArt[mainURL], groupErr[mainURL]); err != nil {
			log.Printf("export %s gagal: %v", mainURL, err)
		}
	}
	if err := zw.Close(); err != nil {
		log.Printf("zip close gagal: %v", err)
	}
}

// ---------- util ----------

func normalizeURLList(items []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		s = strings.TrimSuffix(s, ",")
		if s == "" {
			return
		}
		if !strings.Contains(s, "://") {
			s = "https://" + s
		}
		if seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	for _, it := range items {
		for _, line := range strings.Split(it, "\n") {
			add(line)
		}
	}
	return out
}

func openBrowser(url string) {
	if runtime.GOOS != "windows" {
		return // di Linux/Mac cukup tampilkan URL di terminal
	}
	_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

// ---------- main ----------

func main() {
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "8080"
	}

	m := newManager()
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/jobs", m.handleCreateJob)
	mux.HandleFunc("GET /api/jobs/{id}/events", m.handleEvents)
	mux.HandleFunc("GET /api/jobs/{id}", m.handleStatus)
	mux.HandleFunc("GET /api/jobs/{id}/results", m.handleResults)
	mux.HandleFunc("POST /api/jobs/{id}/stop", m.handleStop)
	mux.HandleFunc("DELETE /api/jobs/{id}", m.handleDelete)
	mux.HandleFunc("GET /api/jobs/{id}/export", m.handleExport)

	uifs, err := fs.Sub(uiAssets, "ui")
	if err != nil {
		log.Fatal(err)
	}
	mux.Handle("/", http.FileServer(http.FS(uifs)))

	url := "http://localhost:" + port
	log.Printf("Spider Web berjalan di %s", url)
	openBrowser(url)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
