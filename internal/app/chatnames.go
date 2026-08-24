package app

import (
	"context"
	"fmt"
	"os"

	"go.mau.fi/whatsmeow/types"
)

// repairChatNames herstelt chats waarvan de naam nooit is opgelost.
//
// Achtergrond: ResolveChatName riep ResolveLIDToPN niet aan, waardoor 40 DM's
// een rauwe LID als naam kregen ("152136902033557@lid") terwijl de mapping naar
// het telefoonnummer lokaal beschikbaar was. De code is gefixt, maar bestaande
// rijen houden hun oude naam. Deze pas draait bij elke connect en repareert ze.
//
// Raakt alleen rijen zonder echte naam, dus een goede naam wordt nooit
// overschreven. Loopt stil door bij fouten: een mislukte naamsherstelpoging mag
// de sync niet tegenhouden.
func (a *App) repairChatNames(ctx context.Context) {
	chats, err := a.db.ChatsWithUnresolvedNames()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[names] kon chats niet lezen: %v\n", err)
		return
	}
	if len(chats) == 0 {
		return
	}

	repaired := 0
	for _, chat := range chats {
		select {
		case <-ctx.Done():
			return
		default:
		}

		jid, err := types.ParseJID(chat.JID)
		if err != nil {
			continue
		}

		// Voor groepen eerst de lokale groups-tabel: ResolveChatName doet daar
		// een netwerkronde per groep, wat bij honderden groepen op rate limits
		// stukloopt terwijl de naam lokaal al bekend is.
		name := ""
		if jid.Server == types.GroupServer {
			name = a.db.LocalGroupName(chat.JID)
		}
		if name == "" {
			name = a.wa.ResolveChatName(ctx, jid, "")
		}
		if name == "" || name == chat.Name {
			continue
		}
		if err := a.db.SetChatName(chat.JID, name); err != nil {
			continue
		}
		repaired++
	}

	if repaired > 0 {
		fmt.Fprintf(os.Stderr, "[names] %d van %d chatnamen alsnog opgelost.\n", repaired, len(chats))
	}
}
