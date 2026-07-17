package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// federationScheme is the URL scheme for WebFinger lookups — https per spec,
// loosened to http alongside the SSRF guard (CAIRN_FEDERATION_ALLOW_PRIVATE_HOSTS)
// so LAN self-hosting / the interop harness, which serve plain HTTP, can resolve.
var federationScheme = "https"

// resolveRemoteHandle turns "alice@remote.example" (or "@alice@remote.example")
// into the actor URL via WebFinger.
func resolveRemoteHandle(ctx context.Context, handle string) (string, error) {
	handle = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(handle), "@"))
	at := strings.LastIndexByte(handle, '@')
	if at <= 0 || at == len(handle)-1 {
		return "", fmt.Errorf("expected user@host")
	}
	user, host := handle[:at], handle[at+1:]
	wf := federationScheme + "://" + host + "/.well-known/webfinger?resource=" +
		url.QueryEscape("acct:"+user+"@"+host)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wf, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/jrd+json, application/json")
	resp, err := federationClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("webfinger: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("webfinger %s: status %d", host, resp.StatusCode)
	}
	var doc struct {
		Links []struct {
			Rel  string `json:"rel"`
			Type string `json:"type"`
			Href string `json:"href"`
		} `json:"links"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&doc); err != nil {
		return "", fmt.Errorf("decode webfinger: %w", err)
	}
	for _, l := range doc.Links {
		if l.Rel == "self" && strings.Contains(l.Type, "activity+json") && l.Href != "" {
			return l.Href, nil
		}
	}
	return "", fmt.Errorf("no actor link for %s", handle)
}
