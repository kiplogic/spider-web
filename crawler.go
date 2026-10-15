package main

// Semua logika crawler ada di file ini:
// - fetch: HTTP client + retry
// - links: klasifikasi link (artikel vs pagination vs abaikan)
// - title: ekstraksi & pembersihan judul
// - run/discover/fetchTitles: orkestrasi crawl

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// ---------- HTTP fetch (retry 3x, UA browser, timeout) ----------

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 25 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   16,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 20 * time.Second,
		},
	}
}

func (c *crawler) fetch(ctx context.Context, rawURL string) (*goquery.Document, error) {
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if attempt > 1 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt-1) * 600 * time.Millisecond):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")

		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		ct := strings.ToLower(resp.Header.Get("Content-Type"))
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 2048))
			resp.Body.Close()
			lastErr = fmt.Errorf("http %d (retry)", resp.StatusCode)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 2048))
			resp.Body.Close()
			return nil, fmt.Errorf("http %d", resp.StatusCode)
		}
		if ct != "" && !strings.Contains(ct, "html") && !strings.Contains(ct, "xml") {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 2048))
			resp.Body.Close()
			return nil, fmt.Errorf("content-type bukan HTML (%s)", ct)
		}
		doc, err := goquery.NewDocumentFromReader(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		return doc, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("fetch failed")
	}
	return nil, lastErr
}

// ---------- Klasifikasi link ----------

const (
	linkSkip = iota
	linkPagination
	linkArticle
)

var pagePathRe = regexp.MustCompile(`/(?:page|paged)/(\d+)/?$`)

var assetExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true,
	".svg": true, ".ico": true, ".bmp": true, ".css": true, ".js": true,
	".zip": true, ".rar": true, ".7z": true, ".gz": true, ".pdf": true,
	".doc": true, ".docx": true, ".xls": true, ".xlsx": true, ".ppt": true,
	".pptx": true, ".mp3": true, ".mp4": true, ".avi": true, ".mkv": true,
	".mov": true, ".wav": true, ".woff": true, ".woff2": true, ".ttf": true,
	".eot": true, ".otf": true, ".xml": true, ".xsl": true, ".xslt": true,
	".json": true, ".txt": true, ".csv": true, ".dmg": true, ".exe": true, ".apk": true,
}

var skipSubstrings = []string{
	"/wp-content/", "/wp-admin/", "/wp-json/", "xmlrpc.php", "/cdn-cgi/",
	"replytocom=", "?share=", "utm_", "/wp-comments",
}

var skipSegments = map[string]bool{
	"feed": true, "comments": true, "comment": true, "author": true,
	"tag": true, "tags": true, "category": true, "categories": true,
	"amp": true, "search": true, "archive": true, "archives": true,
	"cart": true, "checkout": true, "shop": true, "store": true,
	"product-category": true, "product": true, "downloads": true,
}

var nonArticleSlugs = map[string]bool{
	"about": true, "about-us": true, "about-me": true,
	"contact": true, "contact-us": true, "contact-me": true,
	"privacy-policy": true, "privacy": true, "terms": true,
	"terms-and-conditions": true, "terms-of-service": true, "terms-of-use": true,
	"disclaimer": true, "dmca": true, "faq": true, "faqs": true,
	"home": true, "homepage": true, "sample-page": true,
	"my-account": true, "wishlist": true, "login": true, "register": true,
	"signin": true, "signup": true, "sitemap": true, "sitemap-index": true,
	"sitemap.xml": true, "404": true, "advertise": true, "advertising": true,
	"press": true, "careers": true, "jobs": true,
}

func normalizeHost(h string) string {
	h = strings.ToLower(h)
	h = strings.TrimPrefix(h, "www.")
	return strings.TrimSuffix(h, ".")
}

func canonicalKey(u *url.URL) string {
	p := u.Path
	if p == "" {
		p = "/"
	}
	if p != "/" && path.Ext(p) == "" && !strings.HasSuffix(p, "/") {
		p += "/"
	}
	k := normalizeHost(u.Host) + p
	if u.RawQuery != "" {
		k += "?" + u.RawQuery
	}
	return k
}

