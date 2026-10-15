package main

// Tipe data bersama (dipakai crawler.go, server.go, excel.go)

type CrawlOptions struct {
	MainURLs    []string `json:"mainUrls"`
	MaxPages    int      `json:"maxPages"`
	MaxArticles int      `json:"maxArticles"`
	Concurrency int      `json:"concurrency"`
	DelayMs     int      `json:"delayMs"`
}

type ArticleResult struct {
	MainURL    string `json:"mainUrl"`
	ArticleURL string `json:"articleUrl"`
	Date       string `json:"date"`
	Title      string `json:"title"`
}

type ErrorResult struct {
	MainURL    string `json:"mainUrl"`
	ArticleURL string `json:"articleUrl"`
	Error      string `json:"error"`
}

type CrawlOutput struct {
	Articles []ArticleResult `json:"articles"`
	Errors   []ErrorResult   `json:"errors"`
	Stopped  bool            `json:"stopped"`
}

type Progress struct {
	Phase         string `json:"phase"`
	MainURL       string `json:"mainUrl"`
	MainIndex     int    `json:"mainIndex"`
	MainTotal     int    `json:"mainTotal"`
	PagesDone     int    `json:"pagesDone"`
	ArticlesFound int    `json:"articlesFound"`
	TitlesDone    int    `json:"titlesDone"`
	TotalTitles   int    `json:"totalTitles"`
	Log           string `json:"log"`
}
