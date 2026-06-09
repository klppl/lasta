package extractor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CookieSite is one per-site cookie slot in the settings UI. Pasted content
// is filtered to Domains so each slot only holds the cookies its site needs
// and a wrong-slot paste fails loudly.
type CookieSite struct {
	Key     string
	Label   string
	Domains []string
}

// CookieSites defines the available slots. YouTube includes google.com
// because its auth cookies live there.
var CookieSites = []CookieSite{
	{Key: "youtube", Label: "YouTube", Domains: []string{"youtube.com", "google.com"}},
	{Key: "instagram", Label: "Instagram", Domains: []string{"instagram.com"}},
	{Key: "twitter", Label: "Twitter/X", Domains: []string{"twitter.com", "x.com"}},
	{Key: "threads", Label: "Threads", Domains: []string{"threads.net", "threads.com"}},
}

// CookieSiteByKey returns the slot definition, or nil for unknown keys.
func CookieSiteByKey(key string) *CookieSite {
	for i := range CookieSites {
		if CookieSites[i].Key == key {
			return &CookieSites[i]
		}
	}
	return nil
}

func siteCookieFile(key string) string {
	return filepath.Join(filepath.Dir(CookiesPath), "cookies-"+key+".txt")
}

// SaveSiteCookies filters Netscape-format content to the site's domains,
// stores it in the site's slot, and rebuilds the merged cookies file that
// yt-dlp consumes. Returns the number of cookies kept.
func SaveSiteCookies(site *CookieSite, content string) (int, error) {
	if CookiesPath == "" {
		return 0, fmt.Errorf("no cookies path configured")
	}
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		f := cookieFields(line)
		if f == nil {
			continue
		}
		if domainMatches(f[0], site.Domains) {
			lines = append(lines, strings.TrimSuffix(line, "\r"))
		}
	}
	if len(lines) == 0 {
		if countCookieLines(content) == 0 {
			return 0, fmt.Errorf("no valid cookie lines found — paste the contents of a Netscape-format cookies.txt (tab-separated, 7 fields per line)")
		}
		return 0, fmt.Errorf("no %s cookies in the pasted text — it contains cookies for other domains", site.Label)
	}

	if err := os.MkdirAll(filepath.Dir(CookiesPath), 0o700); err != nil {
		return 0, fmt.Errorf("creating cookies dir: %w", err)
	}
	body := "# Netscape HTTP Cookie File\n" + strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(siteCookieFile(site.Key), []byte(body), 0o600); err != nil {
		return 0, fmt.Errorf("writing cookies file: %w", err)
	}
	if err := rebuildMergedCookies(); err != nil {
		return 0, err
	}
	return len(lines), nil
}

// ClearSiteCookies removes one site's slot and rebuilds the merged file.
func ClearSiteCookies(site *CookieSite) error {
	if err := os.Remove(siteCookieFile(site.Key)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return rebuildMergedCookies()
}

// SiteCookieInfo reports a slot's entry count and save time; ok is false
// when the slot is empty.
func SiteCookieInfo(key string) (count int, saved time.Time, ok bool) {
	st, err := os.Stat(siteCookieFile(key))
	if err != nil {
		return 0, time.Time{}, false
	}
	data, err := os.ReadFile(siteCookieFile(key))
	if err != nil {
		return 0, time.Time{}, false
	}
	return countCookieLines(string(data)), st.ModTime(), true
}

// rebuildMergedCookies regenerates the single file yt-dlp reads (CookiesPath)
// from all site slots, deduplicated by domain+path+name (later slots win).
// With no slots saved, the merged file is removed.
func rebuildMergedCookies() error {
	merged := make(map[string]string)
	var order []string
	for _, site := range CookieSites {
		data, err := os.ReadFile(siteCookieFile(site.Key))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			f := cookieFields(line)
			if f == nil {
				continue
			}
			key := strings.ToLower(f[0]) + "\x00" + f[2] + "\x00" + f[5]
			if _, exists := merged[key]; !exists {
				order = append(order, key)
			}
			merged[key] = strings.TrimSuffix(line, "\r")
		}
	}
	if len(order) == 0 {
		if err := os.Remove(CookiesPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	var b strings.Builder
	b.WriteString("# Netscape HTTP Cookie File\n")
	for _, key := range order {
		b.WriteString(merged[key])
		b.WriteByte('\n')
	}
	if err := os.WriteFile(CookiesPath, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("writing merged cookies: %w", err)
	}
	return nil
}

func domainMatches(cookieDomain string, domains []string) bool {
	d := strings.TrimPrefix(strings.ToLower(cookieDomain), ".")
	for _, want := range domains {
		if d == want || strings.HasSuffix(d, "."+want) {
			return true
		}
	}
	return false
}

func countCookieLines(content string) int {
	n := 0
	for _, line := range strings.Split(content, "\n") {
		if cookieFields(line) != nil {
			n++
		}
	}
	return n
}

// cookieFields parses one Netscape cookie line into its 7 tab-separated
// fields, returning nil for comments, blanks, and malformed lines.
// "#HttpOnly_" prefixed lines are real cookies and are kept.
func cookieFields(line string) []string {
	line = strings.TrimSuffix(line, "\r")
	if strings.HasPrefix(line, "#HttpOnly_") {
		line = strings.TrimPrefix(line, "#HttpOnly_")
	} else if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
		return nil
	}
	fields := strings.Split(line, "\t")
	if len(fields) < 7 {
		return nil
	}
	return fields
}

// CookieHeaderFor builds a Cookie header value from the merged cookies file
// for the given domains (e.g. "threads.com"). Used by extractors that fetch
// pages natively instead of through yt-dlp.
func CookieHeaderFor(domains ...string) string {
	if !HasCookies() {
		return ""
	}
	data, err := os.ReadFile(CookiesPath)
	if err != nil {
		return ""
	}
	seen := make(map[string]bool)
	var pairs []string
	for _, line := range strings.Split(string(data), "\n") {
		fields := cookieFields(line)
		if fields == nil {
			continue
		}
		name, value := fields[5], fields[6]
		if name == "" || seen[name] {
			continue
		}
		if domainMatches(fields[0], domains) {
			seen[name] = true
			pairs = append(pairs, name+"="+value)
		}
	}
	return strings.Join(pairs, "; ")
}
