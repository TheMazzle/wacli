package wa

import (
	"strings"

	"go.mau.fi/whatsmeow/types"
)

// ChatNameInputs bundelt alles wat nodig is om een chatnaam te kiezen.
//
// Los van de I/O gehouden zodat de keuze testbaar is. De aanleiding: 40 DM's
// toonden een rauwe LID ("152136902033557@lid") als naam, terwijl de
// LID->telefoonnummer mapping lokaal gewoon beschikbaar was. De oude code riep
// ResolveLIDToPN in dit pad simpelweg nooit aan.
type ChatNameInputs struct {
	// Chat is de JID van het gesprek.
	Chat types.JID
	// GroupName is de naam uit de groepsinfo, leeg voor DM's.
	GroupName string
	// ContactName is de naam uit het adresboek voor Chat zelf.
	ContactName string
	// PushName is de door de afzender zelf ingestelde naam.
	PushName string
	// ResolvedPN is Chat omgezet naar een telefoonnummer-JID; gelijk aan Chat
	// als het geen LID is of er geen mapping bestaat.
	ResolvedPN types.JID
	// ResolvedPNContactName is de adresboeknaam onder ResolvedPN. Een LID en het
	// bijbehorende telefoonnummer zijn aparte contactrijen; de naam kan onder de
	// ene staan en niet onder de andere.
	ResolvedPNContactName string
}

// ChatDisplayName kiest de best beschikbare weergavenaam.
//
// Volgorde: groepsnaam, adresboeknaam (op de chat zelf of op het gemapte
// nummer), pushname, telefoonnummer, en pas als laatste de rauwe JID.
func ChatDisplayName(in ChatNameInputs) string {
	if name := clean(in.GroupName); name != "" {
		return name
	}
	if name := clean(in.ContactName); name != "" {
		return name
	}
	if name := clean(in.ResolvedPNContactName); name != "" {
		return name
	}
	if name := clean(in.PushName); name != "" {
		return name
	}

	// Geen naam bekend. Een telefoonnummer is herkenbaar — een LID nooit.
	if in.ResolvedPN.Server == types.DefaultUserServer && in.ResolvedPN.User != "" {
		return "+" + in.ResolvedPN.User
	}

	return in.Chat.String()
}

// clean normaliseert een naam; "-" is WhatsApp's lege pushname.
func clean(s string) string {
	s = strings.TrimSpace(s)
	if s == "-" {
		return ""
	}
	return s
}
