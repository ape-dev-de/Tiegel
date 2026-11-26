package syntax

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

const sampleDockerfile = `# syntax=docker/dockerfile:1.7
ARG PHP_VERSION=8.3
FROM php:${PHP_VERSION}-fpm-alpine AS base
ENV APP_ENV=prod \
    COMPOSER_HOME="/tmp/$APP_ENV"
LABEL org.opencontainers.image.title="tiegel demo"
WORKDIR /app

FROM base AS build
# install build deps
RUN --mount=type=cache,target=/var/cache/apk apk add --no-cache git unzip \
    && docker-php-ext-install pdo_mysql \
    # comment inside a continuation
    && echo "done $PHP_VERSION"
RUN <<EOF
set -eux
if [ -f composer.json ]; then composer install; fi
EOF
COPY <<-CONF /usr/local/etc/php/conf.d/app.ini
	memory_limit = 256M
	CONF

FROM base
COPY --from=build --chown=www-data:www-data /app /app
EXPOSE 9000/tcp 80
HEALTHCHECK --interval=30s CMD php-fpm -t || exit 1
ONBUILD RUN echo onbuild
CMD ["php-fpm", "-F"]
`

// occurrence returns the n-th (0-based) byte offset of sub in src.
func occurrence(src, sub string, n int) int {
	off := 0
	for {
		i := strings.Index(src[off:], sub)
		if i < 0 {
			return -1
		}
		if n == 0 {
			return off + i
		}
		n--
		off += i + len(sub)
	}
}

func roleAtOff(spans []Span, off int) Role {
	for _, s := range spans {
		if s.Start <= off && off < s.End {
			return s.Role
		}
	}
	return RolePlain
}

func TestDockerfileHighlighting(t *testing.T) {
	src := sampleDockerfile
	spans := dockerfileSpans([]byte(src))
	cases := []struct {
		sub  string
		n    int
		want Role
	}{
		{"# syntax=docker", 0, RoleComment},
		{"ARG", 0, RoleKeyword},
		{"PHP_VERSION=", 0, RoleProperty},
		{"=8.3", 0, RoleOperator},
		{"8.3", 0, RoleString},
		{"FROM", 0, RoleKeyword},
		{"php:", 0, RoleType},
		{"${PHP_VERSION}", 0, RoleVariable},
		{"AS base", 0, RoleKeyword},
		{"base\n", 0, RoleConstant},
		{"ENV", 0, RoleKeyword},
		{"APP_ENV=", 0, RoleProperty},
		{"\\\n    COMPOSER", 0, RolePunctuation},
		{"COMPOSER_HOME", 0, RoleProperty}, // continuation keeps the instruction
		{"\"/tmp/", 0, RoleString},
		{"$APP_ENV", 0, RoleVariable},
		{"LABEL", 0, RoleKeyword},
		{"org.opencontainers", 0, RoleProperty},
		{"\"tiegel demo\"", 0, RoleString},
		{"WORKDIR", 0, RoleKeyword},
		{"base AS build", 0, RoleType},
		{"build\n", 0, RoleConstant},
		{"# install", 0, RoleComment},
		{"RUN", 0, RoleKeyword},
		{"--mount", 0, RoleAttribute},
		{"type=cache", 0, RoleString},
		{"no-cache git", 0, RolePlain}, // a shell argument, not a RUN flag
		{"&&", 0, RoleOperator},
		{"docker-php-ext-install", 0, RolePlain},
		{"# comment inside", 0, RoleComment},
		{"\"done $PHP_VERSION\"", 0, RoleString},
		{"$PHP_VERSION\"", 0, RoleVariable}, // variables inside double quotes
		{"<<EOF", 0, RoleKeyword},
		{"set -eux", 0, RoleKeyword},
		{"if [", 0, RoleKeyword},
		{"then", 0, RoleKeyword},
		{"EOF\nCOPY", 0, RoleKeyword},
		{"COPY", 0, RoleKeyword},
		{"<<-CONF", 0, RoleKeyword},
		{"memory_limit", 0, RoleString},
		{"CONF\n\nFROM", 0, RoleKeyword},
		{"--from", 0, RoleAttribute},
		{"build --chown", 0, RoleConstant},
		{"--chown", 0, RoleAttribute},
		{"www-data:", 0, RoleString},
		{"EXPOSE", 0, RoleKeyword},
		{"9000/tcp", 0, RoleNumber},
		{"80\n", 0, RoleNumber},
		{"HEALTHCHECK", 0, RoleKeyword},
		{"--interval", 0, RoleAttribute},
		{"CMD php-fpm", 0, RoleKeyword},
		{"||", 0, RoleOperator},
		{"exit", 0, RoleKeyword},
		{"ONBUILD", 0, RoleKeyword},
		{"RUN echo onbuild", 0, RoleKeyword},
		{"CMD [", 0, RoleKeyword},
		{"[\"php-fpm\"", 0, RolePunctuation},
		{"\"php-fpm\"", 0, RoleString},
		{"\"-F\"", 0, RoleString},
	}
	for _, c := range cases {
		off := occurrence(src, c.sub, c.n)
		if off < 0 {
			t.Fatalf("%q not in sample", c.sub)
		}
		if got := roleAtOff(spans, off); got != c.want {
			t.Errorf("%q is %s, want %s", c.sub, got, c.want)
		}
	}
	for i, s := range spans {
		if s.Start >= s.End || s.Role == RolePlain || i > 0 && s.Start < spans[i-1].End {
			t.Fatalf("bad span %d %v (prev %v)", i, s, spans[max(i-1, 0)])
		}
	}
}

