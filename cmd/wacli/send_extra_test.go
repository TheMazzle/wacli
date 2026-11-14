package main

import (
	"strings"
	"testing"
	"time"
)

func TestDetectMedia(t *testing.T) {
	cases := map[string]string{
		"foto.jpg": "image", "foto.PNG": "image", "clip.mp4": "video", "song.mp3": "audio",
		"menu.pdf": "document", "anim.gif": "document", "logo.svg": "document",
	}
	for name, want := range cases {
		if _, got := detectMedia([]byte("x"), name, ""); got != want {
			t.Errorf("%s: media type %q, want %q", name, got, want)
		}
	}
	// Zonder extensie: inhoud bepaalt het (PNG-handtekening).
	png := []byte("\x89PNG\r\n\x1a\n0000000000000")
	if mime, mt := detectMedia(png, "zonder-extensie", ""); mt != "image" || mime != "image/png" {
		t.Errorf("sniffed %q %q", mime, mt)
	}
}

func TestUploadCacheExpires(t *testing.T) {
	c := &uploadCache{}
	now := time.Now()
	tok := c.put(preparedMedia{Name: "a"}, now)
	if pm, ok := c.get(tok, now.Add(59*time.Minute)); !ok || pm.Name != "a" {
		t.Fatal("upload should still be valid within the hour")
	}
	if _, ok := c.get(tok, now.Add(61*time.Minute)); ok {
		t.Fatal("upload should have expired")
	}
	if _, ok := c.get("nope", now); ok {
		t.Fatal("unknown token")
	}
}

func TestVCard(t *testing.T) {
	v := vCard("Jansen; Piet", "31612345678")
	for _, want := range []string{`FN:Jansen\; Piet`, "waid=31612345678:+31612345678", "BEGIN:VCARD", "END:VCARD"} {
		if !strings.Contains(v, want) {
			t.Errorf("vcard misses %q:\n%s", want, v)
		}
	}
}

func TestValidateLocation(t *testing.T) {
	if err := ValidateLocation(52.0907, 5.1214); err != nil {
		t.Fatal(err)
	}
	for _, c := range [][2]float64{{91, 0}, {0, 181}, {0, 0}} {
		if ValidateLocation(c[0], c[1]) == nil {
			t.Errorf("%v should be invalid", c)
		}
	}
	m := locationMessage(52.09, 5.12, " Tolbar ", "")
	if m.GetLocationMessage().GetName() != "Tolbar" || m.GetLocationMessage().Address != nil {
		t.Errorf("unexpected location %+v", m.GetLocationMessage())
	}
}
