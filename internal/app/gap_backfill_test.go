package app

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/steipete/wacli/internal/store"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// Regressie 2026-10-11: bij elke (her)start vroeg wacli voor dezelfde 20
// gesprekken geschiedenis op bij de telefoon. De gaten raakten nooit gevuld,
// de afkoeltijd stond alleen in het geheugen, en de telefoon toonde bij elk
// verzoek een "syncing on another device"-notificatie.

// seedGap zet twee geschiedenisberichten met een gat van een dag in een gesprek.
func seedGap(t *testing.T, a *App, chat string) {
	t.Helper()
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	if err := a.db.UpsertChat(chat, "group", chat, base); err != nil {
		t.Fatalf("UpsertChat: %v", err)
	}
	for i, ts := range []time.Time{base, base.Add(24 * time.Hour)} {
		if err := a.db.UpsertMessage(store.UpsertMessageParams{
			ChatJID: chat, MsgID: chat + "-" + string(rune('a'+i)), SenderJID: "1@lid",
			Timestamp: ts, Text: "x",
		}); err != nil {
			t.Fatalf("UpsertMessage: %v", err)
		}
	}
}

func gapTestApp(t *testing.T) (*App, *atomic.Int32) {
	t.Helper()
	old := backfillRequestDelay
	backfillRequestDelay = 0
	t.Cleanup(func() { backfillRequestDelay = old })

	a := newTestApp(t)
	f := newFakeWA()
	var requests atomic.Int32
	f.onDemandHistory = func(types.MessageInfo, int) *events.HistorySync {
		requests.Add(1)
		return &events.HistorySync{Data: &waHistorySync.HistorySync{}}
	}
	a.wa = f
	return a, &requests
}

// restart bootst een herstart van de daemon na: het geheugen is leeg, de database niet.
func restart(a *App) { a.lastGapBackfill.Store(0) }

func TestGapBackfillDoesNotRepeatAfterRestart(t *testing.T) {
	a, requests := gapTestApp(t)
	seedGap(t, a, "g1@g.us")

	a.backfillDetectedGaps(context.Background())
	if got := requests.Load(); got != 1 {
		t.Fatalf("eerste run: %d verzoeken, verwachtte 1", got)
	}

	restart(a)
	a.backfillDetectedGaps(context.Background())

	if got := requests.Load(); got != 1 {
		t.Fatalf("na herstart: %d verzoeken, verwachtte dat de afkoeltijd een tweede run tegenhoudt", got)
	}
}

func TestGapBackfillAsksOncePerGap(t *testing.T) {
	a, requests := gapTestApp(t)
	seedGap(t, a, "g1@g.us")
	// Afkoeltijd voorbij, maar het gat is twee dagen geleden al opgevraagd en niet gevuld.
	gapEnd := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC).Unix()
	if err := a.db.RecordBackfillAttempt("g1@g.us", gapEnd, time.Now().Add(-48*time.Hour)); err != nil {
		t.Fatalf("RecordBackfillAttempt: %v", err)
	}

	a.backfillDetectedGaps(context.Background())

	if got := requests.Load(); got != 0 {
		t.Fatalf("%d verzoeken, verwachtte dat hetzelfde gat niet opnieuw wordt opgevraagd", got)
	}
}

// ageAttempts doet alsof de tot nu toe gedane verzoeken `age` geleden waren.
func ageAttempts(t *testing.T, a *App, age time.Duration) {
	t.Helper()
	gapEnd := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC).Unix()
	for _, chat := range []string{"g1@g.us", "g2@g.us"} {
		tried, err := a.db.BackfillAttemptedSince(chat, gapEnd, time.Unix(0, 0))
		if err != nil {
			t.Fatalf("BackfillAttemptedSince: %v", err)
		}
		if tried {
			if err := a.db.RecordBackfillAttempt(chat, gapEnd, time.Now().Add(-age)); err != nil {
				t.Fatalf("RecordBackfillAttempt: %v", err)
			}
		}
	}
}

// WJ 2026-10-12: één melding per dag op de telefoon is acceptabel, meer niet.
func TestGapBackfillIsAtMostOneRequestPerDay(t *testing.T) {
	a, requests := gapTestApp(t)
	seedGap(t, a, "g1@g.us")
	seedGap(t, a, "g2@g.us")

	a.backfillDetectedGaps(context.Background())
	if got := requests.Load(); got != 1 {
		t.Fatalf("eerste run: %d verzoeken, verwachtte 1", got)
	}

	// 23 uur later, na een herstart: nog niet.
	ageAttempts(t, a, 23*time.Hour)
	restart(a)
	a.backfillDetectedGaps(context.Background())
	if got := requests.Load(); got != 1 {
		t.Fatalf("na 23 uur: %d verzoeken, verwachtte nog steeds 1", got)
	}

	// 25 uur later: het volgende gat is aan de beurt.
	ageAttempts(t, a, 25*time.Hour)
	restart(a)
	a.backfillDetectedGaps(context.Background())
	if got := requests.Load(); got != 2 {
		t.Fatalf("na 25 uur: %d verzoeken, verwachtte 2", got)
	}
}

func TestGapBackfillCapsRequestsPerRun(t *testing.T) {
	a, requests := gapTestApp(t)
	for _, c := range []string{"a@g.us", "b@g.us", "c@g.us", "d@g.us", "e@g.us", "f@g.us", "g@g.us", "h@g.us"} {
		seedGap(t, a, c)
	}

	a.backfillDetectedGaps(context.Background())

	if got := requests.Load(); got != maxBackfillRequests {
		t.Fatalf("%d verzoeken in één run, verwachtte het maximum van %d", got, maxBackfillRequests)
	}
}
