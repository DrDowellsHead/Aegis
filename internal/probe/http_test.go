package probe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStatus(t *testing.T) {
	for _, code := range []int{200, 204, 301, 404, 503} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if code == 301 {
					w.Header().Set("Location", "/redirect")
				}
				w.WriteHeader(code)
			}))
			defer s.Close()
			got, err := HTTP(context.Background(), s.URL, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if got.StatusCode != code || got.Healthy != (code >= 200 && code < 300) {
				t.Fatalf("unexpected result: %+v", got)
			}
		})
	}
}

func TestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := HTTP(ctx, "http://127.0.0.1:1", time.Second)
	if err != nil || got.Healthy || got.Error != "context canceled" {
		t.Fatalf("%+v, %v", got, err)
	}
}

func TestDeadline(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer s.Close()
	got, err := HTTP(context.Background(), s.URL, 50*time.Millisecond)
	if err != nil || got.Healthy || got.Error != "context deadline exceeded" {
		t.Fatalf("%+v, %v", got, err)
	}
}

func TestValidation(t *testing.T) {
	for _, u := range []string{"", "localhost", "ftp://host", "http://user:secret@host", "http://host/#secret", "http://%"} {
		_, err := HTTP(context.Background(), u, time.Second)
		if err == nil {
			t.Errorf("accepted %q", u)
		}
		if err != nil && strings.Contains(err.Error(), "secret") {
			t.Fatal("secret leaked")
		}
	}
	for _, d := range []time.Duration{0, -1, time.Minute + 1} {
		if _, err := HTTP(context.Background(), "http://localhost", d); err == nil {
			t.Errorf("accepted %v", d)
		}
	}
}

func TestUntrustedTLS(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer s.Close()
	got, err := HTTP(context.Background(), s.URL, time.Second)
	if err != nil || got.Healthy || got.Error != "request failed" {
		t.Fatalf("%+v, %v", got, err)
	}
}
