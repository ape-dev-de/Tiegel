package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
)

// Compose labels docker puts on containers of a compose project.
const (
	LabelComposeProject = "com.docker.compose.project"
	LabelComposeService = "com.docker.compose.service"
)

// Port is a port of a container. Published is 0 for a port that is only
// exposed inside the Docker network.
type Port struct {
	// IP the port is published on ("0.0.0.0", "::", "127.0.0.1"; empty
	// when not published).
	IP        string `json:"ip,omitempty"`
	Published int    `json:"published,omitempty"`
	Target    int    `json:"target"`
	Protocol  string `json:"protocol"`
	// URL is an http:// link to the published port (TCP only); the GUI
	// opens it in the browser on click only.
	URL string `json:"url,omitempty"`
}

// PortURL returns the http:// URL of a published TCP port. Wildcard
// addresses (0.0.0.0, ::, empty) are replaced by host (default
// "localhost"); unpublished or non-TCP ports have no URL.
func PortURL(ip string, published int, protocol, host string) string {
	if published <= 0 || (protocol != "" && !strings.EqualFold(protocol, "tcp")) {
		return ""
	}
	if host == "" {
		host = "localhost"
	}
	switch ip {
	case "", "0.0.0.0", "::", "[::]":
	default:
		host = strings.Trim(ip, "[]")
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(published))
}

// SortPorts orders ports by published port, target port and protocol and
// drops duplicates that differ only in the wildcard address (docker lists
// a port once for IPv4 and once for IPv6).
func SortPorts(ports []Port) []Port {
	sort.SliceStable(ports, func(i, j int) bool {
		a, b := ports[i], ports[j]
		if a.Published != b.Published {
			return a.Published < b.Published
		}
		if a.Target != b.Target {
			return a.Target < b.Target
		}
		return a.Protocol < b.Protocol
	})
	out := ports[:0]
	seen := map[string]bool{}
	for _, p := range ports {
		ip := p.IP
		if ip == "::" || ip == "[::]" {
			ip = "0.0.0.0"
		}
		key := fmt.Sprintf("%s|%d|%d|%s", ip, p.Published, p.Target, p.Protocol)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, p)
	}
	return out
}

// ParsePorts parses the Ports column of `docker ps`, e.g.
// "0.0.0.0:8080->80/tcp, :::8080->80/tcp, 9000/tcp". Ranges
// ("8000-8001->8000-8001/tcp") are expanded.
func ParsePorts(s, host string) []Port {
	var out []Port
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		pub, target, hasArrow := strings.Cut(part, "->")
		if !hasArrow {
			target, pub = pub, ""
		}
		target, proto, _ := strings.Cut(target, "/")
		if proto == "" {
			proto = "tcp"
		}
		ip, pubPort := "", ""
		if pub != "" {
			i := strings.LastIndex(pub, ":")
			if i < 0 {
				continue
			}
			ip, pubPort = pub[:i], pub[i+1:]
			if ip == "::" || ip == "" {
				ip = "::"
			}
		}
		tFrom, tTo, ok := portRange(target)
		if !ok {
			continue
		}
		pFrom, pTo := 0, 0
		if pubPort != "" {
			if pFrom, pTo, ok = portRange(pubPort); !ok {
				continue
			}
		}
		for i := 0; tFrom+i <= tTo; i++ {
			p := Port{Target: tFrom + i, Protocol: proto}
			if pFrom > 0 && pFrom+i <= pTo {
				p.IP, p.Published = ip, pFrom+i
				p.URL = PortURL(ip, p.Published, proto, host)
			}
			out = append(out, p)
		}
	}
	return SortPorts(out)
}

func portRange(s string) (int, int, bool) {
	a, b, isRange := strings.Cut(s, "-")
	from, err := strconv.Atoi(a)
	if err != nil {
		return 0, 0, false
	}
	to := from
	if isRange {
		if to, err = strconv.Atoi(b); err != nil || to < from || to-from > 1000 {
			return 0, 0, false
		}
	}
	return from, to, true
}

// Container is a container listed by `docker ps`.
type Container struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Image  string `json:"image"`
	State  string `json:"state"`
	Status string `json:"status"`
	Ports  []Port `json:"ports,omitempty"`
	// ComposeProject and ComposeService come from the compose labels;
	// empty for containers started without compose.
	ComposeProject string `json:"composeProject,omitempty"`
	ComposeService string `json:"composeService,omitempty"`
}

type rawContainer struct {
	ID     string `json:"ID"`
	Names  string `json:"Names"`
	Image  string `json:"Image"`
	State  string `json:"State"`
	Status string `json:"Status"`
	Ports  string `json:"Ports"`
	Labels string `json:"Labels"`
}

// ParseLabels parses the Labels column of `docker ps` ("a=b,c=d").
// Values containing commas cannot be told apart from the separator; the
// compose labels never contain one.
func ParseLabels(s string) map[string]string {
	out := map[string]string{}
	for _, kv := range strings.Split(s, ",") {
		k, v, _ := strings.Cut(kv, "=")
		if k = strings.TrimSpace(k); k != "" {
			out[k] = v
		}
	}
	return out
}

// ParseContainers parses `docker ps --format json`: one JSON object per
// line, or a JSON array. host replaces wildcard addresses in port URLs.
func ParseContainers(data []byte, host string) ([]Container, error) {
	trimmed := bytes.TrimSpace(data)
	var raws []rawContainer
	switch {
	case len(trimmed) == 0:
	case trimmed[0] == '[':
		if err := json.Unmarshal(trimmed, &raws); err != nil {
			return nil, fmt.Errorf("docker: parse ps: %w", err)
		}
	default:
		sc := bufio.NewScanner(bytes.NewReader(trimmed))
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			line := bytes.TrimSpace(sc.Bytes())
			if len(line) == 0 {
				continue
			}
			var r rawContainer
			if err := json.Unmarshal(line, &r); err != nil {
				return nil, fmt.Errorf("docker: parse ps: %w", err)
			}
			raws = append(raws, r)
		}
		if err := sc.Err(); err != nil {
			return nil, fmt.Errorf("docker: read ps: %w", err)
		}
	}
	out := make([]Container, 0, len(raws))
	for _, r := range raws {
		labels := ParseLabels(r.Labels)
		name, _, _ := strings.Cut(r.Names, ",")
		out = append(out, Container{
			ID: r.ID, Name: name, Image: r.Image, State: r.State, Status: r.Status,
			Ports:          ParsePorts(r.Ports, host),
			ComposeProject: labels[LabelComposeProject], ComposeService: labels[LabelComposeService],
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Containers runs `docker ps --all --format json` (contacts the engine).
// host replaces wildcard addresses in port URLs.
func (c Client) Containers(ctx context.Context, host string) ([]Container, error) {
	out, err := c.Output(ctx, "ps", "--all", "--no-trunc", "--format", "json")
	if err != nil {
		return nil, err
	}
	return ParseContainers(out, host)
}

// LinkHost returns the host that published ports of an engine are reached
// at: the host of a tcp:// or ssh:// endpoint, else "localhost".
func LinkHost(endpoint string) string {
	if !IsRemote(endpoint) {
		return "localhost"
	}
	_, rest, _ := strings.Cut(endpoint, "://")
	if i := strings.LastIndex(rest, "@"); i >= 0 {
		rest = rest[i+1:]
	}
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	if h, _, err := net.SplitHostPort(rest); err == nil {
		return h
	}
	return strings.Trim(rest, "[]")
}
