package phpunit

import (
	"fmt"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/ideimport"
	"github.com/ape-dev-de/tiegel/internal/run"
)

// Components of the reference IDE's configuration this plugin imports
// (hook import.ide.component, M16).
const (
	refComponent    = "PhpUnit"
	refVersionCache = "PhpTestFrameworkVersionCache"
	refRunType      = "PHPUnitRunConfigurationType"
)

// importComponent maps the PHPUnit settings and run configurations.
func importComponent(c ideimport.Component) ([]ideimport.Item, bool) {
	switch {
	case c.Kind == ideimport.KindRunConfiguration && c.Name == refRunType:
		return []ideimport.Item{importRunConfig(c)}, true
	case c.Kind != ideimport.KindComponent:
		return nil, false
	case c.Name == refVersionCache:
		it := c.Item("PHPUnit version cache", "Tiegel detects the PHPUnit version itself")
		it.Status = ideimport.StatusSkipped
		return []ideimport.Item{it}, true
	case c.Name == refComponent:
		return []ideimport.Item{importSettings(c)}, true
	}
	return nil, false
}

// importSettings maps the configuration file and a PHAR runner.
func importSettings(c ideimport.Component) ideimport.Item {
	var changes []ideimport.Change
	var parts []string
	for _, s := range c.Element.Find("PhpUnitSettings") {
		if use, ok := s.LookupAttr("use_configuration_file"); !ok || use == "true" {
			if p := c.Path(s.Attr("configuration_file_path")); p != "" {
				changes = append(changes, c.Setting(SettingConfig, p))
				parts = append(parts, "configuration "+p)
			}
		}
		if p := c.Path(s.Attr("phpunit_phar_path")); p != "" && s.Attr("load_method") == "PHPUNIT_PHAR" {
			changes = append(changes, c.Setting(SettingExecutable, p))
			parts = append(parts, "runner "+p)
		}
		if s.Attr("custom_loader_path") != "" {
			parts = append(parts, "autoloader from Composer")
		}
	}
	it := c.Item("PHPUnit settings", strings.Join(parts, ", "), changes...)
	if len(changes) == 0 {
		it.Status = ideimport.StatusSkipped
		it.Detail = ideimport.JoinDetail(it.Detail, "Tiegel finds phpunit.xml and vendor/bin/phpunit itself")
	}
	return it
}

// importRunConfig maps a PHPUnit run configuration to the type "phpunit".
func importRunConfig(c ideimport.Component) ideimport.Item {
	el := c.Element
	name := el.Attr("name")
	tr := el.Child("TestRunner")
	vals := map[string]any{}
	var notes []string
	args := strings.TrimSpace(tr.Attr("options"))
	suite := tr.Attr("test_suite")
	if suite == "" {
		// --testsuite NAME in the options selects a suite as well.
		if words, err := run.SplitWords(args); err == nil {
			for i := 0; i < len(words)-1; i++ {
				if words[i] == "--testsuite" {
					suite = words[i+1]
					words = append(words[:i], words[i+2:]...)
					args = strings.Join(words, " ")
					break
				}
			}
		}
	}
	file := c.Path(tr.Attr("file"))
	switch strings.ToLower(tr.Attr("scope")) {
	case "class":
		vals[fieldScope], vals[fieldClass] = scopeClass, tr.Attr("class")
		if file != "" {
			vals[fieldPath] = file
		}
	case "method":
		vals[fieldScope], vals[fieldClass], vals[fieldMethod] = scopeMethod, tr.Attr("class"), tr.Attr("method")
		if file != "" {
			vals[fieldPath] = file
		}
	case "file":
		vals[fieldScope], vals[fieldPath] = scopePath, file
	case "directory":
		vals[fieldScope], vals[fieldPath] = scopePath, c.Path(tr.Attr("directory"))
	case "group":
		vals[fieldScope] = scopeAll
		notes = append(notes, "groups are passed as --group")
		if g := tr.Attr("groups"); g != "" {
			args = strings.TrimSpace(args + " --group " + g)
		}
	default: // XML: the whole configuration or one suite
		vals[fieldScope] = scopeAll
	}
	if suite != "" {
		vals[fieldScope], vals[fieldSuite] = scopeSuite, suite
	}
	if cfg := c.Path(tr.Attr("configuration_file")); cfg != "" && tr.Attr("use_alternative_configuration_file") == "true" {
		args = strings.TrimSpace(args + " --configuration " + cfg)
	}
	if args != "" {
		vals[fieldArgs] = args
	}
	if env := ideimport.RunEnv(el); len(env) > 0 {
		vals[fieldEnv] = env
	}
	if s, _ := vals[fieldPath].(string); vals[fieldScope] == scopePath && s == "" {
		it := c.Item("Run configuration "+name, "no test file or directory")
		it.Area, it.Status = ideimport.AreaRun, ideimport.StatusSkipped
		return it
	}
	detail := fmt.Sprintf("PHPUnit %s", vals[fieldScope])
	if len(notes) > 0 {
		detail += "; " + strings.Join(notes, "; ")
	}
	return ideimport.RunConfigItem(c, run.Config{Name: name, Type: ID, Values: vals}, detail)
}
