package main

// Link preview za tekstualne poruke.
//
// WhatsApp NE generira preview na strani primatelja — posiljateljev klijent
// dohvati URL, procita Open Graph tagove i UGRADI naslov, opis i slicicu u samu
// poruku (ExtendedTextMessage). Poruka poslana kao goli `Conversation` string
// zato kod primatelja uvijek izgleda kao gol link, bez obzira na to koliko su
// og: tagovi na odredistu uredni.
//
// Ovdje se radi ono sto bi napravio sluzbeni klijent:
//   1. iz teksta se izvuce prvi URL,
//   2. dohvati se HTML i isparsiraju og:title / og:description / og:image,
//   3. slika se skine i pripremi u dvije velicine:
//        - ugradjena JPEGThumbnail (mala, putuje unutar poruke),
//        - hi-res thumbnail koji se uploada na WhatsApp medijske servere
//          (MediaLinkThumbnail) — bez njega klijent crta malu kvadratnu
//          slicicu umjesto velikog preview-a,
//   4. sve se slozi u ExtendedTextMessage.
//
// Gasi se s WA_LINK_PREVIEW=0. Svaki neuspjeh (nema URL-a, nema og: tagova,
// timeout, slika se ne da dekodirati) je NEFATALAN — poruka tada ode kao obican
// tekst, tocno kao prije ove zakrpe.

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
	"golang.org/x/net/html"
	"google.golang.org/protobuf/proto"
)

const (
	// Koliko cekamo odrediste. Preview nije vrijedan toga da posiljanje poruke
	// visi — ako se ne stigne, poruka ide bez njega.
	previewHTTPTimeout = 8 * time.Second

	// Koliko HTML-a citamo. og: tagovi su u <head>, pa je 512 KiB i vise nego
	// dovoljno; bez ograde bi jedan velik dokument mogao pojesti memoriju.
	maxHTMLBytes = 512 << 10

	// Gornja granica za skidanje slike (og:image je tipicno 1200x630 JPEG,
	// ~100-300 KB).
	maxImageBytes = 8 << 20

	// Ugradjena slicica putuje unutar E2E poruke, pa mora ostati mala.
	inlineThumbMaxDim  = 320
	inlineThumbQuality = 60

	// Hi-res thumbnail se uploada odvojeno; ovo je ono sto klijent prikaze kao
	// veliki preview.
	hiResThumbMaxDim  = 1280
	hiResThumbQuality = 82

	// Neki posluzitelji serviraju preview metapodatke samo poznatim klijentima.
	previewUserAgent = "WhatsApp/2.23.20.0 A"
)

// ogData je ono sto nam treba iz <head> odredisne stranice.
type ogData struct {
	Title       string
	Description string
	ImageURL    string
}

// Namjerno bez trailing interpunkcije: link na kraju recenice ("...pogledaj
// https://primjer.hr/x.") ne smije povuci tocku u URL.
var urlPattern = regexp.MustCompile(`https?://[^\s<>"']+`)

// firstURL vraca prvi http(s) URL iz teksta, ili "" ako ga nema.
func firstURL(text string) string {
	match := urlPattern.FindString(text)
	if match == "" {
		return ""
	}
	match = strings.TrimRight(match, ".,;:!?)]}'\"")
	if _, err := url.Parse(match); err != nil {
		return ""
	}
	return match
}

// linkPreviewEnabled — izlaz u nuzdi ako neko odrediste pravi probleme.
func linkPreviewEnabled() bool {
	return os.Getenv("WA_LINK_PREVIEW") != "0"
}

func previewHTTPClient() *http.Client {
	return &http.Client{Timeout: previewHTTPTimeout}
}

func previewGet(ctx context.Context, rawURL string, limit int64) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", previewUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,image/*;q=0.9,*/*;q=0.8")

	resp, err := previewHTTPClient().Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, "", err
	}
	return body, resp.Header.Get("Content-Type"), nil
}

// fetchOpenGraph dohvaca stranicu i cita og: tagove. Parsira se pravim HTML
// parserom, ne regexom — redoslijed atributa, jednostruki navodnici i HTML
// entiteti u opisu inace tiho pokvare rezultat.
func fetchOpenGraph(ctx context.Context, rawURL string) (*ogData, error) {
	body, contentType, err := previewGet(ctx, rawURL, maxHTMLBytes)
	if err != nil {
		return nil, err
	}
	if contentType != "" && !strings.Contains(strings.ToLower(contentType), "html") {
		return nil, fmt.Errorf("odrediste nije HTML (%s)", contentType)
	}

	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	meta := map[string]string{}
	pageTitle := ""

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "meta":
				var key, content string
				for _, attr := range n.Attr {
					switch strings.ToLower(attr.Key) {
					case "property", "name":
						key = strings.ToLower(attr.Val)
					case "content":
						content = attr.Val
					}
				}
				// Prvi pogodak pobjeduje — og:image se zna ponoviti za vise
				// velicina, a prva je ona koju stranica smatra glavnom.
				if key != "" && content != "" {
					if _, seen := meta[key]; !seen {
						meta[key] = content
					}
				}
			case "title":
				if pageTitle == "" && n.FirstChild != nil {
					pageTitle = n.FirstChild.Data
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	pick := func(keys ...string) string {
		for _, k := range keys {
			if v := strings.TrimSpace(meta[k]); v != "" {
				return html.UnescapeString(v)
			}
		}
		return ""
	}

	og := &ogData{
		Title:       pick("og:title", "twitter:title"),
		Description: pick("og:description", "twitter:description", "description"),
		ImageURL:    pick("og:image", "og:image:url", "twitter:image", "twitter:image:src"),
	}
	if og.Title == "" {
		og.Title = strings.TrimSpace(html.UnescapeString(pageTitle))
	}
	if og.Title == "" && og.ImageURL == "" {
		return nil, fmt.Errorf("stranica nema ni naslov ni og:image")
	}

	// og:image zna biti relativan.
	if og.ImageURL != "" {
		if base, err := url.Parse(rawURL); err == nil {
			if ref, err := url.Parse(og.ImageURL); err == nil {
				og.ImageURL = base.ResolveReference(ref).String()
			}
		}
	}
	return og, nil
}

// scaleJPEG smanjuje sliku tako da duza stranica ne prelazi maxDim i vraca JPEG.
// Slike manje od maxDim se ne povecavaju.
func scaleJPEG(src image.Image, maxDim int, quality int) ([]byte, int, int, error) {
	bounds := src.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 0 || h <= 0 {
		return nil, 0, 0, fmt.Errorf("prazna slika")
	}

	dstW, dstH := w, h
	if w > maxDim || h > maxDim {
		if w >= h {
			dstW = maxDim
			dstH = int(float64(h) * float64(maxDim) / float64(w))
		} else {
			dstH = maxDim
			dstW = int(float64(w) * float64(maxDim) / float64(h))
		}
	}
	if dstW < 1 {
		dstW = 1
	}
	if dstH < 1 {
		dstH = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, bounds, draw.Over, nil)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: quality}); err != nil {
		return nil, 0, 0, err
	}
	return buf.Bytes(), dstW, dstH, nil
}

