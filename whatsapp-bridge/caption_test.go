package main

import (
	"testing"

	waProto "go.mau.fi/whatsmeow/binary/proto"
	"google.golang.org/protobuf/proto"
)

// Caption uz medij je bio tiha rupa: poruka se upisivala s praznim `content`,
// sto izvana izgleda isto kao slika bez ijedne rijeci. Test postoji da se ta
// razlika vise ne moze izgubiti neprimjetno.
func TestExtractTextContentCaptions(t *testing.T) {
	// Doslovna Krunina poruka od 12.09.2026. — caption JE bio cijela poruka.
	const krunin = "Ej, ovdje kad pošaljem poruku, svi je sudionici dobiju, kaj ne?"

	slucajevi := []struct {
		ime  string
		msg  *waProto.Message
		zeli string
	}{
		{"nil", nil, ""},
		{"conversation", &waProto.Message{Conversation: proto.String("Može, deal")}, "Može, deal"},
		{
			"extendedText",
			&waProto.Message{ExtendedTextMessage: &waProto.ExtendedTextMessage{Text: proto.String("Hvala ti!")}},
			"Hvala ti!",
		},
		{
			"slika s captionom",
			&waProto.Message{ImageMessage: &waProto.ImageMessage{Caption: proto.String(krunin)}},
			krunin,
		},
		{
			"slika bez captiona",
			&waProto.Message{ImageMessage: &waProto.ImageMessage{}},
			"",
		},
		{
			"video s captionom",
			&waProto.Message{VideoMessage: &waProto.VideoMessage{Caption: proto.String("pogledaj ovo")}},
			"pogledaj ovo",
		},
		{
			"dokument s captionom",
			&waProto.Message{DocumentMessage: &waProto.DocumentMessage{Caption: proto.String("evo ugovora")}},
			"evo ugovora",
		},
		{
			// Ovako dokument s captionom stvarno stigne iz history synca.
			"DocumentWithCaption omotac",
			&waProto.Message{DocumentWithCaptionMessage: &waProto.FutureProofMessage{
				Message: &waProto.Message{DocumentMessage: &waProto.DocumentMessage{
					Caption: proto.String("prirucnik"),
				}},
			}},
			"prirucnik",
		},
		{
			"ephemeral + viewOnce (omotac u omotacu)",
			&waProto.Message{EphemeralMessage: &waProto.FutureProofMessage{
				Message: &waProto.Message{ViewOnceMessageV2: &waProto.FutureProofMessage{
					Message: &waProto.Message{ImageMessage: &waProto.ImageMessage{
						Caption: proto.String("nestaje"),
					}},
				}},
			}},
			"nestaje",
		},
		{
			// Audio nema caption u protokolu — ne smije se izmisliti tekst.
			"audio",
			&waProto.Message{AudioMessage: &waProto.AudioMessage{}},
			"",
		},
	}

	for _, s := range slucajevi {
		t.Run(s.ime, func(t *testing.T) {
			if dobio := extractTextContent(s.msg); dobio != s.zeli {
				t.Errorf("extractTextContent() = %q, ocekivano %q", dobio, s.zeli)
			}
		})
	}
}

// Omotana slika mora zadrzati i privitak, ne samo caption.
func TestExtractMediaInfoOdmotava(t *testing.T) {
	msg := &waProto.Message{ViewOnceMessageV2: &waProto.FutureProofMessage{
		Message: &waProto.Message{ImageMessage: &waProto.ImageMessage{
			Caption:    proto.String("x"),
			FileLength: proto.Uint64(213903),
		}},
	}}

	mediaType, _, _, _, _, _, fileLength := extractMediaInfo(msg)
	if mediaType != "image" {
		t.Errorf("mediaType = %q, ocekivano \"image\"", mediaType)
	}
	if fileLength != 213903 {
		t.Errorf("fileLength = %d, ocekivano 213903", fileLength)
	}
}
