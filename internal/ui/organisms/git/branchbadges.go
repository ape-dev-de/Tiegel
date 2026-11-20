package git

import (
	"fmt"

	"github.com/ape-dev-de/tiegel/internal/gitcli"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
)

// BranchBadges returns the status badges of a branch row: "current",
// commits ahead (↑n) and behind (↓n) of the upstream, and "gone" for a
// deleted upstream. BranchList and the header toolbar's branch popup
// share it so both show the same markers.
func BranchBadges(br gitcli.Branch) []atoms.Badge {
	var out []atoms.Badge
	if br.Current {
		out = append(out, atoms.Badge{Text: "current", Kind: atoms.BadgeAccent})
	}
	if br.Ahead > 0 {
		out = append(out, atoms.Badge{Text: fmt.Sprintf("↑%d", br.Ahead), Kind: atoms.BadgeSuccess})
	}
	if br.Behind > 0 {
		out = append(out, atoms.Badge{Text: fmt.Sprintf("↓%d", br.Behind), Kind: atoms.BadgeWarning})
	}
	if br.UpstreamGone {
		out = append(out, atoms.Badge{Text: "gone", Kind: atoms.BadgeDanger})
	}
	return out
}

// BranchGlyph returns the glyph of a branch row: a cloud for remote
// branches, the branch glyph otherwise.
func BranchGlyph(br gitcli.Branch) *atoms.Glyph {
	if br.Remote {
		return atoms.GlyphCloud
	}
	return atoms.GlyphBranch
}
