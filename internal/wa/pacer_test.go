package wa

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
)

// Nep-klok: slapen schuift de tijd op, zodat de test niet echt wacht.
func fakePacer() (*sendPacer, *time.Time, *[]time.Duration) {
	now := time.Date(2026, 11, 14, 12, 28, 1, 0, time.UTC)
	var slept []time.Duration
	p := newSendPacer()
	p.now = func() time.Time { return now }
	p.sleep = func(ctx context.Context, d time.Duration) error {
		slept = append(slept, d)
		now = now.Add(d)
		return nil
	}
	return p, &now, &slept
}

func TestPacerSpacesMessagesToSameChat(t *testing.T) {
	p, now, slept := fakePacer()
	chat := types.JID{User: "31615829315", Server: types.DefaultUserServer}
	var sentAt []time.Time
	for i := 0; i < 3; i++ {
		_ = p.do(context.Background(), chat, func() error {
			sentAt = append(sentAt, *now)
			*now = now.Add(150 * time.Millisecond) // rondreis naar de server
			return nil
		})
	}
	for i := 1; i < len(sentAt); i++ {
		if sentAt[i].Unix() == sentAt[i-1].Unix() {
			t.Fatalf("message %d left in the same second as %d: %v", i, i-1, sentAt)
		}
	}
	if len(*slept) != 2 {
		t.Fatalf("slept %d times, want 2", len(*slept))
	}
}

func TestPacerDoesNotDelayOtherChatsOrIdleChats(t *testing.T) {
	p, now, slept := fakePacer()
	a := types.JID{User: "a", Server: types.GroupServer}
	b := types.JID{User: "b", Server: types.GroupServer}
	_ = p.do(context.Background(), a, func() error { return nil })
	_ = p.do(context.Background(), b, func() error { return nil })
	*now = now.Add(2 * time.Second)
	_ = p.do(context.Background(), a, func() error { return nil })
	if len(*slept) != 0 {
		t.Fatalf("unexpected waits %v", *slept)
	}
}

func TestPacerFailedSendDoesNotDelayNext(t *testing.T) {
	p, _, slept := fakePacer()
	a := types.JID{User: "a", Server: types.GroupServer}
	_ = p.do(context.Background(), a, func() error { return context.DeadlineExceeded })
	_ = p.do(context.Background(), a, func() error { return nil })
	if len(*slept) != 0 {
		t.Fatalf("a failed send must not count: %v", *slept)
	}
}

// Gelijktijdige verzoeken naar één chat (twee IPC-aanroepen) gaan na elkaar.
func TestPacerSerialisesConcurrentSends(t *testing.T) {
	p := newSendPacer()
	p.sleep = func(ctx context.Context, d time.Duration) error { return nil }
	chat := types.JID{User: "a", Server: types.GroupServer}
	var mu sync.Mutex
	inFlight, maxInFlight := 0, 0
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = p.do(context.Background(), chat, func() error {
				mu.Lock()
				inFlight++
				if inFlight > maxInFlight {
					maxInFlight = inFlight
				}
				mu.Unlock()
				time.Sleep(5 * time.Millisecond)
				mu.Lock()
				inFlight--
				mu.Unlock()
				return nil
			})
		}()
	}
	wg.Wait()
	if maxInFlight != 1 {
		t.Fatalf("max %d sends in flight to one chat, want 1", maxInFlight)
	}
}
