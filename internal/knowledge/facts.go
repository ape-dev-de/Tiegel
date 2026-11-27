package knowledge

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// Facts are what Tiegel knows about the project without an agent
// (level 0): composer.json, PSR-4 roots, the PHP root, frameworks,
// notable packages, QA tools, Docker Compose services, CI and the folder
// structure. They feed the overview page and, compactly, every prompt.
type Facts struct {
	Project string `json:"project"`
	// PHPRoot is the project relative PHP root ("." for the project).
	PHPRoot     string `json:"phpRoot"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type,omitempty"`
	// PHP is the required PHP version constraint.
	PHP string `json:"php,omitempty"`
	// Framework is the main framework ID (symfony, laravel, cakephp,
	// slim, laminas, yii, codeigniter, "" for plain PHP); Frameworks
	// are the framework packages with constraints.
	Framework  string   `json:"framework,omitempty"`
	Frameworks []string `json:"frameworks,omitempty"`
	// Style is the architecture style guessed mechanically (mvc, api,
	// cli, library, mixed); the agent may correct it.
	Style string `json:"style,omitempty"`
	// Packages are notable runtime packages (persistence, messaging,
	// HTTP clients, mail, storage …), DevTools the QA/test packages.
	Packages []string `json:"packages,omitempty"`
	DevTools []string `json:"devTools,omitempty"`
	// Scripts are the composer scripts.
	Scripts []Script `json:"scripts,omitempty"`
	// PSR4 maps namespaces to project relative directories.
	PSR4 map[string][]string `json:"psr4,omitempty"`
	// Folders are the directories with PHP files (two levels deep).
	Folders []FolderInfo `json:"folders,omitempty"`
	// Tooling are configuration files of development tools.
	Tooling []Tool `json:"tooling,omitempty"`
	// Services are Docker Compose services ("db (mysql:8)").
	Services []string `json:"services,omitempty"`
	// PHPFiles counts the PHP files analysed.
	PHPFiles int `json:"phpFiles"`
	// Views is set when a template folder exists (templates,
	// resources/views, src/Template).
	Views bool `json:"views,omitempty"`
}

// Script is a composer script.
type Script struct {
	Name    string `json:"name"`
	Command string `json:"command"`
}

// FolderInfo is a directory with its PHP file count.
type FolderInfo struct {
	Path     string `json:"path"`
	PHPFiles int    `json:"phpFiles"`
}

// Tool is a development tool found by its configuration file.
type Tool struct {
	Name string `json:"name"`
	File string `json:"file"`
}

// frameworkPackages maps composer packages to framework IDs.
var frameworkPackages = []struct{ pkg, id string }{
	{"symfony/framework-bundle", "symfony"},
	{"laravel/framework", "laravel"},
	{"laravel/lumen-framework", "laravel"},
	{"cakephp/cakephp", "cakephp"},
	{"slim/slim", "slim"},
	{"laminas/laminas-mvc", "laminas"},
	{"mezzio/mezzio", "laminas"},
	{"yiisoft/yii2", "yii"},
	{"codeigniter4/framework", "codeigniter"},
	{"api-platform/core", "api-platform"},
	{"api-platform/symfony", "api-platform"},
}

// notablePrefixes are package prefixes worth naming in the overview.
var notablePrefixes = []string{
	"doctrine/orm", "doctrine/dbal", "doctrine/mongodb", "illuminate/database", "cakephp/orm", "propel/",
	"symfony/messenger", "php-amqplib/", "enqueue/", "bunny/", "aws/aws-sdk-php", "google/cloud",
	"guzzlehttp/guzzle", "symfony/http-client", "php-http/", "predis/predis", "snc/redis", "symfony/cache",
	"elasticsearch/", "ruflin/elastica", "meilisearch/", "algolia/", "league/flysystem", "symfony/mailer",
	"phpmailer/", "swiftmailer/", "twig/twig", "symfony/twig-bundle", "symfony/security", "lexik/jwt",
	"league/oauth2", "stripe/", "symfony/serializer", "jms/serializer", "symfony/workflow", "symfony/scheduler",
	"monolog/monolog", "sentry/", "nelmio/", "api-platform/", "livewire/", "inertiajs/", "laravel/sanctum",
	"laravel/passport", "laravel/horizon", "spatie/", "ramsey/uuid", "nesbot/carbon",
}