func TestDockerfileDetails(t *testing.T) {
	cases := []struct {
		src, sub string
		want     Role
	}{
		// Lower-case instructions are valid.
		{"from alpine as x\nrun ls", "run", RoleKeyword},
		// "#" in the middle of a non-shell instruction is not a comment.
		{"WORKDIR /a#b", "#b", RolePlain},
		// Instruction words on continuation lines are arguments.
		{"RUN echo \\\n  from", "from", RolePlain},
		// The escape directive changes the continuation character.
		{"# escape=`\nRUN dir `\n  copy", "copy", RolePlain},
		{"# escape=`\nRUN dir \\\nCOPY a b", "COPY", RoleKeyword},
		// Blank lines inside a continuation do not end it.
		{"ENV A=1 \\\n\n    B=2", "B", RoleProperty},
		// Legacy ENV form.
		{"ENV KEY some value", "some", RoleString},
		{"ENV KEY some value", "KEY", RoleProperty},
		// Here-strings are not heredocs.
		{"RUN cat <<<EOF\nFROM x", "FROM", RoleKeyword},
		// Mismatched quotes are not heredocs.
		{"RUN cat <<\"EOF'\nFROM x", "FROM", RoleKeyword},
		// Quoted heredoc marker.
		{"RUN <<'EOT' bash\necho $X\nEOT\nFROM x", "$X", RoleVariable},
		{"RUN <<'EOT' bash\necho $X\nEOT\nFROM x", "FROM", RoleKeyword},
		{"RUN echo '$A' \"$B\"", "$A", RoleString},
		{"RUN echo '$A' \"$B\"", "$B", RoleVariable},
		// Two heredocs on one line.
		{"COPY <<A <<B /dst/\na\nA\nb\nB\nRUN x", "RUN", RoleKeyword},
		{"COPY <<A <<B /dst/\na\nA\nb\nB\nRUN x", "b\n", RoleString},
		{"HEALTHCHECK NONE", "NONE", RoleKeyword},
		{"HEALTHCHECK CMD [\"curl\", \"-f\"]", "\"curl\"", RoleString},
		{"ENTRYPOINT [\"docker-entrypoint\"]\nSTOPSIGNAL SIGQUIT", "STOPSIGNAL", RoleKeyword},
		{"FROM --platform=$BUILDPLATFORM golang:1.23", "$BUILDPLATFORM", RoleVariable},
		{"FROM --platform=$BUILDPLATFORM golang:1.23", "golang", RoleType},
		{"LABEL \"com.example.vendor\"=\"ACME\" version=1.0", "version", RoleProperty},
		{"ARG NAME", "NAME", RoleProperty},
		{"USER 'www'", "'www'", RoleString},
		{"VOLUME [\"/data\"]", "\"/data\"", RoleString},
		{"COPY a\\$b c", "$b", RolePlain},
		{"UNKNOWN thing", "UNKNOWN", RolePlain},
		{"RUN echo a\r\nCMD x\r\n", "CMD", RoleKeyword},
	}
	for _, c := range cases {
		spans := dockerfileSpans([]byte(c.src))
		if got := roleAtOff(spans, strings.LastIndex(c.src, c.sub)); got != c.want {
			t.Errorf("%q: %q is %s, want %s (%v)", c.src, c.sub, got, c.want, spans)
		}
	}
}