// buildLinkPreview slaze ExtendedTextMessage s preview podacima za prvi URL u
// tekstu. Vraca nil kad preview nije moguc — pozivatelj tada salje obican tekst.
func buildLinkPreview(client *whatsmeow.Client, text string) *waProto.ExtendedTextMessage {
	if !linkPreviewEnabled() {
		return nil
	}
	matched := firstURL(text)
	if matched == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*previewHTTPTimeout)
	defer cancel()

	og, err := fetchOpenGraph(ctx, matched)
	if err != nil {
		fmt.Printf("Link preview: og tagovi nedostupni za %s: %v\n", matched, err)
		return nil
	}

	ext := &waProto.ExtendedTextMessage{
		Text:        proto.String(text),
		MatchedText: proto.String(matched),
		PreviewType: waProto.ExtendedTextMessage_NONE.Enum(),
	}
	if og.Title != "" {
		ext.Title = proto.String(og.Title)
	}
	if og.Description != "" {
		ext.Description = proto.String(og.Description)
	}

	if og.ImageURL == "" {
		// Naslov i opis bez slike su i dalje bolji od golog linka.
		return ext
	}

	raw, _, err := previewGet(ctx, og.ImageURL, maxImageBytes)
	if err != nil {
		fmt.Printf("Link preview: og:image se ne da skinuti (%s): %v\n", og.ImageURL, err)
		return ext
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		fmt.Printf("Link preview: og:image se ne da dekodirati: %v\n", err)
		return ext
	}

	inline, _, _, err := scaleJPEG(img, inlineThumbMaxDim, inlineThumbQuality)
	if err != nil {
		fmt.Printf("Link preview: sicica se ne da napraviti: %v\n", err)
		return ext
	}
	ext.JPEGThumbnail = inline

	// Hi-res thumbnail ide na medijske servere. Bez njega WhatsApp nacrta malu
	// kvadratnu slicicu; s njim dobijemo veliki preview kakav ima sluzbeni
	// klijent. Neuspjeh uploada nije fatalan — ostaje ugradjena sicica.
	hiRes, hiW, hiH, err := scaleJPEG(img, hiResThumbMaxDim, hiResThumbQuality)
	if err != nil {
		return ext
	}
	upload, err := client.Upload(ctx, hiRes, whatsmeow.MediaLinkThumbnail)
	if err != nil {
		fmt.Printf("Link preview: upload hi-res sicice nije uspio: %v\n", err)
		return ext
	}

	ext.ThumbnailDirectPath = proto.String(upload.DirectPath)
	ext.ThumbnailSHA256 = upload.FileSHA256
	ext.ThumbnailEncSHA256 = upload.FileEncSHA256
	ext.MediaKey = upload.MediaKey
	ext.MediaKeyTimestamp = proto.Int64(time.Now().Unix())
	ext.ThumbnailWidth = proto.Uint32(uint32(hiW))
	ext.ThumbnailHeight = proto.Uint32(uint32(hiH))

	return ext
}

// previewSummary — kratak redak za log, da se u terminalu vidi je li preview
// stvarno otisao uz poruku.
func previewSummary(ext *waProto.ExtendedTextMessage) string {
	if ext == nil {
		return ""
	}
	parts := []string{}
	if ext.GetTitle() != "" {
		title := ext.GetTitle()
		if len(title) > 48 {
			title = title[:48] + "…"
		}
		parts = append(parts, "naslov="+strconv.Quote(title))
	}
	if len(ext.GetJPEGThumbnail()) > 0 {
		parts = append(parts, fmt.Sprintf("sicica=%dB", len(ext.GetJPEGThumbnail())))
	}
	if ext.GetThumbnailDirectPath() != "" {
		parts = append(parts, fmt.Sprintf("hi-res=%dx%d", ext.GetThumbnailWidth(), ext.GetThumbnailHeight()))
	}
	if len(parts) == 0 {
		return ""
	}
	return " [preview: " + strings.Join(parts, " ") + "]"
}
