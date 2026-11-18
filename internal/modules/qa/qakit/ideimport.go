package qakit

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/ideimport"
)

// ImportSpec names the components of the reference IDE's project
// configuration that describe the tool (M16, hook import.ide.component).
// The names live in the tool's definition; qakit only interprets their
// common structure.
type ImportSpec struct {
	// Settings is the component with the tool path (attribute tool_path)
	// or the interpreter choice (an element named *_by_interpreter) and
	// the timeout (attribute timeout, milliseconds).
	Settings string
	// Options is the component with the tool options; ConfigOptions are
	// the option names holding the configuration file (searched at any
	// depth, first non-empty wins).
	Options       string
	ConfigOptions []string
	// StandardOption names the option with a coding standard (a ruleset
	// name such as PSR12 when no file is configured); "" for none.
	StandardOption string
	// Formatter is the value of the external formatter choice that
	// selects this tool for Reformat Code ("" for analyzers).
	Formatter string
}

// FormatterComponent and FormatterOption hold the project's choice of
// an external formatter in the reference IDE's configuration.
const (
	FormatterComponent = "PhpExternalFormatter"
	FormatterOption    = "externalFormatter"
)

// importComponent maps the components of the tool.
func (p *Plugin) importComponent(c ideimport.Component) ([]ideimport.Item, bool) {
	spec := p.def.Import
	if spec == nil || c.Kind != ideimport.KindComponent {
		return nil, false
	}
	switch c.Name {
	case spec.Settings:
		return []ideimport.Item{p.importSettings(c)}, true
	case spec.Options:
		return []ideimport.Item{p.importOptions(c)}, true
	case FormatterComponent:
		v, _ := c.Element.Option(FormatterOption)
		if spec.Formatter == "" || p.def.Format == nil || !strings.EqualFold(v, spec.Formatter) {
			return nil, false
		}
		return []ideimport.Item{c.Item(p.def.Name+": Reformat Code", "used for Reformat Code", c.Setting(p.key(KeyReformat), true))}, true
	}
	return nil, false
}

// importSettings reads tool path, interpreter use and timeout.
func (p *Plugin) importSettings(c ideimport.Component) ideimport.Item {
	var changes []ideimport.Change
	var parts []string
	byInterp := false
	path := ""
	timeout := 0
	c.Element.Walk(func(e *ideimport.Element) bool {
		if strings.HasSuffix(e.Name, "_by_interpreter") {
			byInterp = true
		}
		if v := e.Attr("tool_path"); v != "" && path == "" {
			path = c.Path(v)
		}
		if v := e.Attr("timeout"); v != "" && timeout == 0 {
			if ms, err := strconv.Atoi(v); err == nil && ms > 0 {
				timeout = min(max((ms+999)/1000, 1), MaxTimeout)
			}
		}
		return true
	})
	if path != "" {
		changes = append(changes, c.Setting(p.key(KeyPath), path))
		parts = append(parts, path)
	}
	if byInterp || path != "" {
		changes = append(changes, c.Setting(p.key(KeyUseInterpreter), byInterp))
		if byInterp {
			parts = append(parts, "in the project interpreter")
		} else {
			parts = append(parts, "on the host")
		}
	}
	if timeout > 0 {
		changes = append(changes, c.Setting(p.key(KeyTimeout), timeout))
		parts = append(parts, fmt.Sprintf("timeout %d s", timeout))
	}
	it := c.Item(p.def.Name+": tool", strings.Join(parts, ", "), changes...)
	if len(changes) == 0 {
		it.Status, it.Detail = ideimport.StatusSkipped, "no tool path or interpreter set"
	}
	return it
}

// importOptions reads the configuration file (or coding standard).
func (p *Plugin) importOptions(c ideimport.Component) ideimport.Item {
	spec := p.def.Import
	config := ""
	c.Element.Walk(func(e *ideimport.Element) bool {
		if config != "" {
			return false
		}
		if e.Name == "option" {
			for _, n := range spec.ConfigOptions {
				if e.Attr("name") == n && strings.TrimSpace(e.Attr("value")) != "" {
					config = c.Path(e.Attr("value"))
				}
			}
		}
		return true
	})
	if config == "" && spec.StandardOption != "" {
		if v, ok := c.Element.Option(spec.StandardOption); ok && v != "" && !strings.EqualFold(v, "Custom") {
			config = v
		}
	}
	if config == "" {
		it := c.Item(p.def.Name+": configuration", "no configuration file; the tool finds its own")
		it.Status = ideimport.StatusSkipped
		return it
	}
	return c.Item(p.def.Name+": configuration", config, c.Setting(p.key(KeyConfig), config))
}