func TestLexedDocument(t *testing.T) {
	ctx := context.Background()
	d, err := NewDocument(ctx, LangDockerfile, []byte(sampleDockerfile))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.Language() != LangDockerfile || !LangDockerfile.Lexed() || LangDockerfile.HasTree() || LangPHP.Lexed() {
		t.Fatal("language flags")
	}
	// Highlights tiles any window, also one starting inside a span.
	start, end := occurrence(sampleDockerfile, "OSER_HOME", 0), occurrence(sampleDockerfile, "WORKDIR", 0)
	hs := d.Highlights(start, end)
	if hs[0].Start != start || hs[len(hs)-1].End != end || hs[0].Role != RoleProperty {
		t.Fatalf("window %v", hs)
	}
	for i := 1; i < len(hs); i++ {
		if hs[i].Start != hs[i-1].End {
			t.Fatalf("gap %v %v", hs[i-1], hs[i])
		}
	}
	// Reparse re-tokenizes.
	if err := d.Reparse(ctx, []byte("RUN ls")); err != nil {
		t.Fatal(err)
	}
	if hs := d.Highlights(0, 6); len(hs) != 2 || hs[0].Role != RoleKeyword || hs[1].Role != RolePlain {
		t.Fatalf("after reparse %v", hs)
	}
	if d.Highlights(5, 5) != nil {
		t.Fatal("empty range")
	}
	d.Close()
	if d.Reparse(ctx, nil) != ErrClosed || d.Highlights(0, 1) != nil {
		t.Fatal("closed")
	}
}

func TestLexedLanguages(t *testing.T) {
	cases := []struct {
		lang      Language
		src, sub  string
		want      Role
		occurence int
	}{
		{LangShell, "#!/bin/sh\nif [ -n \"$X\" ]; then echo ${Y}; fi", "then", RoleKeyword, 0},
		{LangShell, "#!/bin/sh\necho hi", "#!/bin/sh", RoleComment, 0},
		{LangMake, ".PHONY: test\ntest: vendor\n\t$(PHP) vendor/bin/phpunit\n", ".PHONY", RoleFunction, 0},
		{LangMake, ".PHONY: test\ntest: vendor\n\t$(PHP) vendor/bin/phpunit\n", "test:", RoleFunction, 0},
		{LangMake, "PHP ?= php\nX := $(shell $(PHP) -v)\n", "PHP", RoleProperty, 0},
		{LangMake, "PHP ?= php\nX := $(shell $(PHP) -v)\n", "$(shell $(PHP) -v)", RoleVariable, 0},
		{LangMake, "ifeq ($(CI),1)\nendif # c\n", "ifeq", RoleKeyword, 0},
		{LangMake, "ifeq ($(CI),1)\nendif # c\n", "# c", RoleComment, 0},
		{LangMake, "%.o: %.c\n", "%.o", RoleFunction, 0},
		{LangINI, "[PHP]\nmemory_limit = 128M\n; c", "memory_limit", RoleProperty, 0},
		{LangSQL, "select * from t -- c", "from", RoleKeyword, 0},
		{LangIgnore, "# c\nvendor/\n!keep.txt\n*.log\n\\#file", "# c", RoleComment, 0},
		{LangIgnore, "# c\nvendor/\n!keep.txt\n*.log\n\\#file", "!", RoleOperator, 0},
		{LangIgnore, "# c\nvendor/\n!keep.txt\n*.log\n\\#file", "*", RoleOperator, 0},
		{LangIgnore, "# c\nvendor/\n!keep.txt\n*.log\n\\#file", "vendor", RolePlain, 0},
		{LangIgnore, "# c\nvendor/\n!keep.txt\n*.log\n\\#file", "\\#", RoleString, 0},
		{LangIgnore, "a[0-9]", "[", RolePunctuation, 0},
	}
	for _, c := range cases {
		d, err := NewDocument(context.Background(), c.lang, []byte(c.src))
		if err != nil {
			t.Fatal(err)
		}
		hs := d.Highlights(0, len(c.src))
		d.Close()
		off := occurrence(c.src, c.sub, c.occurence)
		if got := roleAtOff(hs, off); got != c.want {
			t.Errorf("%s %q: %q is %s, want %s (%s)", c.lang, c.src, c.sub, got, c.want, dump(c.src, hs))
		}
	}
}

