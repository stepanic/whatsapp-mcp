package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFirstURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://domovina.ai/v/aue1GuuMsbA/t/1990", "https://domovina.ai/v/aue1GuuMsbA/t/1990"},
		// Link na kraju recenice ne smije povuci interpunkciju.
		{"pogledaj https://primjer.hr/x.", "https://primjer.hr/x"},
		{"u zagradi (https://primjer.hr/y) ide dalje", "https://primjer.hr/y"},
		{"prvi https://a.hr pa drugi https://b.hr", "https://a.hr"},
		{"bez linka", ""},
		{"ftp://primjer.hr/datoteka", ""},
	}
	for _, c := range cases {
		if got := firstURL(c.in); got != c.want {
			t.Errorf("firstURL(%q) = %q, ocekivano %q", c.in, got, c.want)
		}
	}
}

func TestFetchOpenGraph(t *testing.T) {
	// Redoslijed atributa, jednostruki navodnici, HTML entitet i relativan
	// og:image — sve cetiri stvari koje regex-parser tiho pokvari.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html;charset=UTF-8")
		w.Write([]byte(`<html><head>
			<title>Fallback naslov</title>
			<meta content="33:10 &middot; Slu&#269;aj" property="og:title">
			<meta property='og:description' content='Opis s &quot;navodnicima&quot;'>
			<meta property="og:image" content="/images/og-t-1990.jpg">
		</head><body>x</body></html>`))
	}))
	defer srv.Close()

	og, err := fetchOpenGraph(context.Background(), srv.URL+"/v/x/t/1990")
	if err != nil {
		t.Fatalf("fetchOpenGraph: %v", err)
	}
	if og.Title != "33:10 · Slučaj" {
		t.Errorf("Title = %q", og.Title)
	}
	if og.Description != `Opis s "navodnicima"` {
		t.Errorf("Description = %q", og.Description)
	}
	if og.ImageURL != srv.URL+"/images/og-t-1990.jpg" {
		t.Errorf("ImageURL = %q — relativan og:image nije razrijesen", og.ImageURL)
	}
}

func TestFetchOpenGraphFallsBackToTitle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><head><title>Samo naslov</title></head><body></body></html>`))
	}))
	defer srv.Close()

	og, err := fetchOpenGraph(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("fetchOpenGraph: %v", err)
	}
	if og.Title != "Samo naslov" {
		t.Errorf("Title = %q", og.Title)
	}
}

func TestFetchOpenGraphRejectsNonHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Write([]byte("%PDF-1.4"))
	}))
	defer srv.Close()

	if _, err := fetchOpenGraph(context.Background(), srv.URL); err == nil {
		t.Error("ocekivana greska za ne-HTML odrediste")
	}
}
