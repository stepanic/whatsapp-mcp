package main

// Popis grupa u kojima je ovaj racun clan.
//
// Zasto postoji: messages.db zna samo za chatove kroz koje je prosla poruka ili
// koje je pokupio history sync. Svjeze napravljena grupa u kojoj jos nitko nista
// nije rekao tamo NE postoji, pa joj se ne moze doznati JID — a bez JID-a se ne
// moze poslati poruka. Uz to messages.db uopce ne sprema group metadata, pa se iz
// njega ne vidi je li grupa dio WhatsApp zajednice (community).
//
// GET /api/groups dohvaca popis izravno s WhatsApp servera (GetJoinedGroups), pa
// vraca i jedno i drugo.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

// groupsRequestTimeout ogranicava koliko dugo cekamo WhatsApp server.
// Poziv ide preko websocketa; ako veza visi, handler mora odustati, a ne drzati
// HTTP klijenta zauvijek.
const groupsRequestTimeout = 30 * time.Second

// GroupSummary je ono sto endpoint vraca po grupi. Namjerno je plosnat i u
// snake_caseu — cita ga Python (MCP server i skripte za slanje), ne Go.
type GroupSummary struct {
	JID              string `json:"jid"`
	Name             string `json:"name"`
	Topic            string `json:"topic,omitempty"`
	OwnerJID         string `json:"owner_jid,omitempty"`
	ParticipantCount int    `json:"participant_count"`
	Created          string `json:"created,omitempty"`

	// IsCommunity: ova grupa JE roditelj zajednice (community), ne obicna grupa.
	IsCommunity bool `json:"is_community"`
	// CommunityJID: ova grupa PRIPADA zajednici s tim JID-om (prazno = samostalna grupa).
	CommunityJID string `json:"community_jid,omitempty"`
	// CommunityName se razrjesava iz istog popisa, ako smo clan i roditeljske grupe.
	CommunityName string `json:"community_name,omitempty"`
	// IsDefaultSubGroup: "General" grupa zajednice, ona koja nastaje s njom.
	IsDefaultSubGroup bool `json:"is_default_subgroup"`

	// IsAnnounce: samo administratori smiju pisati.
	IsAnnounce bool `json:"is_announce"`
	IsLocked   bool `json:"is_locked"`
}

type GroupsResponse struct {
	Success bool           `json:"success"`
	Count   int            `json:"count"`
	Groups  []GroupSummary `json:"groups"`
	Message string         `json:"message,omitempty"`
}

// summarizeGroups pretvara whatsmeow tipove u nas oblik i razrjesava imena
// zajednica. Odvojeno od HTTP-a da se moze testirati bez mreze.
func summarizeGroups(groups []*types.GroupInfo, query string) []GroupSummary {
	// Prvo mapa JID -> ime, da LinkedParentJID mozemo prikazati kao ime.
	// Radi samo za zajednice cijih smo grupa clan; inace ostaje samo JID.
	names := make(map[string]string, len(groups))
	for _, g := range groups {
		names[g.JID.String()] = g.Name
	}

	needle := strings.ToLower(strings.TrimSpace(query))
	out := make([]GroupSummary, 0, len(groups))

	for _, g := range groups {
		jid := g.JID.String()
		if needle != "" &&
			!strings.Contains(strings.ToLower(g.Name), needle) &&
			!strings.Contains(strings.ToLower(jid), needle) {
			continue
		}

		s := GroupSummary{
			JID:               jid,
			Name:              g.Name,
			Topic:             g.Topic,
			ParticipantCount:  len(g.Participants),
			IsCommunity:       g.IsParent,
			IsDefaultSubGroup: g.IsDefaultSubGroup,
			IsAnnounce:        g.IsAnnounce,
			IsLocked:          g.IsLocked,
		}
		// ParticipantCount zna doci i kao zaseban broj kad lista nije poslana.
		if s.ParticipantCount == 0 && g.ParticipantCount > 0 {
			s.ParticipantCount = g.ParticipantCount
		}
		if !g.OwnerJID.IsEmpty() {
			s.OwnerJID = g.OwnerJID.String()
		}
		if !g.GroupCreated.IsZero() {
			s.Created = g.GroupCreated.Format(time.RFC3339)
		}
		if !g.LinkedParentJID.IsEmpty() {
			s.CommunityJID = g.LinkedParentJID.String()
			s.CommunityName = names[s.CommunityJID]
		}
		out = append(out, s)
	}

	// Stabilan poredak: po imenu, pa po JID-u za grupe istog imena (ima ih).
	sort.Slice(out, func(i, j int) bool {
		if strings.EqualFold(out[i].Name, out[j].Name) {
			return out[i].JID < out[j].JID
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// handleGroups posluzuje GET /api/groups?query=<filter>.
func handleGroups(client *whatsmeow.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "application/json")

		if !client.IsConnected() {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(GroupsResponse{
				Success: false,
				Message: "not connected to WhatsApp",
			})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), groupsRequestTimeout)
		defer cancel()

		groups, err := client.GetJoinedGroups(ctx)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(GroupsResponse{
				Success: false,
				Message: fmt.Sprintf("failed to fetch groups: %v", err),
			})
			return
		}

		summaries := summarizeGroups(groups, r.URL.Query().Get("query"))
		fmt.Printf("Groups listed: %d od ukupno %d\n", len(summaries), len(groups))

		json.NewEncoder(w).Encode(GroupsResponse{
			Success: true,
			Count:   len(summaries),
			Groups:  summaries,
		})
	}
}
