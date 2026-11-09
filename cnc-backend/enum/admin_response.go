package main

import (
	"cnc-enum/detection"
	"context"
	"crypto/rand"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type adminResponse struct {
	status    int
	body      []byte
	finalURL  string
	signature string
	structure string
}

func missingAdminPath() string {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "/__cnc_missing_page_check__"
	}
	return fmt.Sprintf("/__cnc_missing_%x", nonce)
}

func fetchAdminResponse(client *http.Client, address string) (adminResponse, error) {
	return fetchAdminResponseContext(context.Background(), client, address)
}

func fetchAdminResponseContext(ctx context.Context, client *http.Client, address string) (adminResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return adminResponse{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; admin-enum/1.0)")
	resp, err := client.Do(req)
	if err != nil {
		return adminResponse{}, err
	}
	defer resp.Body.Close()
	// Never confirm a truncated or partially read response.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 128*1024+1))
	if err != nil {
		return adminResponse{}, err
	}
	if len(body) > 128*1024 {
		return adminResponse{}, fmt.Errorf("admin response exceeds limit")
	}
	u, _ := url.Parse(address)
	normalized := html.UnescapeString(string(body))
	// Soft 404 templates often echo the requested URL/path.
	normalized = strings.ReplaceAll(normalized, address, "<requested-path>")
	if u.Path != "" && u.Path != "/" {
		normalized = strings.ReplaceAll(normalized, u.EscapedPath(), "<requested-path>")
		normalized = strings.ReplaceAll(normalized, u.Path, "<requested-path>")
	}
	return adminResponse{
		status: resp.StatusCode, body: body, finalURL: resp.Request.URL.String(),
		signature: strings.Join(strings.Fields(normalized), " "),
		structure: detection.StructuralSignature(body),
	}, nil
}