// devPrefixes are QA and test packages.
var devPrefixes = []string{
	"phpunit/phpunit", "pestphp/pest", "phpstan/phpstan", "vimeo/psalm", "friendsofphp/php-cs-fixer",
	"squizlabs/php_codesniffer", "rector/rector", "infection/infection", "behat/behat", "codeception/codeception",
	"phpspec/phpspec", "symplify/easy-coding-standard", "phpmd/phpmd", "nunomaduro/larastan", "larastan/larastan",
	"phparkitect/", "qossmic/deptrac", "deptrac/deptrac", "laravel/pint", "brianium/paratest", "dama/doctrine-test-bundle",
}

// toolFiles are configuration files of development tools (glob
// patterns relative to the project or the PHP root).
var toolFiles = []struct{ name, glob string }{
	{"Docker Compose", "docker-compose*.y*ml"}, {"Docker Compose", "compose*.y*ml"}, {"Dockerfile", "Dockerfile*"},
	{"Docker Compose", "docker/docker-compose*.y*ml"}, {"Dockerfile", "docker/*/Dockerfile*"},
	{"PHPUnit", "phpunit.xml*"}, {"Pest", "tests/Pest.php"}, {"PHPStan", "phpstan.neon*"}, {"Psalm", "psalm.xml*"},
	{"PHP CS Fixer", ".php-cs-fixer*.php"}, {"PHP_CodeSniffer", "phpcs.xml*"}, {"PHP_CodeSniffer", ".phpcs.xml*"},
	{"Rector", "rector.php"}, {"ECS", "ecs.php"}, {"Infection", "infection.json*"}, {"Deptrac", "deptrac.y*ml"},
	{"Behat", "behat.y*ml"}, {"Codeception", "codeception.y*ml"}, {"Makefile", "Makefile"}, {"Taskfile", "Taskfile.y*ml"},
	{"GitHub Actions", ".github/workflows/*.y*ml"}, {"GitLab CI", ".gitlab-ci.yml"}, {"Bitbucket Pipelines", "bitbucket-pipelines.yml"},
	{"Jenkins", "Jenkinsfile"}, {"CircleCI", ".circleci/config.yml"}, {"DDEV", ".ddev/config.yaml"}, {"Lando", ".lando.yml"},
	{"Environment", ".env"}, {"Environment", ".env.dist"}, {"Environment", ".env.example"}, {"Deployer", "deploy.php"},
	{"Symfony console", "bin/console"}, {"Laravel artisan", "artisan"}, {"CakePHP console", "bin/cake"},
}

// GatherFacts collects the facts of the project at root; phpRoot is the
// absolute PHP root ("" = root); files are the project relative PHP files
// that are analysed.
func GatherFacts(root, phpRoot string, files []string) Facts {
	if phpRoot == "" {
		phpRoot = root
	}
	rel, err := filepath.Rel(root, phpRoot)
	if err != nil || strings.HasPrefix(rel, "..") {
		rel = "."
	}
	rel = filepath.ToSlash(rel)
	f := Facts{Project: filepath.Base(root), PHPRoot: rel, PHPFiles: len(files)}
	f.readComposer(phpRoot, rel)
	for _, d := range []string{"templates", "resources/views", "src/Template", "app/View", "views"} {
		if st, err := os.Stat(filepath.Join(phpRoot, filepath.FromSlash(d))); err == nil && st.IsDir() {
			f.Views = true
		}
	}
	// Tooling below the project and the PHP root.
	seen := map[string]bool{}
	for _, base := range uniqueDirs(root, phpRoot) {
		for _, t := range toolFiles {
			ms, _ := filepath.Glob(filepath.Join(base, filepath.FromSlash(t.glob)))
			sort.Strings(ms)
			for _, m := range ms {
				r, err := filepath.Rel(root, m)
				if err != nil || seen[r] {
					continue
				}
				if st, err := os.Stat(m); err != nil || st.IsDir() {
					continue
				}
				seen[r] = true
				f.Tooling = append(f.Tooling, Tool{Name: t.name, File: filepath.ToSlash(r)})
				if t.name == "Docker Compose" {
					f.Services = append(f.Services, composeServices(m)...)
				}
			}
		}
	}
	f.Services = uniqueStrings(f.Services)
	// Folders: two levels with PHP file counts.
	counts := map[string]int{}
	for _, file := range files {
		parts := strings.Split(file, "/")
		if len(parts) == 1 {
			counts["."]++
			continue
		}
		counts[parts[0]]++
		if len(parts) > 2 {
			counts[parts[0]+"/"+parts[1]]++
		}
	}
	for p, n := range counts {
		f.Folders = append(f.Folders, FolderInfo{Path: p, PHPFiles: n})
	}
	sort.Slice(f.Folders, func(i, j int) bool { return f.Folders[i].Path < f.Folders[j].Path })
	if len(f.Folders) > 40 {
		// Keep the top level and the biggest subfolders.
		sort.SliceStable(f.Folders, func(i, j int) bool {
			ti, tj := !strings.Contains(f.Folders[i].Path, "/"), !strings.Contains(f.Folders[j].Path, "/")
			if ti != tj {
				return ti
			}
			return f.Folders[i].PHPFiles > f.Folders[j].PHPFiles
		})
		f.Folders = f.Folders[:40]
		sort.Slice(f.Folders, func(i, j int) bool { return f.Folders[i].Path < f.Folders[j].Path })
	}
	return f
}

