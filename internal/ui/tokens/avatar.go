package tokens

import (
	"hash/fnv"
	"image/color"
)

// AvatarColors returns the hues used for identity markers such as the
// project avatar in the header toolbar. They are taken from the theme's
// semantic and syntax colors, so they follow light and dark mode.
func (th *Theme) AvatarColors() []color.NRGBA {
	c, s := th.Color, th.Syntax
	return []color.NRGBA{
		c.Accent, c.Success, c.Warning, c.Danger, c.Info,
		s.Keyword, s.String, s.Function, s.Type, s.Number,
	}
}

// AvatarColor picks a stable color from AvatarColors for key (e.g. a
// project name): the same key always gets the same hue in a mode.
func (th *Theme) AvatarColor(key string) color.NRGBA {
	cs := th.AvatarColors()
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return cs[h.Sum32()%uint32(len(cs))]
}