func classifyLink(base *url.URL, href string, sel *goquery.Selection) (int, *url.URL) {
	href = strings.TrimSpace(href)
	if href == "" || href == "#" {
		return linkSkip, nil
	}
	low := strings.ToLower(href)
	for _, p := range []string{"mailto:", "tel:", "javascript:", "data:", "sms:", "#"} {
		if strings.HasPrefix(low, p) {
			return linkSkip, nil
		}
	}
	u, err := base.Parse(href)
	if err != nil {
		return linkSkip, nil
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return linkSkip, nil
	}
	if u.Host == "" || normalizeHost(u.Host) != normalizeHost(base.Host) {
		return linkSkip, nil
	}
	u.Fragment = ""
	if u.Path == "" {
		u.Path = "/"
	}

	if isPaginationLink(u, sel) {
		if n := paginationNumber(u); n == 1 {
			return linkSkip, nil
		}
		return linkPagination, u
	}

	lowPath := strings.ToLower(u.Path)
	if lowPath == "/" {
		return linkSkip, nil
	}
	if ext := path.Ext(lowPath); ext != "" && assetExtensions[ext] {
		return linkSkip, nil
	}
	lowAll := lowPath + "?" + strings.ToLower(u.RawQuery)
	for _, s := range skipSubstrings {
		if strings.Contains(lowAll, s) {
			return linkSkip, nil
		}
	}
	segs := pathSegments(lowPath)
	for _, sg := range segs {
		if skipSegments[sg] {
			return linkSkip, nil
		}
	}
	if len(segs) == 1 && nonArticleSlugs[segs[0]] {
		return linkSkip, nil
	}
	if last := segs[len(segs)-1]; nonArticleSlugs[last] {
		return linkSkip, nil
	}
	return linkArticle, u
}

func isPaginationLink(u *url.URL, sel *goquery.Selection) bool {
	if rel, ok := sel.Attr("rel"); ok {
		for _, r := range strings.Fields(strings.ToLower(rel)) {
			if r == "next" {
				return true
			}
		}
	}
	if pagePathRe.MatchString(strings.ToLower(u.Path)) {
		return true
	}
	q := u.Query()
	for _, k := range []string{"page", "paged", "pg"} {
		if v := q.Get(k); v != "" {
			if _, err := strconv.Atoi(v); err == nil {
				return true
			}
		}
	}
	if sel.HasClass("page-numbers") || sel.HasClass("next") || sel.HasClass("prev") || sel.HasClass("pager__item") {
		return true
	}
	if sel.Closest("[class*='pagination'],[class*='pager'],[class*='page-numbers'],[class*='nav-links'],[id*='pagination'],[id*='nav-links'],nav.navigation,nav.post-navigation").Length() > 0 {
		return true
	}
	return false
}

func paginationNumber(u *url.URL) int {
	if m := pagePathRe.FindStringSubmatch(strings.ToLower(u.Path)); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	q := u.Query()
	for _, k := range []string{"page", "paged", "pg"} {
		if v := q.Get(k); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				return n
			}
		}
	}
	return 0
}

func pathSegments(p string) []string {
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// ---------- Ekstraksi & pembersihan judul ----------

func extractTitle(doc *goquery.Document, pageURL string) string {
	og := stripTitleSuffix(cleanText(doc.Find(`meta[property="og:title"]`).First().AttrOr("content", "")), pageURL, false)
	tt := stripTitleSuffix(cleanText(doc.Find("title").First().Text()), pageURL, true)
	h1 := stripTitleSuffix(cleanText(doc.Find("h1").First().Text()), pageURL, false)

	best := og
	if len([]rune(tt)) > len([]rune(best)) {
		best = tt
	}
	if len([]rune(h1)) > len([]rune(best)) {
		best = h1
	}
	return strings.TrimSpace(best)
}

func cleanText(s string) string {
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}

func stripTitleSuffix(s string, pageURL string, generic bool) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	tokens := hostTokens(pageURL)
	seps := []string{" | ", " – ", " — ", " :: ", " - "}
	changed := true
	for changed {
		changed = false
		for _, sep := range seps {
			i := strings.LastIndex(s, sep)
			if i <= 0 {
				continue
			}
			suffix := strings.TrimSpace(s[i+len(sep):])
			if suffix == "" {
				continue
			}
			ls := strings.ToLower(suffix)
			match := false
			for _, tk := range tokens {
				if tk != "" && strings.Contains(ls, tk) {
					match = true
					break
				}
			}
			if match {
				s = strings.TrimSpace(s[:i])
				changed = true
			}
		}
	}
	if generic {
		for _, sep := range []string{" | ", " – ", " — ", " :: "} {
			if i := strings.Index(s, sep); i > 0 {
				s = strings.TrimSpace(s[:i])
			}
		}
		if i := strings.LastIndex(s, " - "); i > 0 {
			suffix := strings.TrimSpace(s[i+3:])
			if suffix != "" && len(suffix) <= 40 {
				s = strings.TrimSpace(s[:i])
			}
		}
	}
	return s
}