func uniqueDirs(a, b string) []string {
	if filepath.Clean(a) == filepath.Clean(b) {
		return []string{a}
	}
	return []string{a, b}
}

func uniqueStrings(l []string) []string {
	var out []string
	for _, s := range l {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func (f *Facts) readComposer(phpRoot, rel string) {
	b, err := os.ReadFile(filepath.Join(phpRoot, "composer.json"))
	if err != nil {
		return
	}
	var c struct {
		Name        string            `json:"name"`
		Description string            `json:"description"`
		Type        string            `json:"type"`
		Require     map[string]string `json:"require"`
		RequireDev  map[string]string `json:"require-dev"`
		Scripts     map[string]json.RawMessage
	}
	if json.Unmarshal(b, &c) != nil {
		return
	}
	f.Name, f.Description, f.Type = c.Name, c.Description, c.Type
	f.PHP = c.Require["php"]
	for _, fp := range frameworkPackages {
		if v, ok := c.Require[fp.pkg]; ok {
			f.Frameworks = append(f.Frameworks, fp.pkg+" "+v)
			if f.Framework == "" && fp.id != "api-platform" {
				f.Framework = fp.id
			}
		}
	}
	if f.Framework == "" && slices.ContainsFunc(f.Frameworks, func(s string) bool { return strings.HasPrefix(s, "api-platform/") }) {
		f.Framework = "symfony"
	}
	pkgs := sortedKeys(c.Require)
	for _, p := range pkgs {
		for _, pre := range notablePrefixes {
			if strings.HasPrefix(p, pre) {
				f.Packages = append(f.Packages, p+" "+c.Require[p])
				break
			}
		}
	}
	for _, p := range sortedKeys(c.RequireDev) {
		for _, pre := range devPrefixes {
			if strings.HasPrefix(p, pre) {
				f.DevTools = append(f.DevTools, p+" "+c.RequireDev[p])
				break
			}
		}
	}
	for _, name := range sortedKeys(c.Scripts) {
		raw := c.Scripts[name]
		var one string
		var many []string
		switch {
		case json.Unmarshal(raw, &one) == nil:
		case json.Unmarshal(raw, &many) == nil:
			one = strings.Join(many, " && ")
		}
		if one != "" {
			f.Scripts = append(f.Scripts, Script{Name: name, Command: one})
		}
	}
	psr := ReadPSR4(phpRoot)
	if len(psr) > 0 {
		f.PSR4 = map[string][]string{}
		for ns, dirs := range psr {
			for _, d := range dirs {
				f.PSR4[ns] = append(f.PSR4[ns], path.Join(rel, d))
			}
		}
	}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// composeServices lists the services of a compose file ("name (image)")
// with a small line scanner (enough for the overview).
func composeServices(file string) []string {
	fh, err := os.Open(file)
	if err != nil {
		return nil
	}
	defer fh.Close()
	var out []string
	in, indent, cur := false, -1, -1
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), " \t\r")
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		ind := len(line) - len(strings.TrimLeft(line, " "))
		if ind == 0 {
			in = t == "services:"
			indent = -1
			continue
		}
		if !in {
			continue
		}
		if indent < 0 {
			indent = ind
		}
		if ind == indent && strings.HasSuffix(t, ":") {
			out = append(out, strings.Trim(strings.TrimSuffix(t, ":"), `"'`))
			cur = len(out) - 1
			continue
		}
		if cur >= 0 && ind > indent && strings.HasPrefix(t, "image:") {
			img := strings.Trim(strings.TrimSpace(strings.TrimPrefix(t, "image:")), `"'`)
			if img != "" && !strings.Contains(out[cur], "(") {
				out[cur] += " (" + img + ")"
			}
		}
	}
	return out
}

