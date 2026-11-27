package knowledge

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// entryNames returns "kind name → handler" lines.
func entryNames(eps []EntryPoint) []string {
	var out []string
	for _, e := range eps {
		out = append(out, e.Kind+" "+e.Name+" → "+e.Handler)
	}
	return out
}

func has(t *testing.T, eps []EntryPoint, want ...string) {
	t.Helper()
	got := entryNames(eps)
	for _, w := range want {
		found := false
		for _, g := range got {
			if g == w {
				found = true
			}
		}
		if !found {
			t.Errorf("missing %q in\n%s", w, strings.Join(got, "\n"))
		}
	}
}

func TestEntryPointsSymfony(t *testing.T) {
	root := t.TempDir()
	write(t, root, map[string]string{
		"composer.json": `{"name":"acme/shop","require":{"php":">=8.2","symfony/framework-bundle":"^7.1","doctrine/orm":"^3","symfony/messenger":"^7"},"require-dev":{"phpunit/phpunit":"^11","phpstan/phpstan":"^2"},
			"scripts":{"test":"phpunit","qa":["phpstan","phpunit"]},"autoload":{"psr-4":{"App\\":"src/"}}}`,
		"src/Controller/OrderController.php": `<?php
namespace App\Controller;

use Symfony\Component\Routing\Attribute\Route;
use App\Service\OrderService;

#[Route('/orders', name: 'orders_')]
final class OrderController extends AbstractController
{
    #[Route('', name: 'list', methods: ['GET'])]
    public function list(): Response {}

    #[Route(
        path: '/{id}',
        name: 'show',
        methods: ['GET', 'HEAD'],
    )]
    public function show(int $id): Response {}

    #[Route('/', methods: 'POST')]
    public function create(Request $r): Response {}
}
`,
		"src/Controller/LegacyController.php": `<?php
namespace App\Controller;

class LegacyController
{
    /**
     * @Route("/legacy/{slug}", name="legacy", methods={"GET"})
     */
    public function show($slug) {}
}
`,
		"src/Command/ImportOrdersCommand.php": `<?php
namespace App\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;

#[AsCommand(name: 'app:import-orders', description: 'Imports orders')]
class ImportOrdersCommand extends Command
{
    protected function execute(InputInterface $input, OutputInterface $output): int { return 0; }
}
`,
		"src/Command/OldCommand.php": `<?php
namespace App\Command;
class OldCommand extends Command
{
    protected static $defaultName = 'app:old';
    public function __invoke() {}
}
`,
		"src/MessageHandler/OrderPlacedHandler.php": `<?php
namespace App\MessageHandler;

use Symfony\Component\Messenger\Attribute\AsMessageHandler;
use App\Message\OrderPlaced;

#[AsMessageHandler]
final class OrderPlacedHandler
{
    public function __invoke(OrderPlaced $message): void {}
}
`,
		"src/EventListener/ExceptionListener.php": `<?php
namespace App\EventListener;

final class ExceptionListener
{
    #[AsEventListener(event: KernelEvents::EXCEPTION)]
    public function onException(ExceptionEvent $e): void {}
}
`,
		"src/Scheduler/CleanupTask.php": `<?php
namespace App\Scheduler;

#[AsCronTask('0 3 * * *')]
class CleanupTask
{
    public function __invoke() {}
}
`,
		"src/EventSubscriber/AuditSubscriber.php": "<?php\nnamespace App\\EventSubscriber;\nclass AuditSubscriber implements EventSubscriberInterface\n{\n  public static function getSubscribedEvents(): array { return []; }\n}\n",
		"config/routes.yaml": `controllers:
    resource: ../src/Controller/
    type: attribute

health:
    path: /health
    controller: App\Controller\HealthController::check
    methods: [GET]

home:
    path: /
    controller: App\Controller\HomeController
`,
		"public/index.php":         "<?php\nrequire dirname(__DIR__).'/vendor/autoload_runtime.php';\n",
		"docker-compose.yml":       "services:\n  php:\n    build: .\n  database:\n    image: \"mysql:8.0\"\n    environment:\n      X: 1\nvolumes:\n  db:\n",
		"phpunit.xml.dist":         "<phpunit/>",
		"phpstan.neon":             "parameters:\n",
		".github/workflows/ci.yml": "on: push\n",
		"rector.php":               "<?php\nreturn RectorConfig::configure();\n",
		"templates/base.html.twig": "x",
	})
	files := FilterFiles([]string{"src/Controller/OrderController.php", "src/Controller/LegacyController.php", "src/Command/ImportOrdersCommand.php", "src/Command/OldCommand.php",
		"src/MessageHandler/OrderPlacedHandler.php", "src/EventListener/ExceptionListener.php", "src/Scheduler/CleanupTask.php", "src/EventSubscriber/AuditSubscriber.php", "public/index.php", "rector.php", "vendor/x/Route.php"}, nil, nil)
	eps := ExtractEntryPoints(root, ".", files)
	has(t, eps,
		`http GET /orders → App\Controller\OrderController::list`,
		`http GET|HEAD /orders/{id} → App\Controller\OrderController::show`,
		`http POST /orders → App\Controller\OrderController::create`,
		`http GET /legacy/{slug} → App\Controller\LegacyController::show`,
		`http GET /health → App\Controller\HealthController::check`,
		`http ANY / → App\Controller\HomeController::__invoke`,
		`cli app:import-orders → App\Command\ImportOrdersCommand::execute`,
		`cli app:old → App\Command\OldCommand::__invoke`,
		`async message OrderPlaced → App\MessageHandler\OrderPlacedHandler::__invoke`,
		`async listener for EXCEPTION → App\EventListener\ExceptionListener::onException`,
		`async event subscriber AuditSubscriber → App\EventSubscriber\AuditSubscriber::getSubscribedEvents`,
		`cron schedule 0 3 * * * CleanupTask → App\Scheduler\CleanupTask::__invoke`,
		`public public/index.php → `,
	)
	for _, e := range eps {
		if strings.Contains(e.File, "rector") || strings.Contains(e.Name, "controllers") {
			t.Fatal("not an entry point", e)
		}
		if e.Kind == KindHTTP && e.Source == "symfony-attribute" && e.Line == 0 {
			t.Fatal("no line", e)
		}
	}
	// Ordered by kind.
	if eps[0].Kind != KindHTTP || eps[len(eps)-1].Kind != KindPublic {
		t.Fatal(entryNames(eps))
	}
	// Facts.
	f := GatherFacts(root, root, files)
	f.DeriveStyle(eps)
	if f.Framework != "symfony" || f.PHP != ">=8.2" || f.Name != "acme/shop" || f.Style != "mvc + workers" || !f.Views {
		t.Fatalf("%+v", f)
	}
	if strings.Join(f.Services, ",") != "php,database (mysql:8.0)" {
		t.Fatal(f.Services)
	}
	tools := map[string]bool{}
	for _, tl := range f.Tooling {
		tools[tl.Name] = true
	}
	for _, want := range []string{"Docker Compose", "PHPUnit", "PHPStan", "GitHub Actions", "Rector"} {
		if !tools[want] {
			t.Fatal("tool", want, f.Tooling)
		}
	}
	if len(f.DevTools) != 2 || len(f.Packages) != 2 || len(f.Scripts) != 2 || f.Scripts[0].Command != "phpstan && phpunit" || f.PSR4[`App\`][0] != "src" {
		t.Fatalf("%+v", f)
	}
	c := f.Compact()
	for _, want := range []string{"composer package: acme/shop", "framework: symfony", "Docker Compose services", "PSR-4: App\\ → src"} {
		if !strings.Contains(c, want) {
			t.Fatal(want, c)
		}
	}
	if f.Hash() == "" || f.ConfigFiles()[0] != "composer.json" {
		t.Fatal(f.ConfigFiles())
	}
	// Planning: one entry-point page per kind, processes per handler
	// class.
	in := PlanInput{Files: files, Facts: f, Entries: eps}
	eitems := PlanEntryPoints(in)
	var ids []string
	for _, it := range eitems {
		ids = append(ids, it.ID)
	}
	if strings.Join(ids, ",") != "entrypoints-http,entrypoints-cli,entrypoints-async,entrypoints-cron,entrypoints-public" || eitems[0].MD != "entrypoints/http.md" || eitems[0].Inputs["@entries"] == "" {
		t.Fatal(ids)
	}
	procs, skipped := PlanProcesses(in)
	if skipped != 0 {
		t.Fatal(skipped)
	}
	var oc *Item
	for i := range procs {
		if procs[i].Group == `App\Controller\OrderController` {
			oc = &procs[i]
		}
	}
	if oc == nil || len(oc.Entries) != 3 || oc.Files[0] != "src/Controller/OrderController.php" || oc.Title != "OrderController (HTTP)" || oc.MD != "processes/app-controller-ordercontroller.md" {
		t.Fatalf("%+v", procs)
	}
	procs, skipped = PlanProcesses(PlanInput{Files: files, Facts: f, Entries: eps, MaxProcesses: 2})
	if len(procs) != 2 || skipped == 0 || procs[1].Group != `App\Controller\OrderController` && procs[0].Group != `App\Controller\OrderController` {
		t.Fatal(procs, skipped)
	}
	if ov := PlanOverview(in); ov.ID != "overview" || ov.Inputs["@facts"] != f.Hash() {
		t.Fatal(ov)
	}
	if it := PlanEntryPoints(PlanInput{Facts: f}); len(it) != 1 || it[0].ID != "entrypoints-discover" {
		t.Fatal(it)
	}
}

func TestEntryPointsLaravel(t *testing.T) {
	root := t.TempDir()
	write(t, root, map[string]string{
		"composer.json": `{"require":{"laravel/framework":"^11"},"autoload":{"psr-4":{"App\\":"app/"}}}`,
		"routes/web.php": `<?php
use App\Http\Controllers\OrderController;
use App\Http\Controllers\Admin;
use Illuminate\Support\Facades\Route;

Route::get('/', function () { return view('welcome'); });
Route::get('/orders', [OrderController::class, 'index'])->name('orders.index');
Route::post('orders', [OrderController::class, 'store']);
Route::put('/orders/{order}', 'OrderController@update');
Route::resource('photos', PhotoController::class);
Route::match(['get', 'post'], '/search', [Admin\SearchController::class, 'run']);
Route::view('/about', 'about');
Route::get('/invoke', ShowProfile::class);
`,
		"routes/api.php":     "<?php\nRoute::apiResource('posts', \\App\\Http\\Controllers\\Api\\PostController::class);\n",
		"routes/console.php": "<?php\nArtisan::command('inspire {--quiet}', function () {});\nSchedule::command('emails:send --force')->daily();\n",
		"app/Console/Kernel.php": `<?php
namespace App\Console;
class Kernel extends ConsoleKernel
{
    protected function schedule(Schedule $schedule): void
    {
        $schedule->job(new \App\Jobs\Heartbeat)->everyFiveMinutes();
    }
}
`,
		"app/Console/Commands/SendEmails.php": `<?php
namespace App\Console\Commands;
class SendEmails extends Command
{
    protected $signature = 'mail:send {user} {--queue}';
    public function handle(): void {}
}
`,
		"app/Jobs/ProcessPodcast.php":                "<?php\nnamespace App\\Jobs;\nclass ProcessPodcast implements ShouldQueue\n{\n    public function handle(AudioProcessor $p): void {}\n}\n",
		"app/Listeners/SendShipmentNotification.php": "<?php\nnamespace App\\Listeners;\nclass SendShipmentNotification\n{\n    public function handle(OrderShipped $event): void {}\n}\n",
		"public/index.php":                           "<?php\n",
		"server.php":                                 "<?php\n",
	})
	files := []string{"routes/web.php", "routes/api.php", "routes/console.php", "app/Console/Kernel.php", "app/Console/Commands/SendEmails.php", "app/Jobs/ProcessPodcast.php", "app/Listeners/SendShipmentNotification.php", "public/index.php", "server.php"}
	eps := ExtractEntryPoints(root, ".", files)
	has(t, eps,
		`http GET / → `,
		`http GET /orders → App\Http\Controllers\OrderController::index`,
		`http POST /orders → App\Http\Controllers\OrderController::store`,
		`http PUT /orders/{order} → App\Http\Controllers\OrderController::update`,
		`http resource /photos → PhotoController`,
		`http MATCH /search → App\Http\Controllers\Admin\SearchController::run`,
		`http GET /about → `,
		`http GET /invoke → ShowProfile::__invoke`,
		`http resource /posts → App\Http\Controllers\Api\PostController`,
		`cli inspire → `,
		`cli mail:send → App\Console\Commands\SendEmails::handle`,
		`cron schedule emails:send --force → `,
		`cron schedule job Heartbeat → App\Jobs\Heartbeat::handle`,
		`async job ProcessPodcast → App\Jobs\ProcessPodcast::handle`,
		`async listener for OrderShipped → App\Listeners\SendShipmentNotification::handle`,
		`public public/index.php → `,
	)
	for _, e := range eps {
		if e.File == "server.php" {
			t.Fatal("server.php is a dev router", e)
		}
	}
	// Resource controllers group as a class; a closure route groups by
	// its file.
	procs, _ := PlanProcesses(PlanInput{Entries: eps, Files: files})
	keys := map[string]bool{}
	for _, p := range procs {
		keys[p.Group] = true
	}
	if !keys[`App\Http\Controllers\OrderController`] || !keys["PhotoController"] || !keys["routes/web.php"] {
		t.Fatal(keys)
	}
}

func TestEntryPointsCakeSlimAndPlain(t *testing.T) {
	root := t.TempDir()
	write(t, root, map[string]string{
		"app/config/routes.php": `<?php
use Cake\Routing\RouteBuilder;
return function (RouteBuilder $routes): void {
    $routes->scope('/', function (RouteBuilder $builder): void {
        $builder->connect('/', ['controller' => 'Pages', 'action' => 'display', 'home']);
        $builder->connect('/pages/*', 'Pages::display');
        $builder->get('/articles/{id}', ['controller' => 'Articles', 'action' => 'view']);
        $builder->resources('Comments');
        $builder->fallbacks();
    });
};
`,
		"app/src/Command/UpdateTableCommand.php": `<?php
namespace App\Command;
use Cake\Command\Command;
use Cake\Console\ConsoleIo;
class UpdateTableCommand extends Command
{
    public function execute(Arguments $args, ConsoleIo $io): int { return 0; }
}
`,
		"app/src/Command/HelloCommand.php": `<?php
namespace App\Command;
class HelloCommand extends Command
{
    public static function defaultName(): string { return 'hi'; }
    public function execute(Arguments $args, ConsoleIo $io) {}
}
`,
		"app/src/Shell/CleanupShell.php": "<?php\nnamespace App\\Shell;\nuse Cake\\Console\\Shell;\nclass CleanupShell extends Shell\n{\n    public function main() {}\n}\n",
		"app/webroot/index.php":          "<?php\n",
		"slim/app/routes.php": `<?php
return function (App $app) {
    $app->get('/users/{id}', ViewUserAction::class);
    $app->post('/users', [UserController::class, 'create']);
    $app->group('/admin', function (Group $group) {
        $group->get('/stats', 'StatsController:show');
    });
};
`,
		"index.php":  "<?php\nswitch ($_GET['page']) {}\n",
		"ajax.php":   "<?php\necho json_encode([]);\n",
		"lib/Db.php": "<?php\nclass Db {}\n",
	})
	files := []string{"app/config/routes.php", "app/src/Command/UpdateTableCommand.php", "app/src/Command/HelloCommand.php", "app/src/Shell/CleanupShell.php", "app/webroot/index.php"}
	eps := ExtractEntryPoints(root, "app", files)
	has(t, eps,
		`http ANY / → App\Controller\PagesController::display`,
		`http ANY /pages/* → App\Controller\PagesController::display`,
		`http GET /articles/{id} → App\Controller\ArticlesController::view`,
		`http resource /comments → App\Controller\CommentsController`,
		`http ANY /{controller}/{action}/* (fallback routes) → `,
		`cli update_table → App\Command\UpdateTableCommand::execute`,
		`cli hi → App\Command\HelloCommand::execute`,
		`cli cleanup → App\Shell\CleanupShell::main`,
		`public app/webroot/index.php → `,
	)
	slim := ExtractEntryPoints(root, ".", []string{"slim/app/routes.php", "index.php", "ajax.php", "lib/Db.php"})
	has(t, slim,
		`http GET /users/{id} → ViewUserAction::__invoke`,
		`http POST /users → UserController::create`,
		`http GET /stats → StatsController::show`,
		`public index.php → `,
		`public ajax.php → `,
	)
	if len(slim) != 5 {
		t.Fatal(entryNames(slim))
	}
	// Plain PHP without composer: facts still work.
	f := GatherFacts(root, filepath.Join(root, "app"), files)
	f.DeriveStyle(slim)
	if f.PHPRoot != "app" || f.Framework != "" || f.Style != "mvc" || !strings.Contains(f.Compact(), "none detected") || !strings.Contains(f.Compact(), "PHP root: app") {
		t.Fatalf("%+v", f)
	}
	var cli Facts
	cli.DeriveStyle([]EntryPoint{{Kind: KindCLI}})
	var lib Facts
	lib.DeriveStyle(nil)
	if cli.Style != "cli" || lib.Style != "library" {
		t.Fatal(cli.Style, lib.Style)
	}
	if underscore("UpdateTable") != "update_table" || KindTitle("x") == "" {
		t.Fatal("underscore")
	}
	for _, k := range EntryKinds {
		if KindTitle(k) == "" {
			t.Fatal(k)
		}
	}
}

func TestClassFileAndComponents(t *testing.T) {
	files := []string{"src/Service/OrderService.php", "lib/Other/OrderService.php", "src/Controller/A.php"}
	psr := map[string][]string{`App\`: {"src"}}
	if ClassFile(`\App\Service\OrderService::save`, psr, files) != "src/Service/OrderService.php" {
		t.Fatal("psr4")
	}
	if ClassFile(`Other\OrderService`, nil, files) != "lib/Other/OrderService.php" || ClassFile(`X\A`, nil, files) != "src/Controller/A.php" || ClassFile("", psr, files) != "" {
		t.Fatal("fallback")
	}
	// Components rank the classes the process pages cite.
	dir := t.TempDir()
	m := emptyManifest()
	vt, vf := true, false
	put := func(id, title, group string, steps []Step) {
		d := ProcessDoc{Name: title, Summary: "s", Steps: steps}
		b := mustJSON(d)
		write(t, dir, map[string]string{"data/" + id + ".json": b})
		m.Items[id] = &ItemState{ID: id, Level: LevelProcesses, Title: title, Group: group, Status: StatusDone, Data: "data/" + id + ".json"}
	}
	put("process-a", "A (HTTP)", `App\Controller\A`, []Step{
		{Ref: Ref{File: "src/Controller/A.php", Symbol: `App\Controller\A::run`, Verified: &vt}},
		{Ref: Ref{File: "src/Service/OrderService.php", Symbol: `App\Service\OrderService::save`, Verified: &vt}},
		{Ref: Ref{File: "src/Repo.php", Symbol: `Repo::find`, Verified: &vt}},
		{Ref: Ref{File: "src/Gone.php", Symbol: `Gone::x`, Verified: &vf}},
		{Ref: Ref{File: "src/f.php", Symbol: `helper`, Verified: &vt}},
	})
	put("process-b", "B (CLI)", `App\Command\B`, []Step{
		{Ref: Ref{File: "src/Service/OrderService.php", Symbol: `OrderService::load`, Verified: &vt}},
		{Ref: Ref{File: "src/Service/OrderService.php", Symbol: `App\Service\OrderService::save`, Verified: &vt}},
	})
	m.Items["process-c"] = &ItemState{ID: "process-c", Level: LevelProcesses, Status: StatusDone, Data: "data/missing.json"}
	items := PlanComponents(dir, m, 0)
	if len(items) != 2 {
		t.Fatalf("%+v", items)
	}
	sort.Slice(items, func(i, j int) bool { return len(items[i].UsedBy) > len(items[j].UsedBy) })
	if items[0].Group != `App\Service\OrderService` || strings.Join(items[0].UsedBy, ",") != "A (HTTP),B (CLI)" || items[0].Files[0] != "src/Service/OrderService.php" || items[0].Title != "OrderService" || items[1].Group != "Repo" {
		t.Fatalf("%+v", items)
	}
	if one := PlanComponents(dir, m, 1); len(one) != 1 || one[0].Group != `App\Service\OrderService` {
		t.Fatal(one)
	}
	// The entry points the entry-point pages added feed the processes.
	ed := EntryPointsDoc{Summary: "s", Entries: []EntryDesc{{Name: "GET /x", Found: true}, {Name: "app:sync", Kind: KindCLI, Handler: Ref{File: "src/Sync.php", Symbol: `App\Sync::run`, Verified: &vt}},
		{Name: "GET /y", Handler: Ref{File: "src/Y.php", Verified: &vf}}, {Name: "odd", Kind: "weird", Handler: Ref{File: "src/O.php"}}}}
	write(t, dir, map[string]string{"data/entrypoints-cli.json": mustJSON(ed)})
	m.Items["entrypoints-cli"] = &ItemState{ID: "entrypoints-cli", Level: LevelEntryPoints, Group: KindCLI, Status: StatusDone, Data: "data/entrypoints-cli.json"}
	added := LoadEntryDocs(dir, m)
	if len(added) != 2 || added[0].Name != "app:sync" || added[0].Source != "agent" || added[1].Kind != KindHTTP {
		t.Fatal(added)
	}
	merged := MergeEntries([]EntryPoint{{Kind: KindCLI, Name: "app:sync"}}, append(added, EntryPoint{Kind: KindCLI, Name: "new"}))
	if len(merged) != 3 {
		t.Fatal(merged)
	}
	if PlanEntryPoints(PlanInput{Entries: added})[0].ID != "entrypoints-discover" {
		t.Fatal("agent entries plan no pages")
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