func hostTokens(pageURL string) []string {
	u, err := url.Parse(pageURL)
	if err != nil {
		return nil
	}
	h := strings.ToLower(u.Hostname())
	h = strings.TrimPrefix(h, "www.")
	tokens := []string{h}
	labels := strings.Split(h, ".")
	if len(labels) > 0 && len(labels[0]) >= 3 {
		tokens = append(tokens, labels[0])
	}
	return tokens
}

// ---------- Article date extraction ----------

var dateLayouts = []string{
	"2006-01-02T15:04:05Z07:00",
	"2006-01-02T15:04:05.999999999Z07:00",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02",
	"January 2, 2006",
	"Jan 2, 2006",
	"2 January 2006",
	"2 Jan 2006",
}

func normalizeDate(raw string) string {
	raw = strings.TrimSpace(raw)
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.Format("2006-01-02")
		}
	}
	return ""
}

var textDatePatterns = []struct {
	re     *regexp.Regexp
	layout string
}{
	{regexp.MustCompile(`([A-Za-z]+\.? \d{1,2}, \d{4})`), "January 2, 2006"},
	{regexp.MustCompile(`(\d{1,2} [A-Za-z]+\.? \d{4})`), "2 January 2006"},
	{regexp.MustCompile(`(\d{4}-\d{1,2}-\d{1,2})`), "2006-01-02"},
}

// extractDate returns the article publish date formatted as YYYY-MM-DD
// ("" if not found). Priority: meta tags -> JSON-LD -> <time> -> body text.
func extractDate(doc *goquery.Document) string {
	metaSelectors := []string{
		`meta[property="article:published_time"]`,
		`meta[property="og:article:published_time"]`,
		`meta[itemprop="datePublished"]`,
		`meta[name="datePublished"]`,
		`meta[name="date"]`,
		`meta[name="publish-date"]`,
		`meta[name="DC.date"]`,
	}
	for _, sel := range metaSelectors {
		if v := strings.TrimSpace(doc.Find(sel).First().AttrOr("content", "")); v != "" {
			if d := normalizeDate(v); d != "" {
				return d
			}
		}
	}

	if d := dateFromJSONLD(doc); d != "" {
		return d
	}

	if v, ok := doc.Find("time[datetime]").First().Attr("datetime"); ok {
		if d := normalizeDate(v); d != "" {
			return d
		}
	}
	if txt := strings.TrimSpace(doc.Find("time").First().Text()); txt != "" {
		if d := normalizeDate(txt); d != "" {
			return d
		}
	}

	bodyHtml, _ := doc.Find("body").Html()
	tagRe := regexp.MustCompile(`(?s)<[^>]*>`)
	clean := strings.Join(strings.Fields(html.UnescapeString(tagRe.ReplaceAllString(bodyHtml, " "))), " ")
	if len(clean) > 6000 {
		clean = clean[:6000]
	}
	for _, p := range textDatePatterns {
		for _, m := range p.re.FindAllString(clean, 20) {
			if d := normalizeDate(m); d != "" {
				return d
			}
		}
	}
	return ""
}

func dateFromJSONLD(doc *goquery.Document) string {
	var result string
	doc.Find(`script[type="application/ld+json"]`).Each(func(_ int, s *goquery.Selection) {
		if result != "" {
			return
		}
		var parsed interface{}
		if err := json.Unmarshal([]byte(s.Text()), &parsed); err != nil {
			return
		}
		if v, ok := findJSONKey(parsed, "datePublished"); ok {
			result = v
		}
	})
	if result == "" {
		return ""
	}
	return normalizeDate(result)
}