// DeriveStyle guesses the architecture style from the entry points and
// the folders.
func (f *Facts) DeriveStyle(eps []EntryPoint) {
	kinds := map[string]int{}
	for _, e := range eps {
		kinds[e.Kind]++
	}
	views := false
	for _, fo := range f.Folders {
		base := path.Base(fo.Path)
		if base == "templates" || base == "views" || base == "Template" || base == "View" {
			views = true
		}
	}
	views = views || f.Views
	api := slices.ContainsFunc(f.Frameworks, func(s string) bool { return strings.HasPrefix(s, "api-platform/") })
	switch {
	case kinds[KindHTTP] == 0 && kinds[KindPublic] == 0 && kinds[KindCLI] > 0:
		f.Style = "cli"
	case len(eps) == 0:
		f.Style = "library"
	case api || (kinds[KindHTTP] > 0 && !views && f.Framework != ""):
		f.Style = "api"
	case kinds[KindHTTP] > 0 || kinds[KindPublic] > 0:
		f.Style = "mvc"
	default:
		f.Style = "mixed"
	}
	if f.Style != "cli" && f.Style != "library" && kinds[KindAsync]+kinds[KindCron] > 0 {
		f.Style += " + workers"
	}
}

// Hash identifies the facts (overview staleness).
func (f Facts) Hash() string {
	b, _ := json.Marshal(f)
	return HashString(string(b))
}

// ConfigFiles are the files the facts were read from (sources of the
// overview page).
func (f Facts) ConfigFiles() []string {
	var out []string
	cj := path.Join(f.PHPRoot, "composer.json")
	out = append(out, cj)
	for _, t := range f.Tooling {
		if t.Name != "Environment" {
			out = append(out, t.File)
		}
	}
	return uniqueStrings(out)
}

// Compact renders the facts for a prompt (a few hundred tokens).
func (f Facts) Compact() string {
	var b strings.Builder
	if f.Name != "" {
		fmt.Fprintf(&b, "- composer package: %s", f.Name)
		if f.Description != "" {
			fmt.Fprintf(&b, " – %s", f.Description)
		}
		b.WriteString("\n")
	}
	if f.PHPRoot != "." {
		fmt.Fprintf(&b, "- PHP root: %s\n", f.PHPRoot)
	}
	if f.PHP != "" {
		fmt.Fprintf(&b, "- PHP: %s\n", f.PHP)
	}
	fw := f.Framework
	if fw == "" {
		fw = "none detected (plain PHP or a framework Tiegel does not know)"
	}
	fmt.Fprintf(&b, "- framework: %s", fw)
	if len(f.Frameworks) > 0 {
		fmt.Fprintf(&b, " (%s)", strings.Join(f.Frameworks, ", "))
	}
	b.WriteString("\n")
	if f.Style != "" {
		fmt.Fprintf(&b, "- architecture style (mechanical guess): %s\n", f.Style)
	}
	if len(f.Packages) > 0 {
		fmt.Fprintf(&b, "- notable packages: %s\n", strings.Join(f.Packages, ", "))
	}
	if len(f.PSR4) > 0 {
		var l []string
		for _, ns := range sortedKeys(f.PSR4) {
			l = append(l, ns+" → "+strings.Join(f.PSR4[ns], ", "))
		}
		fmt.Fprintf(&b, "- PSR-4: %s\n", strings.Join(l, "; "))
	}
	if len(f.Services) > 0 {
		fmt.Fprintf(&b, "- Docker Compose services: %s\n", strings.Join(f.Services, ", "))
	}
	fmt.Fprintf(&b, "- PHP files analysed: %d\n", f.PHPFiles)
	return b.String()
}
