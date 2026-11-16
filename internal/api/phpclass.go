package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// PSR4Mapping is one autoload entry of composer.json: namespace prefix →
// directory (project relative, slash separated, "" for the root).
type PSR4Mapping struct {
	Prefix string `json:"prefix"`
	Dir    string `json:"dir"`
	Dev    bool   `json:"dev,omitempty"`
}

// readPSR4 reads the psr-4 mappings of autoload and autoload-dev from
// composer.json in root. A missing file gives no mappings.
func readPSR4(root string) ([]PSR4Mapping, error) {
	data, err := os.ReadFile(filepath.Join(root, "composer.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, Errorf(CodeFailed, "composer.json: %w", err)
	}
	type autoload struct {
		PSR4 map[string]json.RawMessage `json:"psr-4"`
	}
	var c struct {
		Autoload    autoload `json:"autoload"`
		AutoloadDev autoload `json:"autoload-dev"`
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, Errorf(CodeFailed, "composer.json: %v", err)
	}
	var out []PSR4Mapping
	for _, sec := range []struct {
		m   map[string]json.RawMessage
		dev bool
	}{{c.Autoload.PSR4, false}, {c.AutoloadDev.PSR4, true}} {
		for prefix, raw := range sec.m {
			var dirs []string
			var one string
			if json.Unmarshal(raw, &one) == nil {
				dirs = []string{one}
			} else if json.Unmarshal(raw, &dirs) != nil {
				continue
			}
			for _, d := range dirs {
				out = append(out, PSR4Mapping{Prefix: strings.Trim(prefix, `\`), Dir: normDir(d), Dev: sec.dev})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dev != out[j].Dev {
			return !out[i].Dev
		}
		if out[i].Dir != out[j].Dir {
			return out[i].Dir < out[j].Dir
		}
		return out[i].Prefix < out[j].Prefix
	})
	return out, nil
}

// normDir normalises a composer directory to "a/b" ("" for the root).
func normDir(d string) string {
	d = filepath.ToSlash(filepath.Clean(filepath.FromSlash(d)))
	d = strings.TrimPrefix(d, "./")
	if d == "." || d == "/" {
		return ""
	}
	return strings.TrimSuffix(d, "/")
}

var phpIdent = regexp.MustCompile(`^[A-Za-z_\x80-\xff][A-Za-z0-9_\x80-\xff]*$`)

// phpReserved are words that cannot name a class (case-insensitive).
var phpReserved = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`abstract and array as break callable case catch class clone const continue
		declare default do echo else elseif empty enddeclare endfor endforeach endif endswitch endwhile
		eval exit extends final finally fn for foreach function global goto if implements include
		include_once instanceof insteadof interface isset list match namespace new or print private
		protected public readonly require require_once return static switch throw trait try unset use
		var while xor yield int float bool string true false null void iterable object mixed never
		self parent`) {
		m[w] = true
	}
	return m
}()

// validClassName reports whether s can name a PHP class.
func validClassName(s string) bool {
	return phpIdent.MatchString(s) && !phpReserved[strings.ToLower(s)]
}

// validNamespace reports whether ns is empty or a\b\c of identifiers.
func validNamespace(ns string) bool {
	if ns == "" {
		return true
	}
	for _, seg := range strings.Split(ns, `\`) {
		if !phpIdent.MatchString(seg) {
			return false
		}
	}
	return true
}

// NamespaceResult is the result of "files.phpNamespace".
type NamespaceResult struct {
	// Dir is the directory (project relative).
	Dir string `json:"dir"`
	// Namespace is the PSR-4 namespace of Dir ("" if no mapping matches).
	Namespace string `json:"namespace"`
	// Found reports whether a composer.json PSR-4 mapping covers Dir.
	Found   bool         `json:"found"`
	Mapping *PSR4Mapping `json:"mapping,omitempty"`
}

// namespaceFor returns the PSR-4 namespace of the project directory rel
// (slash separated). The longest mapped directory wins; autoload beats
// autoload-dev for the same directory.
func namespaceFor(maps []PSR4Mapping, rel string) (NamespaceResult, error) {
	res := NamespaceResult{Dir: rel}
	best := -1
	for i, m := range maps {
		if m.Dir != "" && rel != m.Dir && !strings.HasPrefix(rel, m.Dir+"/") {
			continue
		}
		if best < 0 || len(m.Dir) > len(maps[best].Dir) {
			best = i
		}
	}
	if best < 0 {
		return res, nil
	}
	m := maps[best]
	rest := strings.TrimPrefix(strings.TrimPrefix(rel, m.Dir), "/")
	parts := []string{}
	if m.Prefix != "" {
		parts = append(parts, m.Prefix)
	}
	if rest != "" {
		for _, seg := range strings.Split(rest, "/") {
			if !phpIdent.MatchString(seg) {
				return res, Errorf(CodeInvalidParams, "directory %q is not a valid namespace segment (PSR-4 %s → %s); pass a namespace explicitly", seg, m.Prefix, m.Dir)
			}
			parts = append(parts, seg)
		}
	}
	res.Namespace, res.Found, res.Mapping = strings.Join(parts, `\`), true, &m
	return res, nil
}

func (a *App) dirNamespace(path string) (NamespaceResult, error) {
	abs, err := a.projectPath(cmpOr(path, "."), true)
	if err != nil {
		return NamespaceResult{}, err
	}
	root := a.ProjectRoot()
	maps, err := readPSR4(root)
	if err != nil {
		return NamespaceResult{}, err
	}
	rel, _ := filepath.Rel(root, abs)
	rel = filepath.ToSlash(rel)
	if rel == "." {
		rel = ""
	}
	return namespaceFor(maps, rel)
}

func (a *App) filesPHPNamespace(_ context.Context, p PathParams) (NamespaceResult, error) {
	return a.dirNamespace(p.Path)
}

// PHPClassParams are the params of "files.createPhpClass".
type PHPClassParams struct {
	// Dir is the target directory (project relative; "" is the root).
	Dir string `json:"dir,omitempty"`
	// Name is the class name; the file is <Dir>/<Name>.php.
	Name string `json:"name"`
	// Kind is class (default, final), abstract, interface, trait or enum.
	Kind string `json:"kind,omitempty"`
	// Namespace overrides the PSR-4 namespace ("" for none); nil means
	// from composer.json.
	Namespace *string `json:"namespace,omitempty"`
	// Open opens the new file as document.
	Open bool `json:"open,omitempty"`
}

// PHPClassResult is the result of "files.createPhpClass".
type PHPClassResult struct {
	FileOpResult
	Namespace string `json:"namespace"`
	// Class is the fully qualified name.
	Class string `json:"class"`
	Kind  string `json:"kind"`
}

// phpTemplate renders the file of a new type.
func phpTemplate(kind, namespace, name string) string {
	var b strings.Builder
	b.WriteString("<?php\n\ndeclare(strict_types=1);\n\n")
	if namespace != "" {
		b.WriteString("namespace " + namespace + ";\n\n")
	}
	switch kind {
	case "abstract":
		b.WriteString("abstract class ")
	case "interface", "trait", "enum":
		b.WriteString(kind + " ")
	default:
		b.WriteString("final class ")
	}
	b.WriteString(name + "\n{\n}\n")
	return b.String()
}

func (a *App) filesCreatePHPClass(ctx context.Context, p PHPClassParams) (PHPClassResult, error) {
	switch p.Kind {
	case "":
		p.Kind = "class"
	case "class", "abstract", "interface", "trait", "enum":
	default:
		return PHPClassResult{}, Errorf(CodeInvalidParams, "kind must be class, abstract, interface, trait or enum")
	}
	p.Name = strings.TrimSuffix(strings.TrimSpace(p.Name), ".php")
	if !validClassName(p.Name) {
		return PHPClassResult{}, Errorf(CodeInvalidParams, "%q is not a valid PHP class name", p.Name)
	}
	var ns string
	if p.Namespace != nil {
		ns = strings.Trim(strings.TrimSpace(*p.Namespace), `\`)
		if !validNamespace(ns) {
			return PHPClassResult{}, Errorf(CodeInvalidParams, "%q is not a valid namespace", ns)
		}
	} else {
		r, err := a.dirNamespace(p.Dir)
		if err != nil {
			return PHPClassResult{}, err
		}
		ns = r.Namespace
	}
	dir, err := a.projectPath(cmpOr(p.Dir, "."), true)
	if err != nil {
		return PHPClassResult{}, err
	}
	abs, err := a.createFile(filepath.Join(dir, p.Name+".php"), []byte(phpTemplate(p.Kind, ns, p.Name)))
	if err != nil {
		return PHPClassResult{}, err
	}
	fr, err := a.openResult(ctx, abs, p.Open)
	class := p.Name
	if ns != "" {
		class = ns + `\` + p.Name
	}
	return PHPClassResult{FileOpResult: fr, Namespace: ns, Class: class, Kind: p.Kind}, err
}
