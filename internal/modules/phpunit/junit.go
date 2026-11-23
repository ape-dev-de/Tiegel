package phpunit

import (
	"encoding/xml"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/teamcity"
)

// JUnit log (D536): the TeamCity protocol carries no assertion counts,
// so runs also write PHPUnit's JUnit log to stderr ("--log-junit
// php://stderr": no temporary file, no path mapping, works inside
// containers). The XML is taken out of the output and annotates the
// result tree with assertions and declaration lines when it is complete.

// junitTarget is the --log-junit argument.
const junitTarget = "php://stderr"

// useJUnit reports whether a run may add the JUnit log: not with
// ParaTest, not when the user logs JUnit already (configuration file or
// arguments) – that log must keep going where it is configured.
func useJUnit(parallel bool, config string, args []string) bool {
	if parallel {
		return false
	}
	for _, a := range args {
		if a == "--log-junit" || strings.HasPrefix(a, "--log-junit=") {
			return false
		}
	}
	if config != "" {
		if b, err := os.ReadFile(config); err == nil && strings.Contains(string(b), "<junit") {
			return false
		}
	}
	return true
}

// junitCollector takes the JUnit XML out of the output lines.
type junitCollector struct {
	inside bool
	buf    strings.Builder
	// done is set after a complete document was applied.
	done bool
}

// feed reports whether line belongs to the JUnit XML; a complete
// document is applied to tree.
func (j *junitCollector) feed(line string, tree *teamcity.Tree) bool {
	t := strings.TrimSpace(line)
	if !j.inside {
		if j.done || !(strings.HasPrefix(t, "<?xml") || strings.HasPrefix(t, "<testsuites")) {
			return false
		}
		j.inside = true
		j.buf.Reset()
	}
	j.buf.WriteString(line)
	j.buf.WriteByte('\n')
	if strings.Contains(t, "</testsuites>") || strings.HasSuffix(t, "<testsuites/>") {
		j.inside, j.done = false, true
		applyJUnit(j.buf.String(), tree)
		j.buf.Reset()
	}
	return true
}

type junitSuite struct {
	Suites []junitSuite `xml:"testsuite"`
	Cases  []junitCase  `xml:"testcase"`
}

type junitCase struct {
	Name       string `xml:"name,attr"`
	Class      string `xml:"class,attr"`
	ClassName  string `xml:"classname,attr"`
	Line       string `xml:"line,attr"`
	Assertions string `xml:"assertions,attr"`
}

// applyJUnit annotates tree with the test cases of a JUnit document; it
// returns the number of tests found in the tree.
func applyJUnit(doc string, tree *teamcity.Tree) int {
	var root struct {
		Suites []junitSuite `xml:"testsuite"`
	}
	if err := xml.Unmarshal([]byte(doc), &root); err != nil {
		return 0
	}
	n := 0
	var walk func([]junitSuite)
	walk = func(ss []junitSuite) {
		for _, s := range ss {
			for _, c := range s.Cases {
				class := c.Class
				if class == "" {
					class = strings.ReplaceAll(c.ClassName, ".", `\`)
				}
				class = strings.TrimPrefix(class, `\`)
				a, _ := strconv.Atoi(c.Assertions)
				line, _ := strconv.Atoi(c.Line)
				for _, id := range junitIDs(class, c.Name) {
					if tree.Annotate(id, a, line) {
						n++
						break
					}
				}
			}
			walk(s.Suites)
		}
	}
	walk(root.Suites)
	return n
}

// junitIDs are the tree IDs a JUnit test case may have: PHPUnit names
// data sets "method with data set #0" like the TeamCity location hint;
// older versions add the data in parentheses.
func junitIDs(class, name string) []string {
	ids := []string{class + "::" + name}
	if i := strings.Index(name, " with data set "); i >= 0 {
		ds := name[i+len(" with data set "):]
		if j := strings.Index(ds, " ("); j > 0 {
			ids = append(ids, class+"::"+name[:i]+" with data set "+ds[:j])
		}
	}
	return slices.Compact(ids)
}
