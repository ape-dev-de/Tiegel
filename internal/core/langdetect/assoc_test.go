package langdetect

import "testing"

func TestAssociations(t *testing.T) {
	as, errs := ParseAssociations([]string{
		"*.tpl = PHP", "templates/*.html=xml", "Jenkinsfile=plaintext",
		"noequals", "=php", "*.x=cobol", "[=php",
	})
	if len(as) != 3 || len(errs) != 4 {
		t.Fatalf("as %+v errs %v", as, errs)
	}
	cases := []struct {
		path string
		want Language
		ok   bool
	}{
		{"/p/views/a.TPL", PHP, true},
		{"/p/templates/x.html", XML, true},
		{"/p/other/x.html", "", false},
		{"/p/Jenkinsfile", PlainText, true},
		{"a.php", "", false},
	}
	for _, c := range cases {
		got, ok := Match(as, c.path)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: %q %v", c.path, got, ok)
		}
	}
	if DetectWith(as, "/p/a.tpl", nil) != PHP || DetectWith(as, "/p/a.json", nil) != JSON || DetectWith(nil, "x.yml", nil) != YAML {
		t.Fatal("DetectWith")
	}
}
