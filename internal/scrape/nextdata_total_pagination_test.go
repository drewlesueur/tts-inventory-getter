package scrape

import "testing"

// drivenmotion.com ships no pageCount in __NEXT_DATA__ — only
// inventory.meta.total plus one page of results — so the page count has to be
// derived. Its pager renders no ?page= anchors at all, so without this the walk
// stops at page 1 (25 of 413 vehicles).
func TestExtractNextDataPageURLsFromTotal(t *testing.T) {
	page := func(cur, total, perPage int) string {
		results := ""
		for i := 0; i < perPage; i++ {
			if i > 0 {
				results += ","
			}
			results += `{"vin":"x"}`
		}
		return `<script id="__NEXT_DATA__" type="application/json">{"props":{"pageProps":{` +
			`"page":` + itoa(cur) + `,"inventory":{"meta":{"total":` + itoa(total) + `},"results":[` + results + `]}}}}</script>`
	}

	got := extractNextDataPageURLs("https://www.drivenmotion.com/inventory", page(1, 413, 25))
	if len(got) != 16 { // 413/25 = 17 pages, minus the one already fetched
		t.Fatalf("expected 16 follow-up pages for 413 items at 25/page, got %d", len(got))
	}
	if got[0] != "https://www.drivenmotion.com/inventory?page=2" {
		t.Errorf("first page url = %q", got[0])
	}
	if got[len(got)-1] != "https://www.drivenmotion.com/inventory?page=17" {
		t.Errorf("last page url = %q", got[len(got)-1])
	}

	// Already on a later page: only the pages after it are emitted.
	got = extractNextDataPageURLs("https://www.drivenmotion.com/inventory?page=16", page(16, 413, 25))
	if len(got) != 1 || got[0] != "https://www.drivenmotion.com/inventory?page=17" {
		t.Errorf("from page 16 expected just page 17, got %v", got)
	}

	// A single page of results must not synthesise anything.
	if got = extractNextDataPageURLs("https://www.drivenmotion.com/inventory", page(1, 20, 25)); len(got) != 0 {
		t.Errorf("expected no pages when total fits on one page, got %v", got)
	}

	// An explicit pageCount still wins over the derived value.
	withCount := `<script id="__NEXT_DATA__" type="application/json">{"props":{"pageProps":{"page":1,"pageCount":3,` +
		`"inventory":{"meta":{"total":413},"results":[{"vin":"x"}]}}}}</script>`
	if got = extractNextDataPageURLs("https://example.com/inventory", withCount); len(got) != 2 {
		t.Errorf("explicit pageCount=3 should yield 2 follow-ups, got %d", len(got))
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}
