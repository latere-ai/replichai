// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package fetch

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoopbackIsRefusedUnlessAllowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	defer srv.Close()

	_, err := New().Fetch(context.Background(), srv.URL)
	if !errors.Is(err, ErrForbiddenAddress) {
		t.Fatalf("a loopback server was fetched: err=%v", err)
	}
	body, err := New(AllowPrivate()).Fetch(context.Background(), srv.URL)
	if err != nil || string(body) != "hello" {
		t.Fatalf("AllowPrivate: body=%q err=%v", body, err)
	}
}

func TestANameResolvingToAForbiddenAddressIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("metadata"))
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))

	// The name looks harmless; the address it resolves to is the loopback
	// server, standing in for a metadata service.
	c := New(withResolver(map[string][]net.IPAddr{"public.example": {{IP: net.ParseIP("127.0.0.1")}}}))
	_, err := c.Fetch(context.Background(), "http://public.example:"+port+"/")
	if !errors.Is(err, ErrForbiddenAddress) {
		t.Fatalf("a name resolving to loopback was fetched: err=%v", err)
	}
}

func TestBodyOverTheCapIsAnErrorNotATruncation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, 1025))
	}))
	defer srv.Close()

	_, err := New(AllowPrivate(), WithMaxBytes(1024)).Fetch(context.Background(), srv.URL)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversize body: err=%v", err)
	}
	body, err := New(AllowPrivate(), WithMaxBytes(1025)).Fetch(context.Background(), srv.URL)
	if err != nil || len(body) != 1025 {
		t.Fatalf("body exactly at the cap: len=%d err=%v", len(body), err)
	}
}

func TestEveryRedirectHopIsChecked(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("inside"))
	}))
	defer target.Close()
	hop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer hop.Close()

	// Redirect to a non-http scheme is refused at the redirect check.
	fileHop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
	}))
	defer fileHop.Close()
	_, err := New(AllowPrivate()).Fetch(context.Background(), fileHop.URL)
	if !errors.Is(err, ErrScheme) {
		t.Fatalf("redirect to file://: err=%v", err)
	}

	// A chain longer than the bound is refused.
	var loop *httptest.Server
	loop = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, loop.URL+"/again", http.StatusFound)
	}))
	defer loop.Close()
	_, err = New(AllowPrivate()).Fetch(context.Background(), loop.URL)
	if !errors.Is(err, ErrTooManyRedirects) {
		t.Fatalf("redirect loop: err=%v", err)
	}

	// A permitted chain is followed.
	body, err := New(AllowPrivate()).Fetch(context.Background(), hop.URL)
	if err != nil || string(body) != "inside" {
		t.Fatalf("permitted redirect: body=%q err=%v", body, err)
	}
}

func TestOnlyHTTPSchemesAreFetched(t *testing.T) {
	for _, u := range []string{"file:///etc/passwd", "ftp://example.org/x", "gopher://example.org", "javascript:alert(1)"} {
		if _, err := New(AllowPrivate()).Fetch(context.Background(), u); !errors.Is(err, ErrScheme) {
			t.Errorf("%s: err=%v", u, err)
		}
	}
}

func TestStatusesAreClassifiedForRetry(t *testing.T) {
	code := http.StatusServiceUnavailable
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
	}))
	defer srv.Close()
	c := New(AllowPrivate())

	_, err := c.Fetch(context.Background(), srv.URL)
	var status *StatusError
	if !errors.As(err, &status) || status.Code != 503 || !Retryable(err) {
		t.Fatalf("503: err=%v retryable=%v", err, Retryable(err))
	}
	code = http.StatusNotFound
	_, err = c.Fetch(context.Background(), srv.URL)
	if !errors.As(err, &status) || status.Code != 404 || Retryable(err) {
		t.Fatalf("404: err=%v retryable=%v", err, Retryable(err))
	}
	for _, e := range []error{ErrTooLarge, ErrForbiddenAddress, ErrScheme, ErrTooManyRedirects} {
		if Retryable(e) {
			t.Errorf("%v must not be retryable", e)
		}
	}
	if !Retryable(context.DeadlineExceeded) {
		t.Error("a timeout must be retryable")
	}
}

func TestPublicAddressPredicate(t *testing.T) {
	forbidden := []string{"127.0.0.1", "::1", "10.0.0.1", "172.16.5.4", "192.168.1.1", "169.254.169.254", "fe80::1", "0.0.0.0", "0.1.2.3", "100.64.0.1", "224.0.0.1", "ff02::1", "fd00::1"}
	for _, s := range forbidden {
		if Public(net.ParseIP(s)) {
			t.Errorf("%s judged public", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111", "151.101.1.140"} {
		if !Public(net.ParseIP(s)) {
			t.Errorf("%s judged not public", s)
		}
	}
	if Public(nil) {
		t.Error("nil judged public")
	}
}
