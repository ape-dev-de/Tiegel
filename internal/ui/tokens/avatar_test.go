package tokens

import "testing"

func TestAvatarColor(t *testing.T) {
	for _, th := range []*Theme{Dark(nil), Light(nil)} {
		if got := len(th.AvatarColors()); got < 8 {
			t.Fatalf("%s: %d avatar colors", th.Mode, got)
		}
		if th.AvatarColor("tiegel") != th.AvatarColor("tiegel") {
			t.Fatal("avatar color not stable")
		}
		seen := map[[4]uint8]bool{}
		for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "shop", "api"} {
			c := th.AvatarColor(n)
			seen[[4]uint8{c.R, c.G, c.B, c.A}] = true
			if c.A == 0 {
				t.Fatalf("%s: transparent avatar color for %q", th.Mode, n)
			}
		}
		if len(seen) < 3 {
			t.Fatalf("%s: avatar colors not spread: %d", th.Mode, len(seen))
		}
	}
}
