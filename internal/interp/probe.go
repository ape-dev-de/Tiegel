package interp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Info is what one probe run learns about an interpreter.
type Info struct {
	// Version is PHP_VERSION, e.g. "8.3.4".
	Version string `json:"version"`
	// SAPI is PHP_SAPI ("cli").
	SAPI string `json:"sapi,omitempty"`
	// OS is PHP_OS ("Linux", "Darwin", "WINNT").
	OS string `json:"os,omitempty"`
	// Binary is PHP_BINARY as seen by the interpreter (container path for
	// containers).
	Binary string `json:"binary,omitempty"`
	// Extensions are the loaded extensions, ZendExtensions the loaded Zend
	// extensions (Xdebug, OPcache); both sorted case-insensitively.
	Extensions     []string `json:"extensions"`
	ZendExtensions []string `json:"zendExtensions,omitempty"`
	// Xdebug is the Xdebug version; empty when Xdebug is not loaded.
	Xdebug string `json:"xdebug,omitempty"`
	// IniFile is the loaded php.ini; empty when none is loaded.
	IniFile string `json:"iniFile,omitempty"`
	// IniScanned are the additional .ini files (conf.d).
	IniScanned []string `json:"iniScanned,omitempty"`
	// Ini holds the values of IniKeys known to the interpreter.
	Ini IniValues `json:"ini,omitempty"`
}

// probeMarker precedes the JSON line of the probe script, so output of
// auto_prepend files or entrypoints before it is ignored.
const probeMarker = "__TIEGEL_PROBE__"

// probeScript prints everything Info needs as one JSON line. %s is the
// quoted list of IniKeys.
const probeScript = `$v=[];foreach([%s] as $k){$g=ini_get($k);if($g!==false){$v[$k]=$g;}}` +
	`$s=php_ini_scanned_files();$x=phpversion('xdebug');` +
	`echo "\n` + probeMarker + `",json_encode(['version'=>PHP_VERSION,'sapi'=>PHP_SAPI,'os'=>PHP_OS,'binary'=>PHP_BINARY,` +
	`'extensions'=>get_loaded_extensions(),'zendExtensions'=>get_loaded_extensions(true),'xdebug'=>$x===false?'':$x,` +
	`'iniFile'=>(string)php_ini_loaded_file(),'iniScanned'=>$s===false?[]:array_values(array_filter(array_map('trim',explode(',',$s)))),` +
	`'ini'=>(object)$v]),"\n";`

// ProbeScript returns the PHP code (for `php -r`) that Probe runs.
func ProbeScript() string {
	quoted := make([]string, len(IniKeys))
	for n, k := range IniKeys {
		quoted[n] = "'" + k + "'"
	}
	return fmt.Sprintf(probeScript, strings.Join(quoted, ","))
}

// Probe runs one `php -r` script on i and returns version, extensions,
// Xdebug version and ini files.
func Probe(ctx context.Context, i Interpreter) (Info, error) {
	out, err := Run(ctx, i, "-r", ProbeScript())
	if err != nil {
		return Info{}, err
	}
	return ParseProbe(out)
}

// ParseProbe parses the output of the probe script.
func ParseProbe(out []byte) (Info, error) {
	i := bytes.Index(out, []byte(probeMarker))
	if i < 0 {
		return Info{}, errors.New("interp: probe printed no result (is it PHP ≥ 7?)")
	}
	line := out[i+len(probeMarker):]
	if j := bytes.IndexByte(line, '\n'); j >= 0 {
		line = line[:j]
	}
	var info Info
	if err := json.Unmarshal(bytes.TrimSpace(line), &info); err != nil {
		return Info{}, fmt.Errorf("interp: parse probe result: %w", err)
	}
	if info.Version == "" {
		return Info{}, errors.New("interp: probe result without version")
	}
	sortFold(info.Extensions)
	sortFold(info.ZendExtensions)
	if info.Extensions == nil {
		info.Extensions = []string{}
	}
	return info, nil
}

// HasExtension reports whether the extension (or Zend extension) name is
// loaded, ignoring case.
func (i Info) HasExtension(name string) bool {
	for _, l := range [][]string{i.Extensions, i.ZendExtensions} {
		for _, e := range l {
			if strings.EqualFold(e, name) {
				return true
			}
		}
	}
	return false
}

func sortFold(s []string) {
	sort.Slice(s, func(a, b int) bool { return strings.ToLower(s[a]) < strings.ToLower(s[b]) })
}
