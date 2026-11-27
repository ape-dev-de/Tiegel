package knowledge

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Entry point kinds (EntryPoint.Kind).
const (
	KindHTTP   = "http"
	KindCLI    = "cli"
	KindAsync  = "async"
	KindCron   = "cron"
	KindPublic = "public"
)

// EntryKinds are the kinds in page order.
var EntryKinds = []string{KindHTTP, KindCLI, KindAsync, KindCron, KindPublic}

// KindTitle is the page title of an entry point kind.
func KindTitle(kind string) string {
	switch kind {
	case KindHTTP:
		return "HTTP routes"
	case KindCLI:
		return "Console commands"
	case KindAsync:
		return "Queue consumers and event listeners"
	case KindCron:
		return "Scheduled tasks"
	case KindPublic:
		return "Public PHP files"
	}
	return "Other entry points"
}

// EntryPoint is a place where data enters the system, found
// mechanically or by the agent.
type EntryPoint struct {
	Kind string `json:"kind"`
	// Name is what triggers it: "GET /orders/{id}", "app:import", "message
	// OrderPlaced", "public/index.php".
	Name string `json:"name"`
	// Handler is the code that runs (Class::method, a function or "").
	Handler string `json:"handler,omitempty"`
	// File and Line are where it is declared (route file, attribute,
	// command class).
	File string `json:"file"`
	Line int    `json:"line,omitempty"`
	// Source tells how it was found (symfony-attribute, laravel-routes,
	// cakephp-routes, slim, symfony-command, … or agent).
	Source string `json:"source"`
}

// HandlerClass is the class part of the handler ("" for functions and
// files).
func (e EntryPoint) HandlerClass() string {
	if i := strings.Index(e.Handler, "::"); i > 0 {
		return e.Handler[:i]
	}
	return ""
}

