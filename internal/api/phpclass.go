package api

import (
	"context"

	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/psr4"
)

// PSR4Mapping is one autoload entry of composer.json: namespace prefix →
// directory (project relative, slash separated, "" for the root).
type PSR4Mapping = psr4.Mapping

// normDir normalises a composer directory to "a/b" ("" for the root).
func normDir(d string) string { return psr4.NormDir(d) }

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
	return psr4.Ident.MatchString(s) && !phpReserved[strings.ToLower(s)]
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
	ns, m, found, err := psr4.NamespaceFor(maps, rel)
	if err != nil {
		return res, Errorf(CodeInvalidParams, "%v", err)
	}
	if found {
		res.Namespace, res.Found, res.Mapping = ns, true, &m
	}
	return res, nil
}

func (a *App) dirNamespace(path string) (NamespaceResult, error) {
	abs, err := a.projectPath(cmpOr(path, "."), true)
	if err != nil {
		return NamespaceResult{}, err
	}
	root := a.ProjectRoot()
	maps, err := psr4.Read(root)
	if err != nil {
		return NamespaceResult{}, Errorf(CodeFailed, "%v", err)
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
	// Kind is a file template (FileTemplateKinds): class (default,
	// final), abstract, interface, trait, enum or test.
	Kind string `json:"kind,omitempty"`
	// Namespace overrides the PSR-4 namespace ("" for none); nil means
	// from composer.json.
	Namespace *string `json:"namespace,omitempty"`
	// Open opens the new file as document.
	Open bool `json:"open,omitempty"`
	// DryRun returns the content without creating the file.
	DryRun bool `json:"dryRun,omitempty"`
}

// PHPClassResult is the result of "files.createPhpClass".
type PHPClassResult struct {
	FileOpResult
	Namespace string `json:"namespace"`
	// Class is the fully qualified name.
	Class string `json:"class"`
	Kind  string `json:"kind"`
	// Content is the rendered file (always set for dryRun).
	Content string `json:"content,omitempty"`
}

func (a *App) filesCreatePHPClass(ctx context.Context, p PHPClassParams) (PHPClassResult, error) {
	if p.Kind == "" {
		p.Kind = "class"
	}
	if fileTemplateByKind(p.Kind) == nil {
		return PHPClassResult{}, Errorf(CodeInvalidParams, "kind must be one of %s", strings.Join(FileTemplateKinds(), ", "))
	}
	p.Name = strings.TrimSuffix(strings.TrimSpace(p.Name), ".php")
	if !validClassName(p.Name) {
		return PHPClassResult{}, Errorf(CodeInvalidParams, "%q is not a valid PHP class name", p.Name)
	}
	var ns string
	if p.Namespace != nil {
		ns = strings.Trim(strings.TrimSpace(*p.Namespace), `\`)
		if !psr4.ValidNamespace(ns) {
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
	content, err := a.renderFileTemplate(p.Kind, a.fileTemplateVars(ns, p.Name))
	if err != nil {
		return PHPClassResult{}, err
	}
	class := p.Name
	if ns != "" {
		class = ns + `\` + p.Name
	}
	res := PHPClassResult{Namespace: ns, Class: class, Kind: p.Kind, Content: content}
	target := filepath.Join(dir, p.Name+".php")
	if p.DryRun {
		res.File, res.Path = target, a.displayPath(target)
		return res, nil
	}
	abs, err := a.createFile(target, []byte(content))
	if err != nil {
		return PHPClassResult{}, err
	}
	res.Content = ""
	res.FileOpResult, err = a.openResult(ctx, abs, p.Open)
	return res, err
}

// fileTemplateVars are the variables of a new type name in namespace ns:
// NAME, NAMESPACE, FQN, DATE, YEAR, USER and, for tests, TEST_BASE (the
// project's own TestCase in the test namespace root if it exists) with
// TEST_BASE_NAME and TEST_BASE_USE ("" when it needs no import).
func (a *App) fileTemplateVars(ns, name string) map[string]string {
	now := time.Now()
	user := os.Getenv("USER")
	if user == "" {
		user = os.Getenv("USERNAME")
	}
	fqn := name
	if ns != "" {
		fqn = ns + `\` + name
	}
	vars := map[string]string{
		"NAME": name, "NAMESPACE": ns, "FQN": fqn, "USER": user,
		"DATE": now.Format("2006-01-02"), "YEAR": now.Format("2006"),
	}
	base := `PHPUnit\Framework\TestCase`
	if root := a.ProjectRoot(); root != "" {
		maps, _ := psr4.Read(root)
		for _, m := range maps {
			if m.Dev {
				if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(m.Dir), "TestCase.php")); err == nil {
					base = strings.TrimPrefix(m.Prefix+`\TestCase`, `\`)
				}
				break
			}
		}
	}
	vars["TEST_BASE"] = base
	baseNS, baseName := "", base
	if i := strings.LastIndexByte(base, '\\'); i >= 0 {
		baseNS, baseName = base[:i], base[i+1:]
	}
	vars["TEST_BASE_NAME"] = baseName
	vars["TEST_BASE_USE"] = ""
	if baseNS != ns {
		vars["TEST_BASE_USE"] = base
	}
	return vars
}
