package cookieimport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
)

// ReadJSON accepts cookie arrays exported by browser extensions, or a
// DevTools-style {"cookies": [...]} object. It never reads browser keys.
func ReadJSON(path string) (Result, error) {
	file, err := os.Open(path)
	if err != nil {
		return Result{}, err
	}
	defer file.Close()
	const maxSize = 32 << 20
	data, err := io.ReadAll(io.LimitReader(file, maxSize+1))
	if err != nil {
		return Result{}, err
	}
	if len(data) > maxSize {
		return Result{}, fmt.Errorf("The cookie file is larger than 32 MiB")
	}
	data = bytes.TrimSpace(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}))
	if len(data) > 0 && data[0] == '{' {
		var object struct {
			Cookies json.RawMessage `json:"cookies"`
		}
		if json.Unmarshal(data, &object) != nil {
			return Result{}, fmt.Errorf("Invalid cookie JSON. Export a JSON array of cookies from your browser")
		}
		data = bytes.TrimSpace(object.Cookies)
	}
	var entries []struct {
		Name             string          `json:"name"`
		Value            *string         `json:"value"`
		Domain           string          `json:"domain"`
		Path             string          `json:"path"`
		Secure           bool            `json:"secure"`
		HTTPOnly         bool            `json:"httpOnly"`
		HostOnly         *bool           `json:"hostOnly"`
		Session          bool            `json:"session"`
		ExpirationDate   *float64        `json:"expirationDate"`
		Expires          *float64        `json:"expires"`
		SameSite         string          `json:"sameSite"`
		PartitionKey     json.RawMessage `json:"partitionKey"`
		Partitioned      bool            `json:"partitioned"`
		OriginAttributes string          `json:"originAttributes"`
	}
	if len(data) == 0 || data[0] != '[' || json.Unmarshal(data, &entries) != nil {
		// Decoder errors can contain source text, including cookie values.
		return Result{}, fmt.Errorf("Invalid cookie JSON. Expected an array with name, value, domain and path fields")
	}
	var cookies []*network.CookieParam
	skipped := 0
	for _, entry := range entries {
		partition := bytes.TrimSpace(entry.PartitionKey)
		if entry.Partitioned || (len(partition) > 0 && !bytes.Equal(partition, []byte("null"))) || entry.OriginAttributes != "" {
			skipped++
			continue
		}
		if entry.Value == nil || entry.Name == "" || entry.Domain == "" {
			skipped++
			continue
		}
		path := entry.Path
		if path == "" {
			path = "/"
		}
		if (&http.Cookie{Name: entry.Name, Value: *entry.Value, Domain: entry.Domain, Path: path}).Valid() != nil {
			skipped++
			continue
		}
		cookie := &network.CookieParam{Name: entry.Name, Value: *entry.Value, Path: path, Secure: entry.Secure, HTTPOnly: entry.HTTPOnly}
		place(cookie, entry.Domain, entry.Secure)
		if entry.HostOnly != nil {
			cookie.Domain, cookie.URL = "", ""
			if *entry.HostOnly {
				place(cookie, strings.TrimPrefix(entry.Domain, "."), entry.Secure)
			} else {
				cookie.Domain = entry.Domain
			}
		}
		switch strings.ToLower(entry.SameSite) {
		case "lax":
			cookie.SameSite = network.CookieSameSiteLax
		case "strict":
			cookie.SameSite = network.CookieSameSiteStrict
		case "none", "no_restriction":
			cookie.SameSite = network.CookieSameSiteNone
		case "", "unspecified":
		default:
			skipped++
			continue
		}
		expires := entry.ExpirationDate
		if expires == nil {
			expires = entry.Expires
		}
		if !entry.Session && expires != nil && *expires != -1 {
			if *expires <= float64(time.Now().Unix()) || *expires > 253402300799 {
				skipped++
				continue
			}
			at := cdp.TimeSinceEpoch(time.Unix(int64(*expires), 0))
			cookie.Expires = &at
		}
		cookies = append(cookies, cookie)
	}
	return result(cookies, skipped), nil
}
