package phpstan

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// PHPStan's result cache (resultCache.php in its tmpDir) holds the errors
// of the last analysis per file. Two formats exist (verified with PHPStan
// 1.12, 2.1 and 2.2):
//
//   - before 2.2: a PHP file returning an array written with var_export():
//     'errorsCallback' => static function (): array { return array(
//     '/abs/File.php' => array(0 => \PHPStan\Analyser\Error::__set_state(
//     array('message' => …, 'line' => …, 'identifier' => …, 'tip' => …,
//     'filePath' => …, 'traitFilePath' => …))))) – very old versions use
//     'errors' => array(…) instead of the callback. Paths are absolute.
//   - 2.2 and later: "<?php return; ?>\n" followed by frames
//     "<name> <length>\n<serialize() payload>"; array sections are written
//     entry by entry as "<name>* <count>\n" followed by count chunks
//     "<length>\n<serialize(array(key => value))>". The errors section is
//     "errors*"; Error objects have private properties (filePath, nodeLine,
//     nodeType). Paths are relative to PHPStan's %rootDir% (the
//     phpstan/phpstan package, i.e. "../../../src/File.php" for
//     vendor/phpstan/phpstan) unless they share no prefix with it.
//
// The cache holds the errors before ignoreErrors (and the baseline) are
// applied and without errors of collector based rules, which PHPStan only
// computes at the end of a run; see Ignore.

// Cache is the content of a result cache relevant to Tiegel.
type Cache struct {
	// Format is "serialized" (2.2+) or "var_export".
	Format string
	// Version is the PHPStan version that wrote the cache; CacheVersion
	// its cache format version.
	Version      string
	CacheVersion string
	// Analysed is the time of the last full analysis (zero if unknown).
	Analysed time.Time
	Errors   []Raw
}

// Cache formats.
const (
	CacheSerialized = "serialized"
	CacheVarExport  = "var_export"
)

// serializedPrefix starts a 2.2+ cache file.
const serializedPrefix = "<?php return; ?>"

// ReadResultCache reads a result cache file. Only the header sections and
// the errors are read; the (large) collected data and exported nodes are
// skipped without being parsed or kept in memory.
func ReadResultCache(path string) (Cache, error) {
	f, err := os.Open(path)
	if err != nil {
		return Cache{}, fmt.Errorf("result cache: %w", err)
	}
	defer f.Close()
	c, err := ParseResultCache(f)
	if err != nil {
		return c, fmt.Errorf("result cache %s: %w", filepath.Base(path), err)
	}
	return c, nil
}

// ParseResultCache reads a result cache from r (see ReadResultCache).
func ParseResultCache(r io.Reader) (Cache, error) {
	br := bufio.NewReaderSize(r, 1<<16)
	head, err := br.Peek(len(serializedPrefix))
	if err != nil && len(head) == 0 {
		return Cache{}, errors.New("empty file")
	}
	if string(head) == serializedPrefix {
		return parseSerializedCache(br)
	}
	return parseVarExportCache(br)
}

// maxFrame bounds a frame read into memory (one errors chunk is one file;
// header frames are a few MB at most).
const maxFrame = 256 << 20

func readLine(br *bufio.Reader) (string, error) {
	s, err := br.ReadString('\n')
	if err != nil {
		if err == io.EOF && s != "" {
			return strings.TrimRight(s, "\r\n"), nil
		}
		return "", err
	}
	return strings.TrimRight(s, "\r\n"), nil
}

func parseSerializedCache(br *bufio.Reader) (Cache, error) {
	c := Cache{Format: CacheSerialized}
	if _, err := readLine(br); err != nil {
		return c, err
	}
	readFrame := func(n int64) ([]byte, error) {
		if n < 0 || n > maxFrame {
			return nil, fmt.Errorf("frame of %d bytes", n)
		}
		b := make([]byte, n)
		_, err := io.ReadFull(br, b)
		return b, err
	}
	for {
		hdr, err := readLine(br)
		if err == io.EOF {
			return c, nil
		}
		if err != nil {
			return c, err
		}
		i := strings.LastIndexByte(hdr, ' ')
		if i < 0 {
			return c, fmt.Errorf("bad frame header %q", truncate(hdr, 40))
		}
		name := hdr[:i]
		n, err := strconv.ParseInt(hdr[i+1:], 10, 64)
		if err != nil {
			return c, fmt.Errorf("bad frame header %q", truncate(hdr, 40))
		}
		if !strings.HasSuffix(name, "*") {
			switch name {
			case "lastFullAnalysisTime", "meta":
				b, err := readFrame(n)
				if err != nil {
					return c, err
				}
				v, err := Unserialize(b)
				if err != nil {
					return c, fmt.Errorf("%s: %w", name, err)
				}
				if name == "meta" {
					c.Version, c.CacheVersion = str(field(v, "phpstanVersion")), str(field(v, "cacheVersion"))
				} else if t := num(v); t > 0 {
					c.Analysed = time.Unix(int64(t), 0)
				}
			default:
				if _, err := br.Discard(int(n)); err != nil {
					return c, err
				}
			}
			continue
		}
		name = strings.TrimSuffix(name, "*")
		for k := int64(0); k < n; k++ {
			l, err := readLine(br)
			if err != nil {
				return c, fmt.Errorf("%s: %w", name, err)
			}
			cl, err := strconv.ParseInt(l, 10, 64)
			if err != nil {
				return c, fmt.Errorf("%s: bad chunk length %q", name, truncate(l, 20))
			}
			if name != "errors" {
				if cl < 0 {
					return c, fmt.Errorf("%s: bad chunk length", name)
				}
				if _, err := br.Discard(int(cl)); err != nil {
					return c, err
				}
				continue
			}
			b, err := readFrame(cl)
			if err != nil {
				return c, err
			}
			v, err := Unserialize(b)
			if err != nil {
				return c, fmt.Errorf("errors: %w", err)
			}
			c.Errors = append(c.Errors, errorsOf(v)...)
		}
		if name == "errors" {
			// Everything after the errors is of no interest.
			return c, nil
		}
	}
}