func findJSONKey(v interface{}, key string) (string, bool) {
	switch t := v.(type) {
	case map[string]interface{}:
		if s, ok := t[key].(string); ok && strings.TrimSpace(s) != "" {
			return s, true
		}
		for _, vv := range t {
			if s, ok := findJSONKey(vv, key); ok {
				return s, true
			}
		}
	case []interface{}:
		for _, vv := range t {
			if s, ok := findJSONKey(vv, key); ok {
				return s, true
			}
		}
	}
	return "", false
}

// ---------- Orkestrasi crawl ----------

type crawler struct {
	client *http.Client
	opts   CrawlOptions
	emit   func(Progress)
}

func newCrawler(opts CrawlOptions, emit func(Progress)) *crawler {
	if opts.MaxPages <= 0 {
		opts.MaxPages = 200
	}
	if opts.MaxArticles <= 0 {
		opts.MaxArticles = 3000
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 8
	}
	if opts.DelayMs < 0 {
		opts.DelayMs = 0
	}
	return &crawler{client: newHTTPClient(), opts: opts, emit: emit}
}

func (c *crawler) run(ctx context.Context, mainURLs []string) CrawlOutput {
	out := CrawlOutput{Articles: []ArticleResult{}, Errors: []ErrorResult{}}
	total := len(mainURLs)
	for i, raw := range mainURLs {
		if ctx.Err() != nil {
			out.Stopped = true
			break
		}
		u, err := normalizeMainURL(raw)
		if err != nil {
			out.Errors = append(out.Errors, ErrorResult{MainURL: raw, ArticleURL: raw, Error: "invalid main URL: " + err.Error()})
			c.emit(Progress{Phase: "discover", MainIndex: i + 1, MainTotal: total, Log: fmt.Sprintf("[%d/%d] URL tidak valid: %s (%v)", i+1, total, raw, err)})
			continue
		}
		mainStr := u.String()
		c.emit(Progress{Phase: "discover", MainURL: mainStr, MainIndex: i + 1, MainTotal: total, Log: fmt.Sprintf("[%d/%d] Mulai: %s", i+1, total, mainStr)})

		articles, pagesDone := c.discover(ctx, u, i+1, total)
		if ctx.Err() != nil {
			out.Stopped = true
		}
		if len(articles) == 0 {
			c.emit(Progress{Phase: "discover", MainURL: mainStr, MainIndex: i + 1, MainTotal: total, PagesDone: pagesDone, Log: fmt.Sprintf("[%d/%d] Tidak ada artikel ditemukan di %s", i+1, total, mainStr)})
			continue
		}
		c.emit(Progress{Phase: "titles", MainURL: mainStr, MainIndex: i + 1, MainTotal: total, PagesDone: pagesDone, ArticlesFound: len(articles), TotalTitles: len(articles), Log: fmt.Sprintf("[%d/%d] Ditemukan %d artikel unik, mengambil judul...", i+1, total, len(articles))})

		arts, errs := c.fetchTitles(ctx, mainStr, pagesDone, articles, i+1, total)
		out.Articles = append(out.Articles, arts...)
		out.Errors = append(out.Errors, errs...)
	}
	c.emit(Progress{Phase: "done", Log: "Semua URL utama selesai"})
	return out
}