var (
	nsRE        = regexp.MustCompile(`(?m)^\s*namespace\s+([A-Za-z0-9_\\]+)\s*[;{]`)
	useRE       = regexp.MustCompile(`(?m)^\s*use\s+([A-Za-z0-9_\\]+)(?:\s+as\s+([A-Za-z0-9_]+))?\s*;`)
	classRE     = regexp.MustCompile(`(?m)^\s*(?:(?:abstract|final|readonly)\s+)*class\s+([A-Za-z0-9_]+)([^{]*)`)
	funcRE      = regexp.MustCompile(`(?m)^\s*(?:(?:public|protected|private|static|final|abstract)\s+)*function\s+&?([A-Za-z0-9_]+)\s*\(([^)]*)`)
	sfRouteRE   = regexp.MustCompile(`(?s)#\[\s*(?:Route|Symfony\\Component\\Routing\\Attribute\\Route|Symfony\\Component\\Routing\\Annotation\\Route)\s*\((.*?)\)\s*\]`)
	sfAnnRE     = regexp.MustCompile(`@Route\(\s*([^)]*)\)`)
	firstStrRE  = regexp.MustCompile(`^\s*(?:path\s*[:=]\s*)?['"]([^'"]*)['"]`)
	pathArgRE   = regexp.MustCompile(`path\s*[:=]\s*['"]([^'"]*)['"]`)
	nameArgRE   = regexp.MustCompile(`name\s*[:=]\s*['"]([^'"]*)['"]`)
	methodsRE   = regexp.MustCompile(`methods\s*[:=]\s*[\[{]?([^\]})]*)[\]}]?`)
	quotedRE    = regexp.MustCompile(`['"]([A-Za-z]+)['"]`)
	apiResRE    = regexp.MustCompile(`#\[\s*(?:ApiPlatform\\Metadata\\)?ApiResource\b`)
	asCmdRE     = regexp.MustCompile(`#\[\s*AsCommand\s*\(\s*(?:name\s*:\s*)?['"]([^'"]+)['"]`)
	defNameRE   = regexp.MustCompile(`static\s+\$defaultName\s*=\s*['"]([^'"]+)['"]`)
	setNameRE   = regexp.MustCompile(`->setName\(\s*['"]([^'"]+)['"]`)
	signatureRE = regexp.MustCompile(`protected\s+\$signature\s*=\s*['"]([^\s'"{]+)`)
	cakeNameRE  = regexp.MustCompile(`function\s+defaultName\(\)\s*:\s*string\s*\{\s*return\s+['"]([^'"]+)['"]`)
	msgAttrRE   = regexp.MustCompile(`#\[\s*AsMessageHandler\b`)
	listenRE    = regexp.MustCompile(`(?s)#\[\s*AsEventListener\s*\((.*?)\)\s*\]`)
	eventArgRE  = regexp.MustCompile(`(?:event\s*:\s*)?(?:([A-Za-z0-9_\\]+)::class|['"]([^'"]+)['"]|[A-Za-z0-9_\\]+::([A-Z_][A-Z0-9_]*))`)
	cronAttrRE  = regexp.MustCompile(`#\[\s*As(?:Cron|Periodic)Task\s*\(\s*(?:expression\s*:\s*|frequency\s*:\s*)?['"]([^'"]+)['"]`)
	laravelRE   = regexp.MustCompile(`Route::(get|post|put|patch|delete|options|any|match|resource|apiResource|view|redirect)\(\s*(?:\[[^\]]*\]\s*,\s*)?['"]([^'"]*)['"]\s*(?:,\s*([^;]*))?`)
	artisanRE   = regexp.MustCompile(`Artisan::command\(\s*['"]([^\s'"{]+)`)
	scheduleRE  = regexp.MustCompile(`(?:\$schedule|Schedule::)(?:->|)(command|job|call|exec)\(\s*(?:new\s+)?([^,)]*)`)
	cakeRouteRE = regexp.MustCompile(`->(connect|get|post|put|patch|delete|options)\(\s*['"]([^'"]*)['"]\s*,\s*([^;]*)`)
	cakeResRE   = regexp.MustCompile(`->resources\(\s*['"]([A-Za-z0-9_]+)['"]`)
	cakeCtlRE   = regexp.MustCompile(`['"]controller['"]\s*=>\s*['"]([A-Za-z0-9_/]+)['"]`)
	cakeActRE   = regexp.MustCompile(`['"]action['"]\s*=>\s*['"]([A-Za-z0-9_]+)['"]`)
	cakeStrRE   = regexp.MustCompile(`^['"]([A-Za-z0-9_/]+)::([A-Za-z0-9_]+)['"]`)
	slimRE      = regexp.MustCompile(`\$(?:app|group|router|r|route|routes)->(get|post|put|patch|delete|options|any|map)\(\s*(?:\[[^\]]*\]\s*,\s*)?['"]([^'"]*)['"]\s*,\s*([^;]*)`)
	classRefRE  = regexp.MustCompile(`\\?([A-Z][A-Za-z0-9_\\]*)::class`)
	arrHandRE   = regexp.MustCompile(`\[\s*\\?([A-Z][A-Za-z0-9_\\]*)::class\s*,\s*['"]([A-Za-z0-9_]+)['"]\s*\]`)
	atHandRE    = regexp.MustCompile(`^['"]([A-Za-z0-9_\\]+)[@:]([A-Za-z0-9_]+)['"]`)
	yamlPathRE  = regexp.MustCompile(`^\s+path:\s*['"]?([^'"\s#]+)`)
	yamlCtlRE   = regexp.MustCompile(`^\s+controller:\s*['"]?([^'"\s#]+)`)
	yamlMethRE  = regexp.MustCompile(`^\s+methods:\s*\[?([^\]#]*)`)
	yamlNameRE  = regexp.MustCompile(`^([A-Za-z0-9_.\-]+):\s*$`)
)