var (
	reVersion      = regexp.MustCompile(`'phpstanVersion' => '([^']*)'`)
	reCacheVersion = regexp.MustCompile(`'cacheVersion' => '([^']*)'`)
	reAnalysed     = regexp.MustCompile(`^\s*'lastFullAnalysisTime' => (\d+),`)
)

// parseVarExportCache reads the var_export() format line by line: the
// header for versions, then the errors entry up to the next top-level
// key. Top-level keys are the only lines starting with a tab and a quote
// (var_export indents with spaces).
func parseVarExportCache(br *bufio.Reader) (Cache, error) {
	c := Cache{Format: CacheVarExport}
	var expr bytes.Buffer
	in := false
	callback := false
	for {
		line, err := br.ReadString('\n')
		if line == "" && err != nil {
			break
		}
		if strings.HasPrefix(line, "\t'") {
			if in {
				break // the next section
			}
			key := line[2:]
			if i := strings.IndexByte(key, '\''); i >= 0 {
				key = key[:i]
			}
			if m := reAnalysed.FindStringSubmatch(line); m != nil {
				if t, err := strconv.ParseInt(m[1], 10, 64); err == nil {
					c.Analysed = time.Unix(t, 0)
				}
			}
			if key == "errorsCallback" || key == "errors" {
				in, callback = true, key == "errorsCallback"
				rest := line[strings.Index(line, "=>")+2:]
				if callback {
					i := strings.Index(rest, "return ")
					if i < 0 {
						return c, errors.New("errorsCallback without return")
					}
					rest = rest[i+len("return "):]
				}
				expr.WriteString(rest)
			}
		} else if in {
			expr.WriteString(line)
		} else if c.Version == "" || c.CacheVersion == "" {
			if m := reVersion.FindStringSubmatch(line); m != nil {
				c.Version = m[1]
			}
			if m := reCacheVersion.FindStringSubmatch(line); m != nil {
				c.CacheVersion = m[1]
			}
		}
		if err != nil {
			break
		}
	}
	if !in {
		if c.Version == "" {
			return c, errors.New("not a PHPStan result cache")
		}
		return c, nil
	}
	src := bytes.TrimSpace(expr.Bytes())
	src = bytes.TrimSuffix(src, []byte(","))
	if callback {
		src = bytes.TrimSpace(bytes.TrimSuffix(bytes.TrimSpace(src), []byte("}")))
	}
	src = bytes.TrimSuffix(bytes.TrimSpace(src), []byte(";"))
	v, err := ParseLiteral(src, "")
	if err != nil {
		return c, fmt.Errorf("errors: %w", err)
	}
	c.Errors = errorsOf(v)
	return c, nil
}

// errorsOf converts the errors array (file → list of Error objects).
func errorsOf(v any) []Raw {
	files, _ := v.(*Array)
	if files == nil {
		return nil
	}
	var out []Raw
	for i, k := range files.Keys {
		list, _ := files.Values[i].(*Array)
		if list == nil {
			continue
		}
		key := str(k)
		for _, e := range list.Values {
			r := Raw{
				Message:    str(field(e, "message")),
				Line:       num(field(e, "line")),
				Identifier: str(field(e, "identifier")),
				Tip:        str(field(e, "tip")),
				Ignorable:  true,
				FilePath:   str(field(e, "filePath")),
				TraitPath:  str(field(e, "traitFilePath")),
			}
			if b, ok := field(e, "canBeIgnored").(bool); ok {
				r.Ignorable = b
			}
			if r.FilePath == "" {
				r.FilePath = StripContext(str(field(e, "file")))
			}
			if r.FilePath == "" {
				r.FilePath = key
			}
			r.Path = r.FilePath
			if r.TraitPath != "" {
				r.Path = r.TraitPath
			}
			if r.Message != "" {
				out = append(out, r)
			}
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
