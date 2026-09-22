package main

// PDF preview za DocumentMessage: sličica prve stranice i broj stranica.
// Bez JPEGThumbnail WhatsApp prikaže samo ikonu i ime; s njim prikaže prvu
// stranicu kao i kad se PDF šalje iz WhatsApp Desktopa. Koristi macOS alate
// (sips za render, pdfinfo ili mdls za broj stranica) pa nema novih Go ovisnosti; ako
// alat zakaže, dokument se šalje bez previewa umjesto da slanje padne.

import (
	"bytes"
	"image"
	_ "image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	waProto "go.mau.fi/whatsmeow/binary/proto"
	"google.golang.org/protobuf/proto"
)

func addPDFPreview(doc *waProto.DocumentMessage, pdfPath string) {
	if pages := pdfPageCount(pdfPath); pages > 0 {
		doc.PageCount = proto.Uint32(pages)
	}
	tmp, err := os.MkdirTemp("", "wa-pdfthumb-")
	if err != nil {
		return
	}
	defer os.RemoveAll(tmp)
	out := filepath.Join(tmp, "thumb.jpg")
	// sips renderira prvu stranicu PDF-a; -Z ograničava dulju stranicu.
	if err := exec.Command("sips", "-s", "format", "jpeg", "-s", "formatOptions", "70",
		"-Z", "480", pdfPath, "--out", out).Run(); err != nil {
		return
	}
	data, err := os.ReadFile(out)
	if err != nil || len(data) == 0 {
		return
	}
	doc.JPEGThumbnail = data
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		doc.ThumbnailWidth = proto.Uint32(uint32(cfg.Width))
		doc.ThumbnailHeight = proto.Uint32(uint32(cfg.Height))
	}
}

func pdfPageCount(pdfPath string) uint32 {
	// pdfinfo (poppler) je pouzdan; mdls vraca null za fajlove koje Spotlight ne
	// indeksira (npr. /private/tmp). LaunchAgent ima ogoljeni PATH, zato i
	// apsolutna Homebrew putanja.
	for _, bin := range []string{"pdfinfo", "/opt/homebrew/bin/pdfinfo", "/usr/local/bin/pdfinfo"} {
		out, err := exec.Command(bin, pdfPath).Output()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "Pages:") {
				if n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Pages:"))); err == nil && n > 0 {
					return uint32(n)
				}
			}
		}
	}
	out, err := exec.Command("mdls", "-raw", "-name", "kMDItemNumberOfPages", pdfPath).Output()
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || n <= 0 {
		return 0
	}
	return uint32(n)
}