// phpFile is a scanned PHP file.
type phpFile struct {
	rel, src  string
	ns        string
	uses      map[string]string // alias → FQN
	class     string            // FQN of the first class
	classLine int
	classDecl string // text after the class name (extends, implements)
	lineOf    []int  // offsets of line starts
}

func scanPHP(rel, src string) *phpFile {
	f := &phpFile{rel: rel, src: src, uses: map[string]string{}}
	f.lineOf = append(f.lineOf, 0)
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			f.lineOf = append(f.lineOf, i+1)
		}
	}
	if m := nsRE.FindStringSubmatch(src); m != nil {
		f.ns = m[1]
	}
	for _, m := range useRE.FindAllStringSubmatch(src, -1) {
		alias := m[2]
		if alias == "" {
			alias = m[1][strings.LastIndexByte(m[1], '\\')+1:]
		}
		f.uses[alias] = m[1]
	}
	if m := classRE.FindStringSubmatchIndex(src); m != nil {
		name := src[m[2]:m[3]]
		f.class = name
		if f.ns != "" {
			f.class = f.ns + `\` + name
		}
		f.classLine = f.line(m[2])
		f.classDecl = src[m[4]:m[5]]
	}
	return f
}

// line returns the 1-based line of an offset.
func (f *phpFile) line(off int) int {
	i := sort.Search(len(f.lineOf), func(i int) bool { return f.lineOf[i] > off })
	return i
}

// resolve turns a class reference of the file into a FQN.
func (f *phpFile) resolve(name string) string {
	name = strings.TrimSpace(name)
	if strings.HasPrefix(name, `\`) {
		return strings.TrimPrefix(name, `\`)
	}
	first, rest, _ := strings.Cut(name, `\`)
	if fq, ok := f.uses[first]; ok {
		if rest != "" {
			return fq + `\` + rest
		}
		return fq
	}
	if f.ns != "" {
		return f.ns + `\` + name
	}
	return name
}

// nextFunc returns the name, parameter text and offset of the first
// function declared after off.
func (f *phpFile) nextFunc(off int) (string, string, int) {
	if m := funcRE.FindStringSubmatchIndex(f.src[off:]); m != nil {
		return f.src[off+m[2] : off+m[3]], f.src[off+m[4] : off+m[5]], off + m[2]
	}
	return "", "", -1
}

func (f *phpFile) hasFunc(name string) bool {
	for _, m := range funcRE.FindAllStringSubmatch(f.src, -1) {
		if m[1] == name {
			return true
		}
	}
	return false
}

// ExtractEntryPoints finds the entry points of the project mechanically:
// routes (Symfony attributes, annotations and YAML, Laravel routes/*.php,
// CakePHP config/routes.php, Slim), console commands (Symfony, Laravel,
// CakePHP), message handlers, event listeners, queued jobs, scheduled
// tasks and public PHP files. files are the project relative PHP files;
// phpRoot is the project relative PHP root ("." for the project).
func ExtractEntryPoints(root, phpRoot string, files []string) []EntryPoint {
	if phpRoot == "" {
		phpRoot = "."
	}
	var out []EntryPoint
	for _, rel := range files {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil || len(b) > 2<<20 {
			continue
		}
		f := scanPHP(rel, string(b))
		out = append(out, phpEntries(f, phpRoot)...)
	}
	out = append(out, yamlRoutes(root, phpRoot)...)
	return dedupeEntries(out)
}

func dedupeEntries(in []EntryPoint) []EntryPoint {
	seen := map[string]bool{}
	var out []EntryPoint
	for _, e := range in {
		k := e.Kind + "|" + e.Name + "|" + e.Handler
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, e)
	}
	SortEntries(out)
	return out
}

// SortEntries orders entry points by kind, file and line.
func SortEntries(l []EntryPoint) {
	rank := func(k string) int {
		for i, x := range EntryKinds {
			if x == k {
				return i
			}
		}
		return len(EntryKinds)
	}
	sort.SliceStable(l, func(i, j int) bool {
		a, b := l[i], l[j]
		if rank(a.Kind) != rank(b.Kind) {
			return rank(a.Kind) < rank(b.Kind)
		}
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Name < b.Name
	})
}

func phpEntries(f *phpFile, phpRoot string) []EntryPoint {
	var out []EntryPoint
	rel := f.rel
	inPHPRoot := strings.TrimPrefix(rel, strings.TrimSuffix(phpRoot, "/")+"/")
	if phpRoot == "." {
		inPHPRoot = rel
	}
	src := f.src
	// Public PHP files.
	dir := path.Dir(inPHPRoot)
	switch {
	case dir == "public" || dir == "web" || dir == "webroot" || dir == "htdocs" || dir == "www" || dir == "public_html":
		out = append(out, EntryPoint{Kind: KindPublic, Name: rel, File: rel, Source: "public-file"})
	case dir == "." && !configScript(path.Base(rel)) && f.class == "":
		out = append(out, EntryPoint{Kind: KindPublic, Name: rel, File: rel, Source: "root-script"})
	}
	// Laravel route files.
	if strings.HasPrefix(inPHPRoot, "routes/") {
		out = append(out, laravelRoutes(f)...)
	}
	if inPHPRoot == "config/routes.php" || strings.HasPrefix(inPHPRoot, "config/routes/") {
		out = append(out, cakeRoutes(f)...)
	}
	// Slim and similar: $app->get('/x', …) anywhere outside vendor.
	cakeFile := inPHPRoot == "config/routes.php" || strings.HasPrefix(inPHPRoot, "config/routes/")
	for _, m := range slimRE.FindAllStringSubmatchIndex(src, -1) {
		if cakeFile {
			break
		}
		method, p, rest := strings.ToUpper(src[m[2]:m[3]]), src[m[4]:m[5]], src[m[6]:m[7]]
		if method == "ANY" || method == "MAP" {
			method = "ANY"
		}
		out = append(out, EntryPoint{Kind: KindHTTP, Name: method + " " + p, Handler: handlerOf(f, rest), File: rel, Line: f.line(m[0]), Source: "slim"})
	}
	// Scheduled tasks (Laravel console kernel / routes/console.php).
	for _, m := range scheduleRE.FindAllStringSubmatchIndex(src, -1) {
		kind, arg := src[m[2]:m[3]], strings.TrimSpace(src[m[4]:m[5]])
		name := strings.Trim(arg, `'"`)
		h := ""
		if kind == "job" {
			h = f.resolve(strings.TrimSuffix(strings.TrimSuffix(strings.Fields(arg + " ")[0], "()"), "::class")) + "::handle"
			name = "job " + path.Base(strings.ReplaceAll(strings.TrimSuffix(h, "::handle"), `\`, "/"))
		}
		if strings.HasPrefix(name, "function") || name == "" {
			name = kind + " (closure)"
		}
		out = append(out, EntryPoint{Kind: KindCron, Name: "schedule " + name, Handler: h, File: rel, Line: f.line(m[0]), Source: "laravel-schedule"})
	}
	for _, m := range artisanRE.FindAllStringSubmatchIndex(src, -1) {
		out = append(out, EntryPoint{Kind: KindCLI, Name: src[m[2]:m[3]], File: rel, Line: f.line(m[0]), Source: "laravel-artisan"})
	}
	if f.class == "" {
		return out
	}
	cls, decl := f.class, f.classDecl
	short := cls[strings.LastIndexByte(cls, '\\')+1:]
	// Symfony routes: class prefix, then methods.
	prefix := ""
	head := src[:max(0, f.lineOf[min(f.classLine-1, len(f.lineOf)-1)])]
	if ms := sfRouteRE.FindAllStringSubmatch(head, -1); len(ms) > 0 {
		prefix = routePath(ms[len(ms)-1][1])
	} else if ms := sfAnnRE.FindAllStringSubmatch(head, -1); len(ms) > 0 {
		prefix = routePath(ms[len(ms)-1][1])
	}
	body := f.lineOf[min(f.classLine-1, len(f.lineOf)-1)]
	for _, re := range []*regexp.Regexp{sfRouteRE, sfAnnRE} {
		source := "symfony-attribute"
		if re == sfAnnRE {
			source = "symfony-annotation"
		}
		for _, m := range re.FindAllStringSubmatchIndex(src[body:], -1) {
			args := src[body+m[2] : body+m[3]]
			fn, _, _ := f.nextFunc(body + m[1])
			h := cls + "::" + fn
			if fn == "" {
				h = cls + "::__invoke"
			}
			methods := routeMethods(args)
			out = append(out, EntryPoint{Kind: KindHTTP, Name: methods + " " + joinRoute(prefix, routePath(args)), Handler: h, File: rel, Line: f.line(body + m[0]), Source: source})
		}
	}
	if apiResRE.MatchString(head) {
		out = append(out, EntryPoint{Kind: KindHTTP, Name: "API resource " + short, Handler: cls, File: rel, Line: f.classLine, Source: "api-platform"})
	}
	// Console commands.
	switch {
	case asCmdRE.MatchString(src):
		out = append(out, cmdEntry(f, asCmdRE.FindStringSubmatch(src)[1], "symfony-command"))
	case defNameRE.MatchString(src):
		out = append(out, cmdEntry(f, defNameRE.FindStringSubmatch(src)[1], "symfony-command"))
	case signatureRE.MatchString(src):
		out = append(out, EntryPoint{Kind: KindCLI, Name: signatureRE.FindStringSubmatch(src)[1], Handler: cls + "::handle", File: rel, Line: f.classLine, Source: "laravel-command"})
	case strings.Contains(decl, "extends") && strings.Contains(decl, "Command") && setNameRE.MatchString(src):
		out = append(out, cmdEntry(f, setNameRE.FindStringSubmatch(src)[1], "symfony-command"))
	case cakeNameRE.MatchString(src):
		out = append(out, EntryPoint{Kind: KindCLI, Name: cakeNameRE.FindStringSubmatch(src)[1], Handler: cls + "::execute", File: rel, Line: f.classLine, Source: "cakephp-command"})
	case strings.HasSuffix(short, "Command") && regexp.MustCompile(`extends\s+(?:\\?Cake\\Command\\)?(?:Base)?Command\b`).MatchString(decl) && strings.Contains(src, "ConsoleIo"):
		out = append(out, EntryPoint{Kind: KindCLI, Name: underscore(strings.TrimSuffix(short, "Command")), Handler: cls + "::execute", File: rel, Line: f.classLine, Source: "cakephp-command"})
	case strings.HasSuffix(short, "Shell") && regexp.MustCompile(`extends\s+\\?(?:Cake\\Console\\)?Shell\b`).MatchString(decl):
		out = append(out, EntryPoint{Kind: KindCLI, Name: underscore(strings.TrimSuffix(short, "Shell")), Handler: cls + "::main", File: rel, Line: f.classLine, Source: "cakephp-shell"})
	}
	// Message handlers, listeners, jobs.
	switch {
	case msgAttrRE.MatchString(src) || strings.Contains(decl, "MessageHandlerInterface"):
		out = append(out, EntryPoint{Kind: KindAsync, Name: "message " + firstParamType(f, "__invoke", short), Handler: cls + "::__invoke", File: rel, Line: f.classLine, Source: "messenger-handler"})
	case strings.Contains(decl, "ShouldQueue") && f.hasFunc("handle"):
		kind := "job "
		if strings.Contains(rel, "/Listeners/") {
			kind = "queued listener for "
			short = firstParamType(f, "handle", short)
		}
		out = append(out, EntryPoint{Kind: KindAsync, Name: kind + short, Handler: cls + "::handle", File: rel, Line: f.classLine, Source: "laravel-queue"})
	case strings.Contains(rel, "/Listeners/") && f.hasFunc("handle"):
		out = append(out, EntryPoint{Kind: KindAsync, Name: "listener for " + firstParamType(f, "handle", short), Handler: cls + "::handle", File: rel, Line: f.classLine, Source: "laravel-listener"})
	case strings.Contains(decl, "EventSubscriberInterface"):
		out = append(out, EntryPoint{Kind: KindAsync, Name: "event subscriber " + short, Handler: cls + "::getSubscribedEvents", File: rel, Line: f.classLine, Source: "event-subscriber"})
	}
	for _, m := range listenRE.FindAllStringSubmatchIndex(src, -1) {
		args := src[m[2]:m[3]]
		ev := "event"
		if em := eventArgRE.FindStringSubmatch(args); em != nil {
			ev = em[1] + em[2] + em[3]
			ev = ev[strings.LastIndexByte(ev, '\\')+1:]
		}
		fn, _, _ := f.nextFunc(m[1])
		h := cls + "::" + fn
		if fn == "" || f.line(m[0]) <= f.classLine {
			h = cls + "::__invoke"
		}
		out = append(out, EntryPoint{Kind: KindAsync, Name: "listener for " + ev, Handler: h, File: rel, Line: f.line(m[0]), Source: "event-listener"})
	}
	if m := cronAttrRE.FindStringSubmatch(src); m != nil {
		h := cls + "::__invoke"
		if !f.hasFunc("__invoke") && f.hasFunc("execute") {
			h = cls + "::execute"
		}
		out = append(out, EntryPoint{Kind: KindCron, Name: "schedule " + m[1] + " " + short, Handler: h, File: rel, Line: f.classLine, Source: "symfony-scheduler"})
	}
	return out
}

// configScript reports PHP files at the project root that configure
// tools (not entry points).
func configScript(name string) bool {
	switch name {
	case "rector.php", "ecs.php", "deploy.php", "phpinsights.php", ".phpstorm.meta.php", "_ide_helper.php", "_ide_helper_models.php", "server.php":
		return true
	}
	return strings.HasPrefix(name, ".php-cs-fixer") || strings.HasPrefix(name, ".php_cs")
}

func cmdEntry(f *phpFile, name, source string) EntryPoint {
	h := f.class + "::execute"
	if !f.hasFunc("execute") && f.hasFunc("__invoke") {
		h = f.class + "::__invoke"
	}
	return EntryPoint{Kind: KindCLI, Name: name, Handler: h, File: f.rel, Line: f.classLine, Source: source}
}

// firstParamType returns the short type of the first parameter of
// function fn (or def).
func firstParamType(f *phpFile, fn, def string) string {
	for _, m := range funcRE.FindAllStringSubmatch(f.src, -1) {
		if m[1] != fn {
			continue
		}
		for _, w := range strings.Fields(strings.ReplaceAll(m[2], ",", " ")) {
			w = strings.TrimLeft(w, "?")
			if w != "" && w[0] != '$' && w != "readonly" && w != "private" && w != "public" && w != "protected" {
				return w[strings.LastIndexByte(w, '\\')+1:]
			}
		}
	}
	return def
}

// routePath extracts the path of route arguments.
func routePath(args string) string {
	if m := pathArgRE.FindStringSubmatch(args); m != nil {
		return m[1]
	}
	if m := firstStrRE.FindStringSubmatch(args); m != nil {
		return m[1]
	}
	if m := nameArgRE.FindStringSubmatch(args); m != nil {
		return "(route " + m[1] + ")"
	}
	return ""
}

func routeMethods(args string) string {
	m := methodsRE.FindStringSubmatch(args)
	if m == nil {
		return "ANY"
	}
	var out []string
	for _, q := range quotedRE.FindAllStringSubmatch(m[1], -1) {
		out = append(out, strings.ToUpper(q[1]))
	}
	if len(out) == 0 {
		return "ANY"
	}
	return strings.Join(out, "|")
}

func joinRoute(prefix, p string) string {
	switch {
	case prefix == "":
		if p == "" {
			return "/"
		}
		return p
	case p == "" || p == "/":
		return prefix
	}
	return strings.TrimSuffix(prefix, "/") + "/" + strings.TrimPrefix(p, "/")
}

// handlerOf parses a route handler argument ([X::class, 'm'], 'X@m',
// X::class, a closure).
func handlerOf(f *phpFile, arg string) string {
	arg = strings.TrimSpace(arg)
	if m := arrHandRE.FindStringSubmatch(arg); m != nil {
		return f.resolve(m[1]) + "::" + m[2]
	}
	if m := atHandRE.FindStringSubmatch(arg); m != nil {
		cls := m[1]
		if !strings.Contains(cls, `\`) && f.uses[cls] == "" {
			if strings.HasSuffix(f.rel, ".php") && strings.Contains(f.rel, "routes/") {
				cls = `App\Http\Controllers\` + cls
			}
			return cls + "::" + m[2]
		}
		return f.resolve(cls) + "::" + m[2]
	}
	if m := classRefRE.FindStringSubmatchIndex(arg); m != nil && m[0] < 3 {
		return f.resolve(arg[m[2]:m[3]]) + "::__invoke"
	}
	return ""
}

func laravelRoutes(f *phpFile) []EntryPoint {
	var out []EntryPoint
	src := f.src
	for _, m := range laravelRE.FindAllStringSubmatchIndex(src, -1) {
		verb, p := src[m[2]:m[3]], src[m[4]:m[5]]
		rest := ""
		if m[6] >= 0 {
			rest = src[m[6]:m[7]]
		}
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
		name := strings.ToUpper(verb) + " " + p
		h := handlerOf(f, rest)
		switch verb {
		case "resource", "apiResource":
			name = "resource " + p
			if c := classRefRE.FindStringSubmatch(rest); c != nil {
				h = f.resolve(c[1])
			} else if q := strings.Trim(strings.TrimSpace(strings.SplitN(rest, ",", 2)[0]), `'")`); q != "" {
				h = `App\Http\Controllers\` + q
			}
		case "view", "redirect":
			name = "GET " + p
			h = ""
		case "match":
			name = "MATCH " + p
		}
		out = append(out, EntryPoint{Kind: KindHTTP, Name: name, Handler: h, File: f.rel, Line: f.line(m[0]), Source: "laravel-routes"})
	}
	return out
}

func cakeRoutes(f *phpFile) []EntryPoint {
	var out []EntryPoint
	src := f.src
	for _, m := range cakeRouteRE.FindAllStringSubmatchIndex(src, -1) {
		verb, p, rest := src[m[2]:m[3]], src[m[4]:m[5]], src[m[6]:m[7]]
		method := strings.ToUpper(verb)
		if verb == "connect" {
			method = "ANY"
		}
		h := ""
		ctl, act := "", "index"
		if c := cakeCtlRE.FindStringSubmatch(rest); c != nil {
			ctl = c[1]
			if a := cakeActRE.FindStringSubmatch(rest); a != nil {
				act = a[1]
			}
		} else if c := cakeStrRE.FindStringSubmatch(strings.TrimSpace(rest)); c != nil {
			ctl, act = c[1], c[2]
		}
		if ctl != "" {
			h = `App\Controller\` + strings.ReplaceAll(ctl, "/", `\`) + "Controller::" + act
		}
		out = append(out, EntryPoint{Kind: KindHTTP, Name: method + " " + p, Handler: h, File: f.rel, Line: f.line(m[0]), Source: "cakephp-routes"})
	}
	for _, m := range cakeResRE.FindAllStringSubmatchIndex(src, -1) {
		ctl := src[m[2]:m[3]]
		out = append(out, EntryPoint{Kind: KindHTTP, Name: "resource /" + underscore(ctl), Handler: `App\Controller\` + ctl + "Controller", File: f.rel, Line: f.line(m[0]), Source: "cakephp-routes"})
	}
	if i := strings.Index(src, "->fallbacks("); i >= 0 {
		out = append(out, EntryPoint{Kind: KindHTTP, Name: "ANY /{controller}/{action}/* (fallback routes)", File: f.rel, Line: f.line(i), Source: "cakephp-routes"})
	}
	return out
}

// yamlRoutes reads Symfony YAML route files (config/routes.yaml and
// config/routes/*.yaml): name, path, controller and methods.
func yamlRoutes(root, phpRoot string) []EntryPoint {
	base := filepath.Join(root, filepath.FromSlash(phpRoot), "config")
	var files []string
	for _, g := range []string{"routes.y*ml", "routes/*.y*ml", "routes/*/*.y*ml"} {
		ms, _ := filepath.Glob(filepath.Join(base, g))
		files = append(files, ms...)
	}
	sort.Strings(files)
	var out []EntryPoint
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(root, file)
		rel = filepath.ToSlash(rel)
		var cur *EntryPoint
		var methods string
		flush := func() {
			if cur != nil && cur.Name != "" && cur.Handler != "" {
				if methods == "" {
					methods = "ANY"
				}
				cur.Name = methods + " " + cur.Name
				out = append(out, *cur)
			}
			cur, methods = nil, ""
		}
		for i, line := range strings.Split(string(b), "\n") {
			if m := yamlNameRE.FindStringSubmatch(line); m != nil {
				flush()
				cur = &EntryPoint{Kind: KindHTTP, File: rel, Line: i + 1, Source: "symfony-yaml"}
				continue
			}
			if cur == nil {
				continue
			}
			if m := yamlPathRE.FindStringSubmatch(line); m != nil {
				cur.Name = m[1]
			}
			if m := yamlCtlRE.FindStringSubmatch(line); m != nil {
				cur.Handler = strings.ReplaceAll(m[1], `\\`, `\`)
				if !strings.Contains(cur.Handler, "::") {
					cur.Handler += "::__invoke"
				}
			}
			if m := yamlMethRE.FindStringSubmatch(line); m != nil {
				var ms []string
				for _, x := range strings.FieldsFunc(m[1], func(r rune) bool { return r == ',' || r == ' ' || r == '\'' || r == '"' }) {
					ms = append(ms, strings.ToUpper(x))
				}
				methods = strings.Join(ms, "|")
			}
		}
		flush()
	}
	return out
}

// underscore turns "UpdateTable" into "update_table".
func underscore(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ClassFile finds the project relative file of a class: the PSR-4 path
// when it exists in files, else a file named like the class.
func ClassFile(fqn string, psr4 map[string][]string, files []string) string {
	fqn = strings.TrimPrefix(fqn, `\`)
	if i := strings.Index(fqn, "::"); i >= 0 {
		fqn = fqn[:i]
	}
	if fqn == "" {
		return ""
	}
	have := make(map[string]bool, len(files))
	for _, f := range files {
		have[f] = true
	}
	best := ""
	for ns, dirs := range psr4 {
		if ns != "" && !strings.HasPrefix(fqn, ns) {
			continue
		}
		rest := strings.ReplaceAll(strings.TrimPrefix(fqn, ns), `\`, "/") + ".php"
		for _, d := range dirs {
			p := path.Join(d, rest)
			if have[p] && len(p) > len(best) {
				best = p
			}
		}
	}
	if best != "" {
		return best
	}
	want := "/" + fqn[strings.LastIndexByte(fqn, '\\')+1:] + ".php"
	tail := "/" + strings.ReplaceAll(fqn, `\`, "/") + ".php"
	cand := ""
	for _, f := range files {
		if strings.HasSuffix("/"+f, tail) {
			return f
		}
		if cand == "" && strings.HasSuffix("/"+f, want) {
			cand = f
		}
	}
	return cand
}
