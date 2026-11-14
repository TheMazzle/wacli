package wa

import (
	"context"
	"sync"
	"time"

	"go.mau.fi/whatsmeow/types"
)

// minSendGap is de minimale tijd tussen twee berichten naar dezelfde chat.
//
// WhatsApp geeft elk bericht een tijdstempel in hele seconden. Gaan twee
// berichten binnen één seconde de deur uit, dan hebben ze dezelfde tijd en
// bepaalt elke app zelf de volgorde: WhatsApp en Beeper zetten een bericht
// + poll + bericht dan door elkaar (2026-11-14). Met minstens een seconde
// tussen het antwoord van de server en de volgende verzending vallen ze
// altijd in verschillende seconden, ongeacht klokverschil met de server.
const minSendGap = 1050 * time.Millisecond

// sendPacer laat berichten naar één chat na elkaar en met minSendGap ertussen
// vertrekken. Verschillende chats hebben geen last van elkaar.
type sendPacer struct {
	mu    sync.Mutex
	chats map[string]*chatPace
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error
}

type chatPace struct {
	mu       sync.Mutex // één verzending tegelijk per chat
	lastSent time.Time  // moment waarop de server de vorige bevestigde
}

func newSendPacer() *sendPacer {
	return &sendPacer{
		chats: map[string]*chatPace{},
		now:   time.Now,
		sleep: func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		},
	}
}

// do voert send uit voor chat `to`, zo nodig na een korte wachttijd.
func (p *sendPacer) do(ctx context.Context, to types.JID, send func() error) error {
	key := to.ToNonAD().String()
	p.mu.Lock()
	cp := p.chats[key]
	if cp == nil {
		cp = &chatPace{}
		p.chats[key] = cp
	}
	p.mu.Unlock()

	cp.mu.Lock()
	defer cp.mu.Unlock()
	if !cp.lastSent.IsZero() {
		if wait := cp.lastSent.Add(minSendGap).Sub(p.now()); wait > 0 {
			if err := p.sleep(ctx, wait); err != nil {
				return err
			}
		}
	}
	err := send()
	if err == nil {
		cp.lastSent = p.now()
	}
	return err
}
