package atoms

import (
	"image/color"

	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// BadgeColors exposes Badge.colors to external tests.
func BadgeColors(b Badge, th *tokens.Theme) (bg, fg color.NRGBA) { return b.colors(th) }
