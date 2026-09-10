package scrape

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// Team Velocity (secureoffersites.com) dealer sites split inventory across
// /inventory/new and /inventory/used SRPs; the bare /inventory path is a
// landing page with only a few featured vehicles. When a scrape request
// targets that landing page, walk both SRPs (server-rendered, ?page=N) and
// return their cards merged so the generic extractor sees the full inventory.
const teamVelocityCardSelector = "[class*='inventory-car-parent-box']"

func (s Service) expandTeamVelocityInventory(ctx context.Context, pageURL, renderedHTML string) string {
	if !strings.Contains(renderedHTML, "secureoffersites.com") {
		return renderedHTML
	}
	u, err := url.Parse(pageURL)
	if err != nil || u.Host == "" {
		return renderedHTML
	}
	if !strings.EqualFold(strings.TrimSuffix(u.Path, "/"), "/inventory") {
		return renderedHTML // already a concrete SRP; the generic engine handles it
	}
	base := u.Scheme + "://" + u.Host + "/inventory/"
	var cards strings.Builder
	cards.WriteString(`<section data-teamvelocity-expanded="true">`)
	seen := map[string]bool{}
	total := 0
	first := true
	for _, section := range []string{"new", "used"} {
		for page := 1; page <= 15; page++ {
			if !first {
				// Be polite to the dealer's site — don't hammer pages back-to-back.
				select {
				case <-ctx.Done():
					return renderedHTML
				case <-time.After(400 * time.Millisecond):
				}
			}
			first = false
			srp := base + section
			if page > 1 {
				srp = fmt.Sprintf("%s?page=%d", srp, page)
			}
			h, ferr := s.fetchViaCurl(ctx, srp)
			if ferr != nil {
				break
			}
			doc, derr := goquery.NewDocumentFromReader(strings.NewReader(h))
			if derr != nil {
				break
			}
			added := 0
			doc.Find(teamVelocityCardSelector).Each(func(_ int, sel *goquery.Selection) {
				href, _ := sel.Find("a[href*='/viewdetails/']").First().Attr("href")
				key := strings.ToLower(strings.TrimSpace(href))
				if key == "" || seen[key] {
					return
				}
				outer, oerr := goquery.OuterHtml(sel)
				if oerr != nil {
					return
				}
				seen[key] = true
				cards.WriteString(outer)
				added++
			})
			total += added
			if added == 0 {
				break // past the last page (repeats or empties)
			}
		}
	}
	cards.WriteString(`</section>`)
	if total == 0 {
		return renderedHTML
	}
	return cards.String()
}

// fetchViaCurl routes a fetch through the cookie-aware curl_cffi fetcher when
// available (some Team Velocity hosts 403 plain HTTP clients on TLS
// fingerprint), falling back to the plain fetcher.
func (s Service) fetchViaCurl(ctx context.Context, pageURL string) (string, error) {
	if s.Fetcher == nil {
		return "", fmt.Errorf("no fetcher configured")
	}
	if cf, ok := s.Fetcher.(interface {
		FetchWithCookie(context.Context, string, string) (string, error)
	}); ok {
		if h, err := cf.FetchWithCookie(ctx, pageURL, ""); err == nil {
			return h, nil
		}
		// The Python curl layer can be missing (e.g. cloud); the plain Go
		// fetcher may still pass, so don't give up on the section walk here.
	}
	return s.Fetcher.Fetch(ctx, pageURL)
}
