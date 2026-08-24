package wa

import (
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func lid(user string) types.JID {
	return types.JID{User: user, Server: types.HiddenUserServer}
}

func pn(user string) types.JID {
	return types.JID{User: user, Server: types.DefaultUserServer}
}

// Regressie: 40 DM's toonden een rauwe LID als naam ("152136902033557@lid"),
// terwijl de LID->telefoonnummer mapping lokaal gewoon beschikbaar was.
// Een telefoonnummer is herkenbaar, een LID is dat nooit.
func TestChatDisplayName(t *testing.T) {
	cases := []struct {
		name    string
		in      ChatNameInputs
		want    string
		comment string
	}{
		{
			name: "adresboeknaam wint van alles",
			in: ChatNameInputs{
				Chat: lid("152136902033557"), ContactName: "Marieke",
				PushName: "mar", ResolvedPN: pn("31623664613"),
			},
			want: "Marieke",
		},
		{
			name: "contact op het gemapte nummer telt ook",
			in: ChatNameInputs{
				Chat: lid("152136902033557"), ResolvedPN: pn("31623664613"),
				ResolvedPNContactName: "Marieke",
			},
			want: "Marieke",
		},
		{
			name: "pushname als er geen adresboeknaam is",
			in: ChatNameInputs{
				Chat: lid("146776514400399"), PushName: "thepulisan",
				ResolvedPN: pn("6282196659675"),
			},
			want: "thepulisan",
		},
		{
			name: "onbekende LID valt terug op het telefoonnummer, niet op de LID",
			in: ChatNameInputs{
				Chat: lid("152136902033557"), ResolvedPN: pn("31623664613"),
			},
			want: "+31623664613",
		},
		{
			name: "LID zonder mapping houdt de LID; beter dan niets",
			in:   ChatNameInputs{Chat: lid("999"), ResolvedPN: lid("999")},
			want: "999@lid",
		},
		{
			name: "gewone telefoon-JID zonder naam toont het nummer",
			in:   ChatNameInputs{Chat: pn("31612345678"), ResolvedPN: pn("31612345678")},
			want: "+31612345678",
		},
		{
			name: "pushname '-' telt niet als naam",
			in: ChatNameInputs{
				Chat: lid("152136902033557"), PushName: "-", ResolvedPN: pn("31623664613"),
			},
			want: "+31623664613",
		},
		{
			name: "groep gebruikt de groepsnaam",
			in: ChatNameInputs{
				Chat:      types.JID{User: "123", Server: types.GroupServer},
				GroupName: "Amsteldorpkidz",
			},
			want: "Amsteldorpkidz",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ChatDisplayName(tc.in); got != tc.want {
				t.Errorf("ChatDisplayName() = %q, want %q", got, tc.want)
			}
		})
	}
}
