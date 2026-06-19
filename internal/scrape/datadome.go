package scrape

import "strings"

func isDataDomeChallenge(html string) bool {
	return strings.Contains(html, "captcha-delivery.com") ||
		(strings.Contains(html, "datadome") && strings.Contains(html, "captcha"))
}