func TestTile(t *testing.T) {
	spans := []Span{{2, 4, RoleKeyword}, {6, 9, RoleString}}
	got := fmt.Sprint(tile(spans, 3, 8))
	if want := fmt.Sprint([]Span{{3, 4, RoleKeyword}, {4, 6, RolePlain}, {6, 8, RoleString}}); got != want {
		t.Fatalf("tile %s, want %s", got, want)
	}
	if got := tile(nil, 0, 3); len(got) != 1 || got[0].Role != RolePlain {
		t.Fatalf("empty %v", got)
	}
	if lex(LangPHP, []byte("x")) != nil {
		t.Fatal("lex of a tree language")
	}
}

func dump(src string, spans []Span) string {
	var b strings.Builder
	for _, s := range spans {
		if s.Role != RolePlain {
			fmt.Fprintf(&b, "%s%q ", s.Role, src[s.Start:s.End])
		}
	}
	return b.String()
}

// BenchmarkDockerfileLexer measures the full re-lex done per Reparse.
func BenchmarkDockerfileLexer(b *testing.B) {
	src := []byte(strings.Repeat(sampleDockerfile, 200)) // ~200 KB
	b.SetBytes(int64(len(src)))
	for b.Loop() {
		dockerfileSpans(src)
	}
}

// TestLexedNoPanics feeds every prefix of some tricky inputs and of
// pseudo-random text to all lexed languages; spans must stay sorted,
// non-overlapping and in range.
func TestLexedNoPanics(t *testing.T) {
	inputs := []string{sampleDockerfile, "RUN <<", "FROM", "ENV A=\"x", "LABEL \"k", "RUN [\"a", "X := $(a", "\\", "COPY <<-E\n\tE"}
	const alphabet = " \t\n\\$\"'#<-=[]{}()aAzZ09:,`!*"
	seed := uint32(1)
	for n := 0; n < 200; n++ {
		b := make([]byte, 40)
		for i := range b {
			seed = seed*1664525 + 1013904223
			b[i] = alphabet[int(seed>>24)%len(alphabet)]
		}
		inputs = append(inputs, "RUN "+string(b), string(b))
	}
	for _, in := range inputs {
		for cut := 0; cut <= len(in); cut++ {
			src := []byte(in[:cut])
			for lang := range lexedLanguages {
				spans := lex(lang, src)
				for i, s := range spans {
					if s.Start < 0 || s.End > len(src) || s.Start >= s.End || i > 0 && s.Start < spans[i-1].End {
						t.Fatalf("%s %q: bad span %v", lang, src, s)
					}
				}
			}
		}
	}
}
