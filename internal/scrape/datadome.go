package scrape

import "strings"

// isDataDomeChallenge detects the DataDome block page. The only reliable marker
// is the captcha-delivery.com challenge host — a real inventory page can legitimately
// mention "datadome" (tag loader) and "captcha" without being blocked, so we must
// not use those words as a heuristic.
func isDataDomeChallenge(html string) bool {
	if strings.Contains(html, "captcha-delivery.com") {
		return true
	}
	// The block page is small and contains this exact DataDome bootstrap marker.
	if len(html) < 4000 && strings.Contains(html, "Please enable JS and disable any ad blocker") {
		return true
	}
	return false
}