func (c *crawler) waitDelay(ctx context.Context) {
	if c.opts.DelayMs <= 0 {
		return
	}
	t := time.NewTimer(time.Duration(c.opts.DelayMs) * time.Millisecond)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// paginationKey returns the numeric page number of a pagination URL for
// queue ordering. Unnumbered pagination links get a huge key so they are
// processed last (real numbered pages take priority).
func paginationKey(u *url.URL) int {
	if n := paginationNumber(u); n > 0 {
		return n
	}
	return 1 << 30
}

func (c *crawler) discover(ctx context.Context, main *url.URL, idx, total int) ([]string, int) {
	queue := []*url.URL{main}
	queued := map[string]bool{canonicalKey(main): true}
	articleSet := map[string]bool{}
	articles := []string{}
	pagesDone := 0

	// popNext always takes the pagination link with the SMALLEST page number,
	// so listing pages are fetched in sequential order (page 2, 3, 4, ...).
	popNext := func() *url.URL {
		if len(queue) == 0 {
			return nil
		}
		best := 0
		bestKey := paginationKey(queue[0])
		for i := 1; i < len(queue); i++ {
			if k := paginationKey(queue[i]); k < bestKey {
				best = i
				bestKey = k
			}
		}
		u := queue[best]
		queue = append(queue[:best], queue[best+1:]...)
		return u
	}

	for len(queue) > 0 && pagesDone < c.opts.MaxPages && len(articles) < c.opts.MaxArticles && ctx.Err() == nil {
		pageURL := popNext()

		c.waitDelay(ctx)
		doc, err := c.fetch(ctx, pageURL.String())
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			c.emit(Progress{Phase: "discover", MainURL: main.String(), MainIndex: idx, MainTotal: total, PagesDone: pagesDone, ArticlesFound: len(articles), Log: fmt.Sprintf("  ! gagal mengambil %s: %v", pageURL.String(), err)})
			continue
		}
		pagesDone++
		newPages := 0

		doc.Find("a[href]").Each(func(_ int, sel *goquery.Selection) {
			href, ok := sel.Attr("href")
			if !ok {
				return
			}
			kind, u := classifyLink(pageURL, href, sel)
			switch kind {
			case linkPagination:
				if u == nil {
					return
				}
				k := canonicalKey(u)
				if !queued[k] {
					queued[k] = true
					queue = append(queue, u)
					newPages++
				}
			case linkArticle:
				k := canonicalKey(u)
				if !articleSet[k] {
					articleSet[k] = true
					articles = append(articles, u.String())
				}
			}
		})

		c.emit(Progress{Phase: "discover", MainURL: main.String(), MainIndex: idx, MainTotal: total, PagesDone: pagesDone, ArticlesFound: len(articles), Log: fmt.Sprintf("  halaman %d (%s): +%d pagination -> %d artikel total", pagesDone, pageURL.String(), newPages, len(articles))})
	}
	stoppedByCap := ctx.Err() == nil && len(articles) >= c.opts.MaxArticles
	if len(articles) > c.opts.MaxArticles {
		articles = articles[:c.opts.MaxArticles]
	}
	if stoppedByCap {
		c.emit(Progress{Phase: "discover", MainURL: main.String(), MainIndex: idx, MainTotal: total, PagesDone: pagesDone, ArticlesFound: len(articles), Log: fmt.Sprintf("  ! batas artikel tercapai (%d). Naikkan 'Maks artikel per website', lalu jalankan lagi.", c.opts.MaxArticles)})
	}
	return articles, pagesDone
}

func (c *crawler) fetchTitles(ctx context.Context, mainStr string, pagesDone int, articles []string, idx, total int) ([]ArticleResult, []ErrorResult) {
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		errs []ErrorResult
		done int
	)
	// Results are written by index so the output stays in discovery (page) order.
	arts := make([]ArticleResult, len(articles))
	fetched := make([]bool, len(articles))
	sem := make(chan struct{}, c.opts.Concurrency)

	for i, artURL := range articles {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(pos int, artURL string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			c.waitDelay(ctx)

			var title, pubDate string
			var ferr error
			doc, err := c.fetch(ctx, artURL)
			if err != nil {
				ferr = err
			} else {
				title = extractTitle(doc, artURL)
				pubDate = extractDate(doc)
				if title == "" {
					ferr = fmt.Errorf("title not found on page")
				}
			}

			mu.Lock()
			done++
			if ferr != nil {
				errs = append(errs, ErrorResult{MainURL: mainStr, ArticleURL: artURL, Error: ferr.Error()})
			} else {
				arts[pos] = ArticleResult{MainURL: mainStr, ArticleURL: artURL, Date: pubDate, Title: title}
				fetched[pos] = true
			}
			c.emit(Progress{Phase: "titles", MainURL: mainStr, MainIndex: idx, MainTotal: total, PagesDone: pagesDone, ArticlesFound: len(articles), TitlesDone: done, TotalTitles: len(articles)})
			mu.Unlock()
		}(i, artURL)
	}
	wg.Wait()

	out := make([]ArticleResult, 0, len(arts))
	for i := range arts {
		if fetched[i] {
			out = append(out, arts[i])
		}
	}
	return out, errs
}

func normalizeMainURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("URL is empty")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Host == "" {
		return nil, fmt.Errorf("host not found")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		u.Scheme = "https"
	}
	return u, nil
}
