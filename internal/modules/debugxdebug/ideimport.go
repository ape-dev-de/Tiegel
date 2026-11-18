package debugxdebug

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/ideimport"
)

// refDebugComponent holds the debugger settings of the reference IDE
// (application options).
const refDebugComponent = "PhpDebugGeneral"

// importComponent maps the Xdebug port (hook import.ide.component, M16).
// The first port of a list wins; break-at-first-line and similar options
// have no counterpart and are named in the report.
func importComponent(c ideimport.Component) ([]ideimport.Item, bool) {
	if c.Kind != ideimport.KindComponent || c.Name != refDebugComponent {
		return nil, false
	}
	el := c.Element
	ports := el.Attr("xdebug_debug_port")
	if v, ok := el.Option("xdebug_debug_port"); ok && ports == "" {
		ports = v
	}
	var unsupported []string
	for _, a := range el.Attrs {
		switch n := a.Name.Local; {
		case n == "xdebug_debug_port", n == "name":
		case strings.HasPrefix(n, "break_at_first_line") && a.Value == "true":
			unsupported = append(unsupported, "break at first line")
		}
	}
	first := strings.TrimSpace(strings.Split(ports, ",")[0])
	port, err := strconv.Atoi(first)
	if ports == "" || err != nil || port < 1 || port > 65535 {
		it := c.Item("Xdebug port", "no port set")
		if ports != "" {
			it.Detail = "invalid port " + ports
		}
		it.Status = ideimport.StatusSkipped
		return []ideimport.Item{it}, true
	}
	detail := fmt.Sprintf("port %d", port)
	if strings.Contains(ports, ",") {
		detail += " (first of " + ports + "; Tiegel listens on one port)"
	}
	if len(unsupported) > 0 {
		detail = ideimport.JoinDetail(detail, "not supported: "+strings.Join(unsupported, ", "))
	}
	return []ideimport.Item{c.Item("Xdebug port", detail, c.Setting(SettingPort, port))}, true
}
