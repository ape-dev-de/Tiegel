package editorview

import (
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/editor/highlight"
	"github.com/ape-dev-de/tiegel/internal/editor/highlight/tshighlight"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

const sampleDockerfile = `# syntax=docker/dockerfile:1.7
ARG PHP_VERSION=8.3
FROM php:${PHP_VERSION}-fpm-alpine AS base
ENV APP_ENV=prod \
    COMPOSER_HOME="/tmp/composer"
WORKDIR /app

FROM base AS build
# install build dependencies
RUN --mount=type=cache,target=/var/cache/apk apk add git unzip \
    && docker-php-ext-install pdo_mysql \
    && echo "PHP $PHP_VERSION ready"
RUN <<EOF
set -eux
if [ -f composer.json ]; then composer install --no-dev; fi
EOF

FROM base
COPY --from=build --chown=www-data:www-data /app /app
EXPOSE 9000/tcp
HEALTHCHECK --interval=30s CMD php-fpm -t || exit 1
CMD ["php-fpm", "-F"]
`

// TestRenderDockerfile draws a Dockerfile highlighted by the lexer path
// (tshighlight with a lexed language) in both themes. With
// TIEGEL_SHOTS=<dir> the PNGs are written for visual review.
func TestRenderDockerfile(t *testing.T) {
	dir := os.Getenv("TIEGEL_SHOTS")
	size := image.Pt(900, 560)
	for _, th := range []*tokens.Theme{tokens.Dark(nil), tokens.Light(nil)} {
		e := New(buffer.NewString(sampleDockerfile))
		hl := tshighlight.New(e.Buffer(), tshighlight.Options{Path: "Dockerfile"})
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := hl.Wait(ctx); err != nil {
			cancel()
			t.Fatal(err)
		}
		cancel()
		e.Highlighter = hl
		e.LineComment = LineComments["dockerfile"]
		e.SetFoldRanges(highlight.BraceFolds(e.Buffer().Snapshot()))
		img, err := uitest.Render(th, size, func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
			return e.Layout(gtx, th)
		})
		hl.Close()
		e.Close()
		if err != nil {
			t.Skipf("headless rendering unavailable: %v", err)
		}
		for _, role := range []tokens.SyntaxRole{tokens.RoleKeyword, tokens.RoleVariable, tokens.RoleString, tokens.RoleComment, tokens.RoleType} {
			if n := countNear(img, th.SyntaxColor(role)); n < 10 {
				t.Errorf("%s: %s color barely visible (%d px)", th.Mode, role, n)
			}
		}
		if dir != "" {
			f, err := os.Create(filepath.Join(dir, "editor-dockerfile-"+th.Mode.String()+".png"))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, img); err != nil {
				t.Fatal(err)
			}
			f.Close()
		}
	}
}
