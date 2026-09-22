package main

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
)

func gjid(user string) types.JID {
	return types.JID{User: user, Server: types.GroupServer}
}

func mkGroup(jidUser, name string) *types.GroupInfo {
	return &types.GroupInfo{
		JID:       gjid(jidUser),
		GroupName: types.GroupName{Name: name},
	}
}

// Ime zajednice se razrjesava samo kad smo clan i roditeljske grupe; inace
// ostaje gol JID. Ovo je cijela poanta endpointa, pa se testira prvo.
func TestSummarizeGroupsResolvesCommunityName(t *testing.T) {
	parent := mkGroup("111", "DOMOVINA.ai")
	parent.IsParent = true

	child := mkGroup("222", "DOMOVINA.ai#001")
	child.LinkedParentJID = parent.JID

	orphan := mkGroup("333", "Tudja zajednica")
	orphan.LinkedParentJID = gjid("999")

	got := summarizeGroups([]*types.GroupInfo{child, parent, orphan}, "")
	if len(got) != 3 {
		t.Fatalf("ocekivano 3 grupe, dobiveno %d", len(got))
	}

	byJID := map[string]GroupSummary{}
	for _, g := range got {
		byJID[g.JID] = g
	}

	if c := byJID["222@g.us"]; c.CommunityJID != "111@g.us" || c.CommunityName != "DOMOVINA.ai" {
		t.Errorf("podgrupa: community_jid=%q community_name=%q", c.CommunityJID, c.CommunityName)
	}
	if p := byJID["111@g.us"]; !p.IsCommunity || p.CommunityJID != "" {
		t.Errorf("roditelj: is_community=%v community_jid=%q", p.IsCommunity, p.CommunityJID)
	}
	// Nepoznat roditelj: JID da, ime ne — nikad izmisljeno ime.
	if o := byJID["333@g.us"]; o.CommunityJID != "999@g.us" || o.CommunityName != "" {
		t.Errorf("nepoznat roditelj: community_jid=%q community_name=%q", o.CommunityJID, o.CommunityName)
	}
}

// Samostalna grupa ne smije dobiti nikakav trag zajednice — `omitempty` je
// jedino sto razlikuje "nije u zajednici" od "u zajednici koju ne poznajem".
func TestSummarizeGroupsPlainGroupHasNoCommunity(t *testing.T) {
	got := summarizeGroups([]*types.GroupInfo{mkGroup("444", "Matija Only")}, "")
	if got[0].CommunityJID != "" || got[0].CommunityName != "" || got[0].IsCommunity {
		t.Errorf("samostalna grupa nosi tragove zajednice: %+v", got[0])
	}
}

// Filtar mora hvatati i ime i JID, i ignorirati velicinu slova — inace se
// grupa poput "DOMOVINA.ai#001" ne nade upisom "domovina".
func TestSummarizeGroupsQueryMatchesNameAndJID(t *testing.T) {
	groups := []*types.GroupInfo{
		mkGroup("111", "DOMOVINA.ai#001"),
		mkGroup("222", "Molitveni tim"),
	}

	if got := summarizeGroups(groups, "domovina"); len(got) != 1 || got[0].JID != "111@g.us" {
		t.Errorf("filtar po imenu: %+v", got)
	}
	if got := summarizeGroups(groups, "222@"); len(got) != 1 || got[0].JID != "222@g.us" {
		t.Errorf("filtar po JID-u: %+v", got)
	}
	if got := summarizeGroups(groups, "  "); len(got) != 2 {
		t.Errorf("prazan filtar mora vratiti sve, dobiveno %d", len(got))
	}
}

// ParticipantCount: kad WhatsApp posalje samo broj bez liste sudionika,
// ne smijemo prijaviti nulu.
func TestSummarizeGroupsParticipantCountFallback(t *testing.T) {
	g := mkGroup("555", "Bez liste")
	g.ParticipantCount = 42

	if got := summarizeGroups([]*types.GroupInfo{g}, ""); got[0].ParticipantCount != 42 {
		t.Errorf("ocekivano 42, dobiveno %d", got[0].ParticipantCount)
	}

	withList := mkGroup("556", "S listom")
	withList.Participants = []types.GroupParticipant{{}, {}}
	withList.ParticipantCount = 99 // lista je mjerodavna kad postoji
	if got := summarizeGroups([]*types.GroupInfo{withList}, ""); got[0].ParticipantCount != 2 {
		t.Errorf("ocekivano 2, dobiveno %d", got[0].ParticipantCount)
	}
}

// Poredak mora biti stabilan (po imenu, pa JID-u) jer izlaz cita skripta koja
// bira grupu; dvije grupe istog imena postoje i u stvarnom popisu.
func TestSummarizeGroupsStableOrder(t *testing.T) {
	groups := []*types.GroupInfo{
		mkGroup("777", "DOMOVINA"),
		mkGroup("111", "zadnja"),
		mkGroup("666", "DOMOVINA"),
		mkGroup("222", "Abeceda"),
	}
	got := summarizeGroups(groups, "")
	want := []string{"222@g.us", "666@g.us", "777@g.us", "111@g.us"}
	for i, w := range want {
		if got[i].JID != w {
			t.Fatalf("pozicija %d: ocekivano %s, dobiveno %s (%s)", i, w, got[i].JID, got[i].Name)
		}
	}
}

// Nula vrijednosti ne smiju zavrsiti u JSON-u kao lazni podaci (vrijeme 0001-01-01).
func TestSummarizeGroupsOmitsZeroValues(t *testing.T) {
	g := mkGroup("888", "Gola grupa")
	if got := summarizeGroups([]*types.GroupInfo{g}, ""); got[0].Created != "" || got[0].OwnerJID != "" {
		t.Errorf("nula vrijednosti procurile: created=%q owner=%q", got[0].Created, got[0].OwnerJID)
	}

	g.GroupCreated = time.Date(2026, 9, 22, 17, 30, 0, 0, time.UTC)
	if got := summarizeGroups([]*types.GroupInfo{g}, ""); got[0].Created != "2026-09-22T17:30:00Z" {
		t.Errorf("created=%q", got[0].Created)
	}
}
